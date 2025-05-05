package server

import (
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"net/http"
)

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
	Message     string                 `json:"message"`
	Model       string                 `json:"model"`
	Temperature float64                `json:"temperature"`
	Seed        int                    `json:"seed"`
	Stream      bool                   `json:"stream"`
	OtherParams map[string]interface{} `json:"-"` // Catches other params for forwarding
}

// UnmarshalJSON custom unmarshaller to capture extra fields.
func (cr *CompletionRequest) UnmarshalJSON(data []byte) error {
	type Alias CompletionRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(cr),
	}

	// First, unmarshal known fields
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Then, unmarshal into a map to capture unknown fields
	var allFields map[string]interface{}
	if err := json.Unmarshal(data, &allFields); err != nil {
		return err
	}

	// Remove known fields from the map
	delete(allFields, "mode")
	delete(allFields, "session_id")
	delete(allFields, "message")
	delete(allFields, "model")
	delete(allFields, "temperature")
	delete(allFields, "seed")
	delete(allFields, "stream")

	// Store remaining fields
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

	// --- Prepare request for LlamaClient ---
	llamaReq := make(map[string]interface{})

	// Copy explicitly some and other parameters
	llamaReq["model"] = clientReq.Model
	llamaReq["temperature"] = clientReq.Temperature
	llamaReq["seed"] = clientReq.Seed
	llamaReq["stream"] = clientReq.Stream
	for k, v := range clientReq.OtherParams {
		llamaReq[k] = v
	}

	var err error
	if clientReq.Mode == "raw" {
		// Default history length for raw context retrieval
		const rawHistoryLength = 20
		textContext, err := s.sessionManager.GetTextSessionContext(clientReq.SessionID, rawHistoryLength)
		if err != nil {
			log.Errorf("Failed to get raw session context for %s: %v", clientReq.SessionID, err)
			http.Error(w, "Failed to retrieve session context", http.StatusInternalServerError)
			return
		}
		// Construct prompt for raw mode
		prompt := textContext + "<|im_start|>user\n" + clientReq.Message + "<|im_end|>\n"
		llamaReq["prompt"] = prompt

	} else if clientReq.Mode == "tokenized" {
		tokenizedContext, err := s.redisContextStorage.GetTokenizedSessionContext(clientReq.SessionID)
		if err != nil {
			// Log error but proceed, context might not exist yet
			log.Warnf("Failed to get tokenized session context for %s (proceeding without): %v", clientReq.SessionID, err)
		}
		// Set prompt directly to the message
		llamaReq["prompt"] = clientReq.Message
		// Add context only if it exists
		if tokenizedContext != nil {
			llamaReq["context"] = tokenizedContext
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
	// Add other endpoints here (e.g., /health, /session)

	log.Infof("Starting server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
