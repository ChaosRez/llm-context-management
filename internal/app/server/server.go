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
	"time"
)

const rawHistoryLength = 20
const sessionDurationDays = 1
const defaultUserID = "default_user" // Default user ID if none provided

// Server holds dependencies for the HTTP server.
type Server struct {
	llamaService   *Llama.LlamaClient
	sessionManager *SessionManager.SQLiteSessionManager // NOTE Assuming SQLite
	contextStorage ContextStorage.ContextStorage
}

// NewServer creates a new Server instance.
func NewServer(
	llama *Llama.LlamaClient,
	sm *SessionManager.SQLiteSessionManager,
	cs ContextStorage.ContextStorage,
) *Server {
	return &Server{
		llamaService:   llama,
		sessionManager: sm,
		contextStorage: cs,
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
	handleStartTime := time.Now()
	defer func() {
		log.Infof("handleCompletion for session %s took %s", r.Header.Get("X-Session-ID"), time.Since(handleStartTime)) // X-Session-ID will be set later if new
	}()

	if r.Method != http.MethodPost {
		log.Warnf("Invalid method %s received from %s", r.Method, r.RemoteAddr)
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var clientReq CompletionRequest
	decodeStartTime := time.Now()
	if err := json.NewDecoder(r.Body).Decode(&clientReq); err != nil {
		log.Errorf("Failed to decode request body from %s: %v (took %s)", r.RemoteAddr, err, time.Since(decodeStartTime))
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	log.Debugf("Request body decoding took %s", time.Since(decodeStartTime))
	defer r.Body.Close()

	// Set X-Session-ID header for deferred log once clientReq.SessionID is determined
	// This is a bit of a workaround as SessionID isn't known at the very start of the defer.
	// A more robust way might involve a custom logger or passing sessionID to the defer.
	// For now, we'll update a header that the defer can read.
	// This is imperfect if session creation fails before this point.
	r.Header.Set("X-Session-ID", clientReq.SessionID) // Initial set, might be updated

	log.Infof(">> Received completion request from %s '%s'<<", r.RemoteAddr, clientReq.Prompt)
	log.Debugf("Decoded request: Mode=%s, SessionID=%s, UserID=%s, Model=%s", clientReq.Mode, clientReq.SessionID, clientReq.UserID, clientReq.Model)

	effectiveUserID := clientReq.UserID
	if effectiveUserID == "" {
		effectiveUserID = defaultUserID
		log.Warnf("No UserID provided in request, using default: %s", effectiveUserID)
	}

	if clientReq.SessionID == "" {
		log.Infof("No session_id provided, creating a new session for user '%s'.", effectiveUserID)
		createSessStartTime := time.Now()
		sessionID, err := s.sessionManager.CreateSession(effectiveUserID, sessionDurationDays)
		log.Debugf("s.sessionManager.CreateSession for user '%s' took %s", effectiveUserID, time.Since(createSessStartTime))
		if err != nil {
			log.Errorf("Failed to create session for user '%s': %v", effectiveUserID, err)
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
		clientReq.SessionID = sessionID
		r.Header.Set("X-Session-ID", clientReq.SessionID) // Update for defer log
		log.Infof("Created new session ID: %s for user %s", clientReq.SessionID, effectiveUserID)
	} else {
		// TODO: validate if the provided sessionID belongs to the effectiveUserID.
		r.Header.Set("X-Session-ID", clientReq.SessionID) // Ensure it's set for defer log
		log.Infof("Using existing session ID: %s (Effective UserID: %s)", clientReq.SessionID, effectiveUserID)
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
	var finalPrompt string // Store the final prompt sent to Llama for logging/history

	if clientReq.Mode == "raw" {
		log.Infof("Using 'raw' context retrieval for session %s", clientReq.SessionID)
		getTextCtxStartTime := time.Now()
		textContext, errCtx := s.sessionManager.GetTextSessionContext(clientReq.SessionID, rawHistoryLength)
		log.Debugf("s.sessionManager.GetTextSessionContext for session %s took %s", clientReq.SessionID, time.Since(getTextCtxStartTime))
		if errCtx != nil {
			log.Errorf("Failed to get raw session context for %s: %v", clientReq.SessionID, errCtx)
			http.Error(w, "Failed to retrieve session context", http.StatusInternalServerError)
			return
		}
		// Construct the prompt including context and user message for Llama.cpp
		finalPrompt = textContext + "<|im_start|>user\n" + clientReq.Prompt + "<|im_end|>\n"
		llamaReq["prompt"] = finalPrompt
		log.Debugf("Prepared raw prompt for session %s", clientReq.SessionID)

	} else if clientReq.Mode == "tokenized" {
		log.Infof("Using 'tokenized' context retrieval for session %s", clientReq.SessionID)
		getTokenCtxStartTime := time.Now()
		tokenizedContext, errCtx := s.contextStorage.GetTokenizedSessionContext(clientReq.SessionID)
		log.Debugf("s.contextStorage.GetTokenizedSessionContext for session %s took %s", clientReq.SessionID, time.Since(getTokenCtxStartTime))
		if errCtx != nil { // Note: GetTokenizedSessionContext already logs details internally
			log.Warnf("Failed to get tokenized session context for %s (proceeding without, error: %v)", clientReq.SessionID, errCtx)
		} else if tokenizedContext != nil {
			log.Infof("Retrieved tokenized context (length %d) for session %s", len(tokenizedContext), clientReq.SessionID)
		} else {
			log.Infof("No existing tokenized context found for session %s, proceeding without.", clientReq.SessionID)
		}

		finalPrompt = clientReq.Prompt
		llamaReq["prompt"] = finalPrompt
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
	llamaCallStartTime := time.Now()
	resp, err := s.llamaService.Completion(llamaReq) // llamaService.Completion has internal timing
	log.Debugf("s.llamaService.Completion call for session %s took %s (overall)", clientReq.SessionID, time.Since(llamaCallStartTime))
	if err != nil {
		log.Errorf("Llama completion error for session %s: %v", clientReq.SessionID, err)
		http.Error(w, "Error processing completion request", http.StatusInternalServerError)
		return
	}
	log.Infof("Received completion response from Llama service for session %s", clientReq.SessionID)

	addUserMsgStartTime := time.Now()
	// --- Add user message to session history ---
	// Note: We add the *original* user prompt (clientReq.Prompt), not the potentially context-prepended one (finalPrompt)
	_, err = s.sessionManager.AddMessage(clientReq.SessionID, "user", clientReq.Prompt, nil, &clientReq.Model)
	log.Debugf("s.sessionManager.AddMessage (user) for session %s took %s", clientReq.SessionID, time.Since(addUserMsgStartTime))
	if err != nil {
		// NOTE Log error but continue processing the response
		log.Errorf("Failed to add user message for session %s: %v", clientReq.SessionID, err)
	} else {
		log.Debugf("Added user message to session %s", clientReq.SessionID)
	}

	// --- Process and add assistant response to session history ---
	assistantMsg := "" // Initialize empty
	if resp != nil {
		if content, ok := resp["content"].(string); ok {
			assistantMsg = content
			addAssistantMsgStartTime := time.Now()
			_, err = s.sessionManager.AddMessage(clientReq.SessionID, "assistant", assistantMsg, nil, &clientReq.Model)
			log.Debugf("s.sessionManager.AddMessage (assistant) for session %s took %s", clientReq.SessionID, time.Since(addAssistantMsgStartTime))
			if err != nil {
				// NOTE Log error but continue processing the response
				log.Errorf("Failed to add assistant message for session %s: %v", clientReq.SessionID, err)
			} else {
				log.Debugf("Added assistant message to session %s", clientReq.SessionID)
			}
		} else {
			log.Warnf("Llama response for session %s did not contain a string 'content' field.", clientReq.SessionID)
		}
	} else {
		log.Warnf("Llama service returned nil response map for session %s.", clientReq.SessionID)
		resp = make(map[string]interface{}) // Initialize if nil to avoid nil pointer below
	}

	// --- Update tokenized context in Redis (regardless of mode, as it uses DB history) ---
	// This should happen *after* both user and assistant messages are added to the DB.
	updateCtxStartTime := time.Now()
	//FIXME err = s.contextStorage.UpdateSessionContext(clientReq.SessionID, s.sessionManager, s.llamaService)
	// UpdateSessionContext has internal detailed timing, log overall here
	log.Debugf("s.contextStorage.UpdateSessionContext (overall) for session %s took %s", clientReq.SessionID, time.Since(updateCtxStartTime))
	if err != nil {
		// Log warning, similar to scenario mode, don't fail the request
		log.Errorf("Failed to update tokenized session context for session %s: %v", clientReq.SessionID, err)
	} else {
		log.Infof("Updated tokenized context for session %s", clientReq.SessionID)
	}

	// --- Add session_id, user_id, and mode to the response ---
	resp["session_id"] = clientReq.SessionID // Add session_id (original or generated)
	resp["user_id"] = effectiveUserID        // Add the effective user_id used/provided
	resp["mode"] = clientReq.Mode            // Add the mode used for the request
	log.Debugf("Added session_id %s, user_id %s, and mode %s to response map", clientReq.SessionID, effectiveUserID, clientReq.Mode)

	// --- Send response ---
	w.Header().Set("Content-Type", "application/json")
	encodeStartTime := time.Now()
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Error already sent potentially, or response started. Log only.
		log.Errorf("Failed to write response for session %s: %v (took %s)", clientReq.SessionID, err, time.Since(encodeStartTime))
	} else {
		log.Infof("Successfully sent completion response for session %s (encoding took %s)", clientReq.SessionID, time.Since(encodeStartTime))
	}
}

// Start runs the HTTP server.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/completion", s.handleCompletion)
	// TODO: Add handlers for session management (list, delete)
	log.Infof("Starting server on %s", addr)
	return http.ListenAndServe(addr, mux)
}
