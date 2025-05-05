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
const defaultUserID = "default_user" // Default user ID if none provided

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
	SessionID   string                 `json:"session_id,omitempty"`
	UserID      string                 `json:"user_id,omitempty"` // UserID field
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
	delete(allFields, "user_id")
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
	log.Infof("Received completion request from %s", r.RemoteAddr)
	if r.Method != http.MethodPost {
		log.Warnf("Invalid method %s received from %s", r.Method, r.RemoteAddr)
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var clientReq CompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&clientReq); err != nil {
		log.Errorf("Failed to decode request body from %s: %v", r.RemoteAddr, err)
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	log.Debugf("Decoded request: Mode=%s, SessionID=%s, UserID=%s, Model=%s", clientReq.Mode, clientReq.SessionID, clientReq.UserID, clientReq.Model)

	// Determine the effective UserID (from request or default)
	effectiveUserID := clientReq.UserID
	if effectiveUserID == "" {
		effectiveUserID = defaultUserID
		log.Warnf("No UserID provided in request, using default: %s", effectiveUserID)
	}

	// If no session_id, create one using the effective UserID
	if clientReq.SessionID == "" {
		log.Infof("No session_id provided, creating a new session for user '%s'.", effectiveUserID)
		sessionID, err := s.sessionManager.CreateSession(effectiveUserID, sessionDurationDays) // Use effective userID
		if err != nil {
			log.Errorf("Failed to create session for user '%s': %v", effectiveUserID, err)
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
		clientReq.SessionID = sessionID
		log.Infof("Created new session ID: %s for user %s", clientReq.SessionID, effectiveUserID)
	} else {
		// TODO: validate if the provided sessionID belongs to the effectiveUserID.
		// For now, we just log the provided session ID and the effective UserID from the request/default.
		log.Infof("Using existing session ID: %s (Effective UserID from request/default: %s)", clientReq.SessionID, effectiveUserID)
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
	log.Debugf("Prepared Llama request parameters for session %s (excluding prompt/context)", clientReq.SessionID)

	var err error
	if clientReq.Mode == "raw" {
		log.Infof("Using 'raw' context retrieval for session %s", clientReq.SessionID)
		textContext, err := s.sessionManager.GetTextSessionContext(clientReq.SessionID, rawHistoryLength)
		if err != nil {
			log.Errorf("Failed to get raw session context for %s: %v", clientReq.SessionID, err)
			http.Error(w, "Failed to retrieve session context", http.StatusInternalServerError)
			return
		}
		prompt := textContext + "<|im_start|>user\n" + clientReq.Prompt + "<|im_end|>\n"
		llamaReq["prompt"] = prompt
		log.Debugf("Prepared raw prompt for session %s", clientReq.SessionID)

	} else if clientReq.Mode == "tokenized" {
		log.Infof("Using 'tokenized' context retrieval for session %s", clientReq.SessionID)
		tokenizedContext, err := s.redisContextStorage.GetTokenizedSessionContext(clientReq.SessionID)
		if err != nil {
			// Log error but proceed, context might not exist yet
			log.Warnf("Failed to get tokenized session context for %s (proceeding without): %v", clientReq.SessionID, err)
		} else if tokenizedContext != nil {
			log.Debugf("Retrieved tokenized context for session %s", clientReq.SessionID)
		} else {
			log.Infof("No existing tokenized context found for session %s, proceeding without.", clientReq.SessionID)
		}

		llamaReq["prompt"] = clientReq.Prompt
		// Add the retrieved tokenized context if available
		if tokenizedContext != nil {
			llamaReq["context"] = tokenizedContext // This key is added internally, not accepted from client
			log.Debugf("Added tokenized context to Llama request for session %s", clientReq.SessionID)
		}
	} else {
		log.Warnf("Invalid mode '%s' requested for session %s", clientReq.Mode, clientReq.SessionID)
		http.Error(w, fmt.Sprintf("Invalid mode: %s. Use 'raw' or 'tokenized'", clientReq.Mode), http.StatusBadRequest)
		return
	}

	// --- Call LlamaClient ---
	log.Infof("Sending completion request to Llama service for session %s", clientReq.SessionID)
	resp, err := s.llamaService.Completion(llamaReq)
	if err != nil {
		log.Errorf("Llama completion error for session %s: %v", clientReq.SessionID, err)
		http.Error(w, "Error processing completion request", http.StatusInternalServerError)
		return
	}
	log.Infof("Received completion response from Llama service for session %s", clientReq.SessionID)

	// --- Add session_id and user_id to the response ---
	// Ensure resp is not nil before adding the session ID
	if resp == nil {
		log.Warnf("Llama service returned nil response for session %s, initializing empty map.", clientReq.SessionID)
		resp = make(map[string]interface{}) // Initialize if nil
	}
	resp["session_id"] = clientReq.SessionID // Add session_id (original or generated)
	resp["user_id"] = effectiveUserID        // Add the effective user_id used/provided
	log.Debugf("Added session_id %s and user_id %s to response map", clientReq.SessionID, effectiveUserID)

	// --- Send response ---
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Error already sent potentially, or response started. Log only.
		log.Errorf("Failed to write response for session %s: %v", clientReq.SessionID, err)
	} else {
		log.Infof("Successfully sent completion response for session %s", clientReq.SessionID)
	}
}

// Start runs the HTTP server.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/completion", s.handleCompletion)
	log.Infof("Starting server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
