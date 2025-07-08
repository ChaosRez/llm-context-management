package server

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// const rawHistoryLength = 100
const sessionDurationDays = 1
const defaultUserID = "default_user" // Default user ID if none provided
const maxTurnRetries = 5
const turnRetryDelay = 10 * time.Millisecond

// Server holds dependencies for the HTTP server.
type Server struct {
	llamaService   *Llama.LlamaClient
	sessionManager *SessionManager.SQLiteSessionManager // NOTE Assuming SQLite
	contextStorage ContextStorage.ContextStorage
	sessionLocks   map[string]*sync.Mutex
	locksMutex     sync.RWMutex
	csvWriter      *csv.Writer
	csvFile        *os.File
}

// NewServer creates a new Server instance.
func NewServer(
	llama *Llama.LlamaClient,
	sm *SessionManager.SQLiteSessionManager,
	cs ContextStorage.ContextStorage,
) *Server {
	s := &Server{
		llamaService:   llama,
		sessionManager: sm,
		contextStorage: cs,
		sessionLocks:   make(map[string]*sync.Mutex),
	}

	// Initialize CSV logger
	logDir := "testdata/log/"
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		log.Fatalf("Failed to create log directory %s: %v", logDir, err)
	}
	csvFilename := filepath.Join(logDir, fmt.Sprintf("%s_server.csv", time.Now().Format("20060102_150405")))
	csvFile, err := os.Create(csvFilename)
	if err != nil {
		log.Fatalf("Failed to create server CSV log file %s: %v", csvFilename, err)
	}
	s.csvFile = csvFile // Store file to close it later

	s.csvWriter = csv.NewWriter(csvFile)
	headers := []string{"Timestamp", "Operation", "DurationMs", "ContextMethod", "ScenarioName", "SessionID", "RequestSizeBytes", "PromptChars", "ContextTokens", "Turn", "Retries", "Details"}
	if err := s.csvWriter.Write(headers); err != nil {
		log.Fatalf("Failed to write CSV header to %s: %v", csvFilename, err)
	}
	s.csvWriter.Flush()
	log.Infof("Logging server operations to %s", csvFilename)

	return s
}

// CompletionRequest defines the expected structure of the incoming JSON request.
type CompletionRequest struct {
	Mode        string                 `json:"mode"` // "raw", "tokenized", or "client-side"
	SessionID   string                 `json:"session_id,omitempty"`
	UserID      string                 `json:"user_id,omitempty"` // UserID field
	Turn        int                    `json:"turn"`              // Client-side turn counter, must be >= 1
	Prompt      string                 `json:"prompt"`
	Model       string                 `json:"model"`
	Temperature float64                `json:"temperature"`
	Seed        int                    `json:"seed"`
	Stream      bool                   `json:"stream"`
	OtherParams map[string]interface{} `json:"-"` // Catches other params for forwarding
	Retries     int                    `json:"-"` // Internal field to track retries
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
	delete(allFields, "turn")
	delete(allFields, "prompt")
	delete(allFields, "model")
	delete(allFields, "temperature")
	delete(allFields, "seed")
	delete(allFields, "stream")

	// Store remaining fields as OtherParams
	cr.OtherParams = allFields
	return nil
}

// writeOperationToCsv writes a record to the server's CSV log.
func (s *Server) writeOperationToCsv(opActualStartTime time.Time, operationName string, duration time.Duration, contextMethod string, scenarioName string, sessionID string, requestSize int, promptChars int, contextTokens int, turn int, retries int, details string) {
	if s.csvWriter == nil {
		log.Warnf("CSV writer not initialized when trying to log operation: %s", operationName)
		return
	}
	record := []string{
		opActualStartTime.Format("2006-01-02T15:04:05.000Z07:00"), // ISO8601 like timestamp for operation start
		operationName,
		strconv.FormatInt(duration.Milliseconds(), 10),
		contextMethod,
		scenarioName,
		sessionID,
		strconv.Itoa(requestSize),
		strconv.Itoa(promptChars),
		strconv.Itoa(contextTokens),
		strconv.Itoa(turn),
		strconv.Itoa(retries),
		details,
	}
	if err := s.csvWriter.Write(record); err != nil {
		log.Errorf("Failed to write record to CSV for operation %s: %v", operationName, err)
	}
	s.csvWriter.Flush() // Flush after each write to ensure data is saved
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

	// Log the size of the incoming request body.
	requestSize := r.ContentLength
	log.Infof("Received request from %s with content length: %d bytes", r.RemoteAddr, requestSize)

	var clientReq CompletionRequest
	decodeStartTime := time.Now()
	if err := json.NewDecoder(r.Body).Decode(&clientReq); err != nil {
		log.Errorf("Failed to decode request body from %s: %v (took %s)", r.RemoteAddr, err, time.Since(decodeStartTime))
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	log.Debugf("Request body decoding took %s", time.Since(decodeStartTime))
	defer r.Body.Close()

	// Log network overhead to CSV
	// We log this early, some fields like SessionID might be empty if not provided.
	s.writeOperationToCsv(
		handleStartTime,
		"Network.Request.Size",
		-1, // Duration is not applicable here
		clientReq.Mode,
		"ServerMode",
		clientReq.SessionID,
		int(requestSize),
		len(clientReq.Prompt),
		-1, // Context tokens are not known yet
		clientReq.Turn,
		-1, // Retries not applicable here
		"", // Details are now in separate columns
	)

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
		createSessDuration := time.Since(createSessStartTime)
		log.Debugf("s.sessionManager.CreateSession for user '%s' took %s", effectiveUserID, createSessDuration)
		if err != nil {
			log.Errorf("Failed to create session for user '%s': %v", effectiveUserID, err)
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
		clientReq.SessionID = sessionID
		s.writeOperationToCsv(createSessStartTime, "sessionManager.CreateSession", createSessDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, -1, -1, -1, fmt.Sprintf("UserID: %s", effectiveUserID))
		r.Header.Set("X-Session-ID", clientReq.SessionID) // Update for defer log
		log.Infof("Created new session ID: %s for user %s", clientReq.SessionID, effectiveUserID)
	} else {
		// TODO: validate if the provided sessionID belongs to the effectiveUserID.
		r.Header.Set("X-Session-ID", clientReq.SessionID) // Ensure it's set for defer log
		log.Infof("Using existing session ID: %s (Effective UserID: %s)", clientReq.SessionID, effectiveUserID)
	}

	// --- Session Locking for data consistency ---
	// Get or create a lock for the session to ensure sequential processing.
	s.locksMutex.RLock()
	sessionLock, ok := s.sessionLocks[clientReq.SessionID]
	s.locksMutex.RUnlock()

	if !ok {
		s.locksMutex.Lock()
		// Double-check in case another goroutine created it while we were waiting for the write lock.
		if _, ok := s.sessionLocks[clientReq.SessionID]; !ok {
			s.sessionLocks[clientReq.SessionID] = &sync.Mutex{}
			log.Debugf("Created new mutex for session %s", clientReq.SessionID)
		}
		sessionLock = s.sessionLocks[clientReq.SessionID]
		s.locksMutex.Unlock()
	}

	log.Debugf("Acquiring lock for session %s", clientReq.SessionID)
	lockAcquireStartTime := time.Now()
	sessionLock.Lock() // Block until the previous operation on this session is complete.
	log.Infof("Lock acquired for session %s (waited %s)", clientReq.SessionID, time.Since(lockAcquireStartTime))
	// The lock will be released in the async update goroutine.

	// Validate turn number
	if clientReq.Turn < 1 {
		log.Errorf("Invalid turn number for session %s. Client turn: %d", clientReq.SessionID, clientReq.Turn)
		http.Error(w, "Invalid turn number. Must be >= 1.", http.StatusBadRequest)
		sessionLock.Unlock()
		log.Infof("Lock released for session %s due to invalid turn", clientReq.SessionID)
		return
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
	var tokenizedContext []int
	var rawMessages []ContextStorage.RawMessage

	if clientReq.Mode == "raw" {
		log.Infof("Using 'raw' context retrieval for session %s", clientReq.SessionID)
		var errCtx error
		var currentTurn int
		var getRawCtxDuration time.Duration
		var getRawCtxStartTime time.Time

		// Turn validation with retry logic
		for i := 0; i <= maxTurnRetries; i++ {
			clientReq.Retries = i
			getRawCtxStartTime = time.Now()
			rawMessages, currentTurn, errCtx = s.contextStorage.GetRawSessionContext(clientReq.SessionID)
			getRawCtxDuration = time.Since(getRawCtxStartTime)
			log.Debugf("s.contextStorage.GetRawSessionContext for session %s took %s (attempt %d)", clientReq.SessionID, getRawCtxDuration, i)

			if errCtx != nil {
				if !s.contextStorage.IsNotFoundError(errCtx) {
					log.Warnf("Failed to get raw session context for %s (proceeding without): %v", clientReq.SessionID, errCtx)
				} else {
					log.Infof("No existing raw context found for session %s, starting fresh.", clientReq.SessionID)
				}
				rawMessages = []ContextStorage.RawMessage{} // Initialize to empty if error or not found
				currentTurn = 0                             // For a new session, turn is 0
			} else if rawMessages != nil {
				log.Infof("Retrieved raw context (message count %d, turn %d) for session %s", len(rawMessages), currentTurn, clientReq.SessionID)
			} else {
				log.Infof("No existing raw context found for session %s, starting fresh.", clientReq.SessionID)
				rawMessages = []ContextStorage.RawMessage{} // Initialize to empty if nil
				currentTurn = 0                             // For a new session, turn is 0
			}

			if clientReq.Turn == currentTurn+1 {
				log.Infof("Turn validation successful for session %s on attempt %d. Client turn: %d, Server turn: %d", clientReq.SessionID, i, clientReq.Turn, currentTurn)
				break // Correct turn, exit loop
			}

			log.Warnf("Turn mismatch for session %s on attempt %d. Client turn: %d, Server turn: %d. Retrying...", clientReq.SessionID, i, clientReq.Turn, currentTurn)

			if i == maxTurnRetries {
				log.Errorf("Turn mismatch for session %s after %d retries. Client turn: %d, Server turn: %d", clientReq.SessionID, maxTurnRetries, clientReq.Turn, currentTurn)
				s.writeOperationToCsv(getRawCtxStartTime, "contextStorage.GetRawSessionContext", getRawCtxDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(rawMessages), currentTurn, clientReq.Retries, "Final attempt failed turn validation")
				http.Error(w, fmt.Sprintf("Turn mismatch after retries. Expected turn %d, but got %d.", currentTurn+1, clientReq.Turn), http.StatusConflict)
				sessionLock.Unlock()
				log.Infof("Lock released for session %s due to turn mismatch after retries", clientReq.SessionID)
				return
			}
			time.Sleep(turnRetryDelay)
		}
		s.writeOperationToCsv(getRawCtxStartTime, "contextStorage.GetRawSessionContext", getRawCtxDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(rawMessages), currentTurn, clientReq.Retries, "")

		// Construct the prompt including context and user message for Llama.cpp
		var textContextBuilder strings.Builder
		for _, msg := range rawMessages {
			textContextBuilder.WriteString(fmt.Sprintf("<|im_start|>%s\n%s<|im_end|>\n", msg.Role, msg.Content))
		}
		finalPrompt = textContextBuilder.String() + "<|im_start|>user\n" + clientReq.Prompt + "<|im_end|>\n"
		llamaReq["prompt"] = finalPrompt
		log.Debugf("Prepared raw prompt for session %s", clientReq.SessionID)

	} else if clientReq.Mode == "tokenized" {
		log.Infof("Using 'tokenized' context retrieval for session %s", clientReq.SessionID)
		var errCtx error
		var currentTurn int
		var getTokenCtxDuration time.Duration
		var getTokenCtxStartTime time.Time

		// Turn validation with retry logic
		for i := 0; i <= maxTurnRetries; i++ {
			clientReq.Retries = i
			getTokenCtxStartTime = time.Now()
			tokenizedContext, currentTurn, errCtx = s.contextStorage.GetTokenizedSessionContext(clientReq.SessionID)
			getTokenCtxDuration = time.Since(getTokenCtxStartTime)
			log.Debugf("s.contextStorage.GetTokenizedSessionContext for session %s took %s (attempt %d)", clientReq.SessionID, getTokenCtxDuration, i)

			if errCtx != nil {
				if !s.contextStorage.IsNotFoundError(errCtx) {
					log.Warnf("Failed to get tokenized session context for %s (proceeding without): %v", clientReq.SessionID, errCtx)
				} else {
					log.Infof("No existing tokenized context found for session %s, starting fresh.", clientReq.SessionID)
				}
				tokenizedContext = []int{} // Initialize to empty if error or not found
				currentTurn = 0            // For a new session, turn is 0
			} else if tokenizedContext != nil {
				log.Infof("Retrieved tokenized context (length %d, turn %d) for session %s", len(tokenizedContext), currentTurn, clientReq.SessionID)
			} else {
				log.Infof("No existing tokenized context found for session %s, starting fresh.", clientReq.SessionID)
				tokenizedContext = []int{} // Initialize to empty if nil
				currentTurn = 0            // For a new session, turn is 0
			}

			if clientReq.Turn == currentTurn+1 {
				log.Infof("Turn validation successful for session %s on attempt %d. Client turn: %d, Server turn: %d", clientReq.SessionID, i, clientReq.Turn, currentTurn)
				break // Correct turn, exit loop
			}

			log.Warnf("Turn mismatch for session %s on attempt %d. Client turn: %d, Server turn: %d. Retrying...", clientReq.SessionID, i, clientReq.Turn, currentTurn)

			if i == maxTurnRetries {
				log.Errorf("Turn mismatch for session %s after %d retries. Client turn: %d, Server turn: %d", clientReq.SessionID, maxTurnRetries, clientReq.Turn, currentTurn)
				s.writeOperationToCsv(getTokenCtxStartTime, "contextStorage.GetTokenizedSessionContext", getTokenCtxDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(tokenizedContext), currentTurn, clientReq.Retries, "Final attempt failed turn validation")
				http.Error(w, fmt.Sprintf("Turn mismatch after retries. Expected turn %d, but got %d.", currentTurn+1, clientReq.Turn), http.StatusConflict)
				sessionLock.Unlock()
				log.Infof("Lock released for session %s due to turn mismatch after retries", clientReq.SessionID)
				return
			}
			time.Sleep(turnRetryDelay)
		}
		s.writeOperationToCsv(getTokenCtxStartTime, "contextStorage.GetTokenizedSessionContext", getTokenCtxDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(tokenizedContext), currentTurn, clientReq.Retries, "")

		finalPrompt = clientReq.Prompt // the template is added by LLama.cpp internally
		llamaReq["prompt"] = finalPrompt
		// Add the retrieved tokenized context if available
		if len(tokenizedContext) > 0 {
			llamaReq["context"] = tokenizedContext // This key is added internally, not accepted from client
			log.Debugf("Added tokenized context to Llama request for session %s", clientReq.SessionID)
		}
	} else if clientReq.Mode == "client-side" {
		log.Infof("Using 'client-side' mode for session %s, forwarding request.", clientReq.SessionID)
		finalPrompt = clientReq.Prompt
		llamaReq["prompt"] = finalPrompt
		// No context management, just forward.
		// The lock will be released immediately after the call.
	} else {
		log.Warnf("Invalid mode '%s' requested for session %s", clientReq.Mode, clientReq.SessionID)
		http.Error(w, fmt.Sprintf("Invalid mode: %s. Use 'raw', 'tokenized', or 'client-side'", clientReq.Mode), http.StatusBadRequest)
		sessionLock.Unlock()
		log.Infof("Lock released for session %s due to invalid mode", clientReq.SessionID)
		return
	}

	// --- Call LlamaClient ---
	log.Infof("Sending completion request to Llama service for session %s", clientReq.SessionID)
	llamaCallStartTime := time.Now()
	resp, err := s.llamaService.Completion(llamaReq) // llamaService.Completion has internal timing
	llamaCallDuration := time.Since(llamaCallStartTime)
	log.Debugf("s.llamaService.Completion call for session %s took %s (overall)", clientReq.SessionID, llamaCallDuration)
	s.writeOperationToCsv(llamaCallStartTime, "llamaService.Completion", llamaCallDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, len(finalPrompt), len(tokenizedContext), clientReq.Turn, clientReq.Retries, "")
	if err != nil {
		log.Errorf("Llama completion error for session %s: %v", clientReq.SessionID, err)
		http.Error(w, "Error processing completion request", http.StatusInternalServerError)
		sessionLock.Unlock()
		log.Warnf("Lock released for session %s due to llama completion error", clientReq.SessionID)
		return
	}
	log.Infof("Received completion response from Llama service for session %s", clientReq.SessionID)

	// --- Process response ---
	assistantMsg := ""
	if resp != nil {
		if content, ok := resp["content"].(string); ok {
			assistantMsg = content
		} else {
			log.Warnf("Llama response for session %s did not contain a string 'content' field.", clientReq.SessionID)
		}
	} else {
		log.Warnf("Llama service returned nil response map for session %s.", clientReq.SessionID)
		resp = make(map[string]interface{}) // Initialize if nil to avoid nil pointer below
	}

	// --- Asynchronously update history and context ---
	// This is done in a goroutine to avoid making the client wait.
	// The lock for the session is passed to the goroutine and released there.
	if clientReq.Mode == "client-side" {
		sessionLock.Unlock()
		log.Infof("Lock released for session %s (client-side mode)", clientReq.SessionID)
	} else {
		go s.updateHistoryAndContextAsync(clientReq, assistantMsg, tokenizedContext, rawMessages, sessionLock)
	}

	// --- Add session_id, user_id, and mode to the response ---
	resp["session_id"] = clientReq.SessionID // Add session_id (original or generated)
	resp["user_id"] = effectiveUserID        // Add the effective user_id used/provided
	resp["mode"] = clientReq.Mode            // Add the mode used for the request
	resp["request_size"] = requestSize
	if clientReq.Retries > 0 {
		resp["retries"] = clientReq.Retries
		log.Infof("Completion for session %s required %d retries for turn consistency.", clientReq.SessionID, clientReq.Retries)
	}
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

// updateHistoryAndContextAsync handles the saving of conversation history and context
// in the background to avoid blocking the client response.
func (s *Server) updateHistoryAndContextAsync(
	clientReq CompletionRequest,
	assistantMsg string,
	initialTokenizedContext []int,
	initialRawMessages []ContextStorage.RawMessage,
	sessionLock *sync.Mutex,
) {
	// Recover from potential panics in the goroutine to prevent server crash
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Recovered in updateHistoryAndContextAsync for session %s: %v", clientReq.SessionID, r)
		}
		sessionLock.Unlock()
		log.Infof("Lock released for session %s", clientReq.SessionID)
	}()

	if clientReq.Mode == "client-side" {
		// No history/context update needed for client-side mode.
		return
	}

	log.Infof("Starting async history/context update for session %s", clientReq.SessionID)

	if clientReq.Mode == "raw" {
		// --- Construct new message history ---
		if initialRawMessages == nil {
			initialRawMessages = []ContextStorage.RawMessage{}
		}
		newHistory := append(initialRawMessages, ContextStorage.RawMessage{Role: "user", Content: clientReq.Prompt})
		if assistantMsg != "" {
			newHistory = append(newHistory, ContextStorage.RawMessage{Role: "assistant", Content: assistantMsg})
		}

		// --- Update raw context in FReD ---
		updateCtxOpStartTime := time.Now()
		errUpdateCtx := s.contextStorage.UpdateRawSessionContext(clientReq.SessionID, newHistory, clientReq.Turn)
		updateCtxOpDuration := time.Since(updateCtxOpStartTime)
		log.Debugf("s.contextStorage.UpdateRawSessionContext for session %s took %s", clientReq.SessionID, updateCtxOpDuration)
		s.writeOperationToCsv(updateCtxOpStartTime, "contextStorage.UpdateRawSessionContext", updateCtxOpDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(newHistory), clientReq.Turn, clientReq.Retries, "")

		if errUpdateCtx != nil {
			log.Errorf("Failed to update raw session context for session %s: %v", clientReq.SessionID, errUpdateCtx)
		} else {
			log.Infof("Updated raw context for session %s, new total messages: %d, new turn: %d", clientReq.SessionID, len(newHistory), clientReq.Turn)
		}

		// --- Increment turn in SQLite ---
		incrementTurnStartTime := time.Now()
		if err := s.sessionManager.IncrementSessionTurn(clientReq.SessionID); err != nil {
			log.Errorf("Failed to increment turn for session %s: %v", clientReq.SessionID, err)
		} else {
			incrementTurnDuration := time.Since(incrementTurnStartTime)
			log.Infof("Incremented turn for session %s to %d", clientReq.SessionID, clientReq.Turn)
			s.writeOperationToCsv(incrementTurnStartTime, "sessionManager.IncrementSessionTurn", incrementTurnDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, -1, clientReq.Turn-1, -1, "")
		}
	} else if clientReq.Mode == "tokenized" {
		if assistantMsg == "" {
			log.Warnf("No assistant message to process for tokenized context update in session %s.", clientReq.SessionID)
			return
		}

		newUserInteractionText := fmt.Sprintf("<|im_start|>user\n%s<|im_end|>\n<|im_start|>assistant\n%s<|im_end|>\n", clientReq.Prompt, assistantMsg)

		tokenizeNewOpStartTime := time.Now()
		newInteractionTokens, errTokenize := s.llamaService.Tokenize(newUserInteractionText)
		tokenizeNewOpDuration := time.Since(tokenizeNewOpStartTime)
		log.Debugf("s.llamaService.Tokenize (new interaction) for session %s took %s", clientReq.SessionID, tokenizeNewOpDuration)
		s.writeOperationToCsv(tokenizeNewOpStartTime, "llamaService.Tokenize", tokenizeNewOpDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, len(newUserInteractionText), -1, clientReq.Turn, clientReq.Retries, "New interaction")

		if errTokenize != nil {
			log.Errorf("Failed to tokenize new interaction for session %s: %v", clientReq.SessionID, errTokenize)
			return // Cannot proceed without tokens
		}

		if initialTokenizedContext == nil {
			initialTokenizedContext = []int{}
		}
		updatedFullTokenizedContext := append(initialTokenizedContext, newInteractionTokens...)

		updateCtxOpStartTime := time.Now()
		errUpdateCtx := s.contextStorage.UpdateSessionContext(clientReq.SessionID, updatedFullTokenizedContext, clientReq.Turn)
		updateCtxOpDuration := time.Since(updateCtxOpStartTime)
		log.Debugf("s.contextStorage.UpdateSessionContext for session %s took %s", clientReq.SessionID, updateCtxOpDuration)
		s.writeOperationToCsv(updateCtxOpStartTime, "contextStorage.UpdateSessionContext", updateCtxOpDuration, clientReq.Mode, "ServerMode", clientReq.SessionID, -1, -1, len(updatedFullTokenizedContext), clientReq.Turn, clientReq.Retries, "")

		if errUpdateCtx != nil {
			log.Errorf("Failed to update tokenized session context for session %s: %v", clientReq.SessionID, errUpdateCtx)
		} else {
			log.Infof("Updated tokenized context for session %s, new total length: %d, new turn: %d", clientReq.SessionID, len(updatedFullTokenizedContext), clientReq.Turn)
		}
	}
}

// Start registers the HTTP handlers and starts the server.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/completion", s.handleCompletion)
	// TODO: Add handlers for session management (list, delete)
	log.Infof("Starting server on %s", addr)

	return http.ListenAndServe(addr, mux)
}

// Stop gracefully shuts down the server (defered from main), closing resources like the CSV logger.
func (s *Server) Stop() {
	log.Infof("Stopping server...")
	if s.csvFile != nil {
		log.Infof("Flushing and closing CSV log file: %s", s.csvFile.Name())
		s.csvWriter.Flush()
		s.csvFile.Close()
	}
}
