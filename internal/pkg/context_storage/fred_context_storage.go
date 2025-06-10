package context_storage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	grpcutil "git.tu-berlin.de/mcc-fred/fred/pkg/grpcutil"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	SessionManager "llm-context-management/internal/app/session_manager"
	fredClient "llm-context-management/internal/pkg/fredclient"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

const (
	// DefaultKeygroup is the FReD keygroup where session contexts will be stored.
	defaultFredKeygroup      = "llm_session_contexts"
	maxMessagesForFredUpdate = 10000 // Similar to Redis implementation
	expiry                   = 0     // 0 = no expiry time for FReD keygroup upon creation
	mutable                  = true  // Keygroups are mutable by default
)

// ErrFredNotFound is returned when a key is not found in FReD.
var ErrFredNotFound = fmt.Errorf("key not found in FReD")

// FReDContextStorage implements the ContextStorage interface using FReD.
type FReDContextStorage struct {
	client   fredClient.ClientClient
	keygroup string
}

// NewFReDContextStorage creates a new FReDContextStorage.
// addr is the FReD node address (e.g., "127.0.0.1:9001").
// createKeygroupIfNotExist will attempt to create the keygroup if it doesn't exist.
func NewFReDContextStorage(addr string, keygroup string, createKeygroupIfNotExist bool) (*FReDContextStorage, error) {
	// Define relative paths for certificates and keys
	certDir := "fred/cert/"
	clientCertPath := filepath.Join(certDir, "frededge1.crt") // FIXME: different certs for different nodes
	clientKeyPath := filepath.Join(certDir, "frededge1.key")
	caCertPath := filepath.Join(certDir, "ca.crt")

	// Setup gRPC client with TLS using GetCredsFromConfig
	tlsConfig := &tls.Config{} // GetCredsFromConfig will populate this
	creds, _, err := grpcutil.GetCredsFromConfig(
		clientCertPath,
		clientKeyPath,
		[]string{caCertPath},
		false, // insecure
		false, // skipVerify (set to false for security)
		tlsConfig,
	)
	if err != nil {
		log.Errorf("Failed to initialize FReD client credentials: %v", err)
		return nil, fmt.Errorf("failed to initialize FReD client credentials: %w", err)
	}

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Errorf("Failed to connect to FReD gRPC server at %s: %v", addr, err)
		return nil, fmt.Errorf("failed to connect to FReD gRPC server at %s: %w", addr, err)
	}
	// defer conn.Close() // Connection should be managed by the lifetime of FReDContextStorage

	grpcClient := fredClient.NewClientClient(conn)

	storageKeygroup := keygroup
	if storageKeygroup == "" {
		storageKeygroup = defaultFredKeygroup
	}

	fs := &FReDContextStorage{
		client:   grpcClient,
		keygroup: storageKeygroup,
	}

	if createKeygroupIfNotExist {
		if err := fs.initializeKeygroup(grpcClient, storageKeygroup); err != nil {
			// Attempt to close the connection if initialization fails.
			if connErr := conn.Close(); connErr != nil {
				log.Warnf("FReD: Failed to close gRPC connection after initialization error: %v", connErr)
			}
			return nil, err // Return the initialization error
		}
	}

	return fs, nil
}

// initializeKeygroup creates the keygroup if it doesn't exist and adds necessary user permissions.
func (f *FReDContextStorage) initializeKeygroup(grpcClient fredClient.ClientClient, storageKeygroup string) error {
	log.Infof("FReD: Attempting to create keygroup '%s'.", storageKeygroup)

	// FIXME: this not properly checking if keygroup exists and get errors with ok==true when it already exists
	createReq := &fredClient.CreateKeygroupRequest{
		Keygroup: storageKeygroup,
		Mutable:  mutable,
		Expiry:   expiry,
	}
	_, err := grpcClient.CreateKeygroup(context.Background(), createReq)
	if err != nil {
		// Check if error is "already exists" and treat as non-fatal for this specific operation
		s, ok := status.FromError(err)
		log.Debugf("FReD: CreateKeygroup response status: '%v', ok: '%v'", s, ok)
		if ok && s.Code() == codes.AlreadyExists { // Assuming FReD returns AlreadyExists
			log.Infof("FReD: Keygroup '%s' already exists.", storageKeygroup)
		} else if ok { // Other gRPC error
			log.Errorf("FReD: Failed to create keygroup '%s': %v (code: %s, message: %s)", storageKeygroup, err, s.Code(), s.Message())
			return fmt.Errorf("failed to create keygroup '%s': %w", storageKeygroup, err)
		} else { // Non-gRPC error
			log.Errorf("FReD: Failed to create keygroup '%s': %v", storageKeygroup, err)
			return fmt.Errorf("failed to create keygroup '%s': %w", storageKeygroup, err)
		}
	} else {
		log.Infof("FReD: Keygroup '%s' creation initiated successfully.", storageKeygroup)
	}

	// Add user to keygroup after creation or if it already exists.
	// FReD user IDs (often certificate common names) must match what FReD expects.
	userID := "context-manager"

	permissionsToAdd := []struct {
		perm fredClient.UserRole
		name string
	}{
		{fredClient.UserRole_ReadKeygroup, "Read"},
		{fredClient.UserRole_WriteKeygroup, "Write"},
		{fredClient.UserRole_ConfigureReplica, "ConfigureReplica"},
	}

	log.Infof("FReD: Adding permissions to add keygroup '%s'.", storageKeygroup)
	for _, p := range permissionsToAdd {
		//log.Infof("FReD: Attempting to add user '%s' to keygroup '%s' with %s permission.", userID, storageKeygroup, p.name)
		addUserReq := &fredClient.AddUserRequest{
			Keygroup: storageKeygroup,
			User:     userID,
			Role:     p.perm,
		}
		_, errAddUser := grpcClient.AddUser(context.Background(), addUserReq)
		if errAddUser != nil {
			s, ok := status.FromError(errAddUser)
			// FReD might return an error if the permission already exists,
			// but specific error codes for "already exists" for permissions are not standard in gRPC.
			// We log the error and continue, assuming the setup might still be usable.
			if ok {
				log.Warnf("FReD: Failed to add %s permission for user '%s' to keygroup '%s': %v (code: %s, message: %s). This might be non-critical if permission already exists.", p.name, userID, storageKeygroup, errAddUser, s.Code(), s.Message())
			} else {
				log.Warnf("FReD: Failed to add %s permission for user '%s' to keygroup '%s': %v. This might be non-critical if permission already exists.", p.name, userID, storageKeygroup, errAddUser)
			}
			// Not returning error here to allow startup even if adding user fails (e.g., user already has permissions, or stricter FReD setup).
		} else {
			log.Infof("FReD: Successfully added %s permission for user '%s' to keygroup '%s' (or permission already existed).", p.name, userID, storageKeygroup)
		}
	}
	return nil
}

// GetTokenizedSessionContext retrieves the tokenized session context from FReD.
func (f *FReDContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("FReD: GetTokenizedSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Attempting to retrieve tokenized context for session ID: %s from keygroup: %s", sessionID, f.keygroup)

	readReq := &fredClient.ReadRequest{
		Keygroup: f.keygroup,
		Id:       sessionID,
	}

	fredReadStartTime := time.Now()
	// For a client-side timeout, use context.WithTimeout here.
	// Example: ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultFredReadTimeout)*time.Millisecond)
	// defer cancel()
	// readResp, err := f.client.Read(ctx, readReq)
	readResp, err := f.client.Read(context.Background(), readReq)
	log.Debugf("FReD: Read for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredReadStartTime))

	if err != nil {
		s, ok := status.FromError(err)
		if ok && s.Code() == codes.NotFound {
			log.Warnf("FReD: Cache miss (NotFound) for session ID: %s in keygroup: %s.", sessionID, f.keygroup)
			return nil, ErrFredNotFound
		}
		log.Errorf("FReD: Failed to read from keygroup '%s', id '%s': %v", f.keygroup, sessionID, err)
		return nil, fmt.Errorf("failed to read from FReD: %w", err)
	}

	if readResp == nil || len(readResp.Data) == 0 {
		log.Warnf("FReD: Cache miss for session ID: '%s' in keygroup: '%s'. No data items returned.", sessionID, f.keygroup)
		return nil, ErrFredNotFound // Or []int{}, nil if empty is not an error but a valid "not found" state for tokens
	}

	if len(readResp.Data) > 1 {
		log.Warnf("FReD: Expected 1 item for session ID '%s', but got %d. Using the first one.", sessionID, len(readResp.Data))
	}

	jsonData := readResp.Data[0].Val
	if jsonData == "" || jsonData == "[]" { // Check for empty string or empty JSON array explicitly
		log.Infof("FReD: Cache hit for session ID: %s, but data is empty. Returning empty token list.", sessionID)
		return []int{}, nil // Return empty slice, not an error
	}

	log.Infof("FReD: Cache hit for session ID: %s in keygroup: %s", sessionID, f.keygroup)
	unmarshalStartTime := time.Now()
	var tokens []int
	errUnmarshal := json.Unmarshal([]byte(jsonData), &tokens)
	log.Debugf("FReD: JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
	if errUnmarshal != nil {
		log.Errorf("FReD: Failed to unmarshal cached tokens for session ID %s: %v. Data: %s", sessionID, errUnmarshal, jsonData)
		return nil, fmt.Errorf("failed to unmarshal cached tokens from FReD: %w", errUnmarshal)
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

	updateReq := &fredClient.UpdateRequest{
		Keygroup: f.keygroup,
		Id:       sessionID,
		Data:     dataToStore,
	}

	fredUpdateOpStartTime := time.Now()
	_, err = f.client.Update(context.Background(), updateReq)
	log.Debugf("FReD: Update operation for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredUpdateOpStartTime))
	if err != nil {
		log.Errorf("FReD: Failed to update key %s in keygroup %s: %v", sessionID, f.keygroup, err)
		return fmt.Errorf("failed to update FReD: %w", err)
	}

	log.Infof("FReD: Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

// DeleteSessionContext removes the session context from FReD.
func (f *FReDContextStorage) DeleteSessionContext(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Infof("FReD: DeleteSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Attempting to delete tokenized context for session ID: %s from keygroup: %s", sessionID, f.keygroup)

	deleteReq := &fredClient.DeleteRequest{
		Keygroup: f.keygroup,
		Id:       sessionID,
	}

	fredDeleteOpStartTime := time.Now()
	_, err := f.client.Delete(context.Background(), deleteReq)
	log.Debugf("FReD: Delete operation for key %s in keygroup %s took %s", sessionID, f.keygroup, time.Since(fredDeleteOpStartTime))

	if err != nil {
		// Check if the error is NotFound, which can be considered a successful deletion if the item didn't exist.
		s, ok := status.FromError(err)
		if ok && s.Code() == codes.NotFound {
			log.Warnf("FReD: Attempted to delete key %s in keygroup %s, but it was not found. Considered deleted.", sessionID, f.keygroup)
			return nil // Or return ErrFredNotFound if the caller needs to know it wasn't there
		}
		log.Errorf("FReD: Failed to delete key %s in keygroup %s: %v", sessionID, f.keygroup, err)
		return fmt.Errorf("failed to delete from FReD: %w", err)
	}

	log.Infof("FReD: Successfully deleted tokenized context from FReD for session ID: %s", sessionID)
	return nil
}

// IsNotFoundError checks if the error signifies that a context was not found in FReD.
func (f *FReDContextStorage) IsNotFoundError(err error) bool {
	return err == ErrFredNotFound
}
