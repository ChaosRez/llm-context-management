package server

import (
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"net/http"
)

const rawHistoryLength = 20
const sessionDurationDays = 1

// Server holds dependencies for the HTTP server.
type Server struct {
	llamaService        *Llama.LlamaClient
	sessionManager      *SessionManager.SQLiteSessionManager // NOTE Assuming SQLite
	redisContextStorage *ContextStorage.RedisContextStorage
}

// NewServer creates a new Server instance.
func NewServer(
	llama *Llama.LlamaClient,
	sm *SessionManager.SQLiteSessionManager,
	cs *ContextStorage.RedisContextStorage,
) *Server {
	return &Server{
		llamaService:        llama,
		sessionManager:      sm,
		redisContextStorage: cs,
	}
}

// CompletionRequest defines the expected structure of the incoming JSON request.
type CompletionRequest struct {
	Mode        string                 `json:"mode"` // "raw" or "tokenized"
	SessionID   string                 `json:"session_id"`
	Prompt      string                 `json:"prompt"`
	Model       string                 `json:"model"`
	Temperature float64                `json:"temperature"`
	Seed        int                    `json:"seed"`
	Stream      bool                   `json:"stream"`
	OtherParams map[string]interface{} `json:"-"` // Catches other params for forwarding
}

// UnmarshalJSON custom unmarshaller to capture extra fields and disallow "context".
func (cr *CompletionRequest) UnmarshalJSON(data []byte) error {
	type Alias CompletionRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(cr),
	}

	// First, unmarshal known fields
	if err := json.Unmarshal(data, &aux); err != nil {
		// Check if the error is specifically about the "context" field if needed,
		// but the map check below is more robust for catching it explicitly.
		return err
	}

	// Then, unmarshal into a map to capture unknown fields and check for disallowed keys
	var allFields map[string]interface{}
	if err := json.Unmarshal(data, &allFields); err != nil {
		return err
	}

	// Check if the disallowed "context" key exists
	if _, found := allFields["context"]; found {
		return errors.New("the 'context' field is not allowed in the request body")
	}

	// Remove known fields that are explicitly handled
	delete(allFields, "mode")
	delete(allFields, "session_id")
	delete(allFields, "prompt")
	delete(allFields, "model")
	delete(allFields, "temperature")
	delete(allFields, "seed")
	delete(allFields, "stream")

	// Store remaining fields as OtherParams
	cr.OtherParams = allFields
	return nil
}

// handleCompletion handles requests to the /completion endpoint.
func (s *Server) handleCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var clientReq CompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&clientReq); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// If no session_id, create one
	if clientReq.SessionID == "" {
		sessionID, err := s.sessionManager.CreateSession("auto", sessionDurationDays)
		if err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
		clientReq.SessionID = sessionID
		log.Infof("Created new session ID: %s", clientReq.SessionID) // Log the new session ID
	} else {
		log.Infof("Using existing session ID: %s", clientReq.SessionID) // Log the provided session ID
	}

	llamaReq := make(map[string]interface{})

	// Copy explicitly known parameters
	llamaReq["model"] = clientReq.Model
	llamaReq["temperature"] = clientReq.Temperature
	llamaReq["seed"] = clientReq.Seed
	llamaReq["stream"] = clientReq.Stream
	// Copy other parameters captured in OtherParams
	for k, v := range clientReq.OtherParams {
		llamaReq[k] = v
	}

	var err error
	if clientReq.Mode == "raw" {
		// Default history length for raw context retrieval
		textContext, err := s.sessionManager.GetTextSessionContext(clientReq.SessionID, rawHistoryLength)
		if err != nil {
			log.Errorf("Failed to get raw session context for %s: %v", clientReq.SessionID, err)
			http.Error(w, "Failed to retrieve session context", http.StatusInternalServerError)
			return
		}
		prompt := textContext + "<|im_start|>user\n" + clientReq.Prompt + "<|im_end|>\n"
		llamaReq["prompt"] = prompt

	} else if clientReq.Mode == "tokenized" {
		tokenizedContext, err := s.redisContextStorage.GetTokenizedSessionContext(clientReq.SessionID)
		if err != nil {
			// Log error but proceed, context might not exist yet
			log.Warnf("Failed to get tokenized session context for %s (proceeding without): %v", clientReq.SessionID, err)
		}
		llamaReq["prompt"] = clientReq.Prompt
		// Add the retrieved tokenized context if available
		if tokenizedContext != nil {
			llamaReq["context"] = tokenizedContext // This key is added internally, not accepted from client
		}
	} else {
		http.Error(w, fmt.Sprintf("Invalid mode: %s. Use 'raw' or 'tokenized'", clientReq.Mode), http.StatusBadRequest)
		return
	}

	// --- Call LlamaClient ---
	resp, err := s.llamaService.Completion(llamaReq)
	if err != nil {
		log.Errorf("Llama completion error: %v", err)
		http.Error(w, "Error processing completion request", http.StatusInternalServerError)
		return
	}

	// --- Add session_id to the response ---
	// Ensure resp is not nil before adding the session ID
	if resp == nil {
		resp = make(map[string]interface{}) // Initialize if nil
	}
	resp["session_id"] = clientReq.SessionID // Add session_id (original or generated)

	// --- Send response ---
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Errorf("Failed to write response: %v", err)
		// Error already sent potentially, or response started. Log only.
	}
}

// Start runs the HTTP server.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/completion", s.handleCompletion)
	log.Infof("Starting server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
