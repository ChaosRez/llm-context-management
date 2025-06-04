package context_storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
	fredClient "llm-context-management/internal/pkg/fredclient"

	SessionManager "llm-context-management/internal/app/session_manager"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

const (
	// DefaultKeygroup is the FReD keygroup where session contexts will be stored.
	// This could be made configurable.
	defaultFredKeygroup      = "llm_session_contexts"
	maxMessagesForFredUpdate = 10000 // Similar to Redis implementation
	defaultFredReadTimeout   = 500   // Default timeout for FReD read operations in ms
	expiry                   = 0     // 0 = no expiry time for FReD keygroup upon creation
	mutable                  = true
)

// ErrFredNotFound is returned when a key is not found in FReD.
var ErrFredNotFound = fmt.Errorf("key not found in FReD")

// FReDContextStorage implements the ContextStorage interface using FReD.
type FReDContextStorage struct {
	client   *fredClient.AlexandraClient
	keygroup string
}

// NewFReDContextStorage creates a new FReDContextStorage.
// addr is the FReD service address (e.g., "127.0.0.1:10000").
// keygroup is the FReD keygroup to use. If empty, defaultFredKeygroup is used.
// createKeygroupIfNotExist will attempt to create the keygroup on a specified node if it doesn't exist.
// bootstrapNode is the node used to create the keygroup (e.g., "nodeA"). Required if createKeygroupIfNotExist is true.
func NewFReDContextStorage(addr string, keygroup string, createKeygroupIfNotExist bool, bootstrapNode string) (*FReDContextStorage, error) {
	// Define relative paths for certificates and keys
	certDir := "fred/cert/"
	clientCertPath := filepath.Join(certDir, "frededge1.crt") // FIXME: diffrent certs for different nodes
	clientKeyPath := filepath.Join(certDir, "frededge1.key")
	caCertPath := filepath.Join(certDir, "ca.crt")

	// Pass the certificate paths to NewAlexandraClient
	c := fredClient.NewAlexandraClient(addr, clientCertPath, clientKeyPath, caCertPath)

	storageKeygroup := keygroup
	if storageKeygroup == "" {
		storageKeygroup = defaultFredKeygroup
	}

	if createKeygroupIfNotExist {
		if bootstrapNode == "" {
			return nil, fmt.Errorf("bootstrapNode is required when createKeygroupIfNotExist is true")
		}
		log.Infof("FReD: Attempting to create keygroup '%s' on node '%s'", storageKeygroup, bootstrapNode)
		c.CreateKeygroup(bootstrapNode, storageKeygroup, mutable, expiry, true)
		log.Infof("FReD: Keygroup '%s' creation initiated (or already exists).", storageKeygroup)
	}

	return &FReDContextStorage{
		client:   &c,
		keygroup: storageKeygroup,
	}, nil
}

// GetTokenizedSessionContext retrieves the tokenized session context from FReD.
func (f *FReDContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("FReD: GetTokenizedSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Attempting to retrieve tokenized context for session ID: %s from keygroup: %s", sessionID, f.keygroup)

	fredReadStartTime := time.Now()
	readData := f.client.Read(f.keygroup, sessionID, defaultFredReadTimeout)
	log.Debugf("FReD: Read for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredReadStartTime))

	if len(readData) == 0 {
		log.Warnf("FReD: Cache miss for session ID: %s in keygroup: %s. No data returned.", sessionID, f.keygroup)
		return nil, ErrFredNotFound
	}

	if len(readData) > 1 {
		log.Warnf("FReD: Expected 1 item for session ID %s, but got %d. Using the first one.", sessionID, len(readData))
	}

	jsonData := readData[0]
	if jsonData == "" || jsonData == "[]" { // Check for empty string or empty JSON array explicitly
		log.Infof("FReD: Cache hit for session ID: %s, but data is empty. Returning empty token list.", sessionID)
		return []int{}, nil // Return empty slice, not an error
	}

	log.Infof("FReD: Cache hit for session ID: %s in keygroup: %s", sessionID, f.keygroup)
	unmarshalStartTime := time.Now()
	var tokens []int
	err := json.Unmarshal([]byte(jsonData), &tokens)
	log.Debugf("FReD: JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
	if err != nil {
		log.Errorf("FReD: Failed to unmarshal cached tokens for session ID %s: %v. Data: %s", sessionID, err, jsonData)
		return nil, fmt.Errorf("failed to unmarshal cached tokens from FReD: %w", err)
	}
	return tokens, nil
}

// UpdateSessionContext generates the tokenized context from session history and stores it in FReD.
func (f *FReDContextStorage) UpdateSessionContext(sessionID string, sessionManager *SessionManager.SQLiteSessionManager, llamaService *Llama.LlamaClient) error {
	startTime := time.Now()
	defer func() {
		log.Infof("FReD: UpdateSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Updating tokenized context cache for session ID: %s in keygroup: %s", sessionID, f.keygroup)

	getTextContextStartTime := time.Now()
	rawTextContext, err := sessionManager.GetTextSessionContext(sessionID, maxMessagesForFredUpdate)
	log.Debugf("FReD: GetTextSessionContext for session %s (update) took %s", sessionID, time.Since(getTextContextStartTime))
	if err != nil {
		log.Errorf("FReD: Failed to retrieve text context for session ID %s during update: %v", sessionID, err)
		return fmt.Errorf("failed to retrieve text context for FReD update: %w", err)
	}

	var tokenBytes []byte
	if rawTextContext == "" {
		log.Warnf("FReD: No messages found for session ID %s during update. Caching empty token list.", sessionID)
		marshalStartTime := time.Now()
		tokenBytes, err = json.Marshal([]int{}) // Store empty list as "[]"
		log.Debugf("FReD: JSON marshal for empty token list (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			log.Errorf("FReD: Failed to marshal empty token list for session ID %s: %v", sessionID, err)
			return fmt.Errorf("failed to marshal empty token list for FReD: %w", err)
		}
	} else {
		tokenizeStartTime := time.Now()
		tokens, errTokenize := llamaService.Tokenize(rawTextContext)
		log.Debugf("FReD: Tokenization for session %s (update) took %s", sessionID, time.Since(tokenizeStartTime))
		if errTokenize != nil {
			log.Errorf("FReD: Failed to tokenize context for session ID %s: %v", sessionID, errTokenize)
			return fmt.Errorf("failed to tokenize context for FReD: %w", errTokenize)
		}

		marshalStartTime := time.Now()
		tokenBytes, err = json.Marshal(tokens)
		log.Debugf("FReD: JSON marshal for tokens (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			log.Errorf("FReD: Failed to marshal tokens for FReD caching for session ID %s: %v", sessionID, err)
			return fmt.Errorf("failed to marshal tokens for FReD: %w", err)
		}
	}

	dataToStore := string(tokenBytes)
	log.Debugf("FReD: Storing data for session %s: %s", sessionID, dataToStore)

	fredUpdateOpStartTime := time.Now()
	f.client.Update(f.keygroup, sessionID, dataToStore)
	// The FReD client's Update method in the sample doesn't return an error.
	// It might log fatally on errors. A production client should return errors.
	// We assume success if it doesn't panic.
	log.Debugf("FReD: Update operation for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredUpdateOpStartTime))
	log.Infof("FReD: Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

// DeleteSessionContext removes the session context from FReD by updating it with an empty value.
func (f *FReDContextStorage) DeleteSessionContext(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Infof("FReD: DeleteSessionContext (by overwriting with empty) for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Attempting to delete (by overwriting with empty) tokenized context for session ID: %s from keygroup: %s", sessionID, f.keygroup)

	emptyData := "[]" // Representing an empty list of tokens

	fredUpdateOpStartTime := time.Now()
	// Since the FReD client doesn't have a Delete operation, we overwrite with an empty value.
	f.client.Update(f.keygroup, sessionID, emptyData)
	// The FReD client's Update method in the sample doesn't return an error.
	// It might log fatally on errors. A production client should return errors.
	// We assume success if it doesn't panic.
	log.Debugf("FReD: Overwrite (delete) operation for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredUpdateOpStartTime))

	log.Infof("FReD: Successfully deleted (by overwriting with empty) tokenized context from FReD for session ID: %s", sessionID)
	return nil
}

// IsNotFoundError checks if the error signifies that a context was not found in FReD.
func (f *FReDContextStorage) IsNotFoundError(err error) bool {
	return err == ErrFredNotFound
}
