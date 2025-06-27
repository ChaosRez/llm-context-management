package context_storage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings" // Added for strings.Contains
	"time"

	grpcutil "git.tu-berlin.de/mcc-fred/fred/pkg/grpcutil"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	fredClient "llm-context-management/internal/pkg/fredclient"
)

const (
	// DefaultKeygroup is the FReD keygroup where session contexts will be stored.
	defaultFredKeygroup = "default-llm-model"
	userID              = "context-manager"
	expiry              = 0    // 0 = no expiry time for FReD keygroup upon creation
	mutable             = true // Keygroups are mutable by default
)

// ErrFredNotFound is returned when a key is not found in FReD.
var ErrFredNotFound = fmt.Errorf("key not found in FReD")

// FredContextData is the structure stored as JSON in FReD.
type FredContextData struct {
	Context []int `json:"context"`
	Turn    int   `json:"turn"`
}

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
		if err := fs.initializeKeygroup(grpcClient, storageKeygroup, addr); err != nil {
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
// selfAddr is the address of this node, used to determine self NodeId for replication.
func (f *FReDContextStorage) initializeKeygroup(grpcClient fredClient.ClientClient, storageKeygroup string, selfAddr string) error {
	log.Infof("FReD: Initializing keygroup '%s'. Checking existence.", storageKeygroup)

	// 1. Check if the keygroup exists
	keygroupInfo, errKgInfo := grpcClient.GetKeygroupInfo(context.Background(), &fredClient.GetKeygroupInfoRequest{
		Keygroup: storageKeygroup,
	})

	if errKgInfo == nil {
		// Keygroup already exists
		log.Infof("FReD: Keygroup '%s' already exists.", storageKeygroup)
	} else {
		// An error occurred trying to get keygroup info.
		s, ok := status.FromError(errKgInfo)
		isNotFound := ok && s.Code() == codes.NotFound
		// FReD returns unknown grpc code, but gives a message when it already exists
		isCannotGetReplicaError := ok && s.Code() == codes.Unknown && strings.Contains(s.Message(), "cannot get replica for keygroup")

		if isNotFound || isCannotGetReplicaError {
			if isNotFound {
				log.Infof("FReD: Keygroup '%s' not found (grpc NotFound). Attempting to create.", storageKeygroup)
			} else { // isCannotGetReplicaError
				log.Infof("FReD: Keygroup '%s' info inaccessible (grpc Unknown: %s). Assuming it does not exist or needs creation. Attempting to create.", storageKeygroup, s.Message())
			}

			// Keygroup does not exist (or appears not to), so create it
			createReq := &fredClient.CreateKeygroupRequest{
				Keygroup: storageKeygroup,
				Mutable:  mutable,
				Expiry:   expiry,
			}
			_, createErr := grpcClient.CreateKeygroup(context.Background(), createReq)
			if createErr != nil {
				csCreate, cokCreate := status.FromError(createErr)
				if cokCreate && csCreate.Code() == codes.AlreadyExists {
					log.Infof("FReD: Keygroup '%s' already exists (detected during create attempt due to concurrent creation or stale info).", storageKeygroup)
					// Keygroup now exists, try to refresh keygroupInfo.
					refreshedKgInfo, refreshErr := grpcClient.GetKeygroupInfo(context.Background(), &fredClient.GetKeygroupInfoRequest{Keygroup: storageKeygroup})
					if refreshErr != nil {
						log.Warnf("FReD: Failed to refresh KeygroupInfo for '%s' after concurrent creation: %v. Replication might use stale info.", storageKeygroup, refreshErr)
						keygroupInfo = nil // Ensure keygroupInfo is nil if refresh fails
					} else {
						keygroupInfo = refreshedKgInfo
					}
				} else {
					// A real error occurred during creation
					log.Errorf("FReD: Failed to create keygroup '%s': %v (gRPC status: %v, ok: %v)", storageKeygroup, createErr, csCreate, cokCreate)
					return fmt.Errorf("failed to create keygroup '%s': %w", storageKeygroup, createErr)
				}
			} else {
				log.Infof("FReD: Keygroup '%s' created successfully.", storageKeygroup)
				// Keygroup was just created, refresh keygroupInfo for replication logic.
				refreshedKgInfo, refreshErr := grpcClient.GetKeygroupInfo(context.Background(), &fredClient.GetKeygroupInfoRequest{Keygroup: storageKeygroup})
				if refreshErr != nil {
					log.Warnf("FReD: Failed to get KeygroupInfo for '%s' immediately after creation: %v. Replication might use stale info.", storageKeygroup, refreshErr)
					keygroupInfo = nil // Ensure keygroupInfo is nil if refresh fails
				} else {
					keygroupInfo = refreshedKgInfo
				}
			}
		} else {
			// Some other error occurred when checking for keygroup existence (not NotFound or the specific Unknown)
			log.Errorf("FReD: Error checking if keygroup '%s' exists: %v (gRPC status: %v, ok: %v)", storageKeygroup, errKgInfo, s, ok)
			return fmt.Errorf("error checking if keygroup '%s' exists: %w", storageKeygroup, errKgInfo)
		}
	}

	// Add user to keygroup
	permissionsToAdd := []struct {
		perm fredClient.UserRole
		name string
	}{
		{fredClient.UserRole_ReadKeygroup, "Read"},
		{fredClient.UserRole_WriteKeygroup, "Write"},
		{fredClient.UserRole_ConfigureReplica, "ConfigureReplica"},
	}
	log.Infof("FReD: Ensuring user '%s' has permissions for keygroup '%s'.", userID, storageKeygroup)
	for _, p := range permissionsToAdd {
		addUserReq := &fredClient.AddUserRequest{
			Keygroup: storageKeygroup,
			User:     userID,
			Role:     p.perm,
		}
		_, errAddUser := grpcClient.AddUser(context.Background(), addUserReq)
		if errAddUser != nil {
			s, ok := status.FromError(errAddUser)
			if ok {
				// Log as warning, as permission might already exist or another node might be configuring.
				log.Warnf("FReD: Problem adding %s permission for user '%s' to keygroup '%s': %v (code: %s, message: %s). This might be non-critical if permission already exists.", p.name, userID, storageKeygroup, errAddUser, s.Code(), s.Message())
			} else {
				log.Warnf("FReD: Problem adding %s permission for user '%s' to keygroup '%s': %v. This might be non-critical.", p.name, userID, storageKeygroup, errAddUser)
			}
		} else {
			log.Infof("FReD: Successfully ensured %s permission for user '%s' on keygroup '%s'.", p.name, userID, storageKeygroup)
		}
	}

	// --- Replicate keygroup to other nodes ---
	if keygroupInfo == nil {
		log.Warnf("FReD: KeygroupInfo for '%s' is unavailable (e.g. due to earlier error during GetKeygroupInfo refresh), skipping replication logic.", storageKeygroup)
		return nil
	}

	// Get all known nodes in the FReD cluster
	allReplicasResp, err := grpcClient.GetAllReplica(context.Background(), &fredClient.Empty{})
	if err != nil {
		log.Warnf("FReD: Could not get all replicas for replication: %v", err)
		return nil
	}

	var selfNodeId string
	for _, node := range allReplicasResp.Replicas {
		if node.Host == selfAddr {
			selfNodeId = node.NodeId
			break
		}
	}
	// Fallback: try to match by host in keygroupInfo.Replica if not found
	if selfNodeId == "" {
		log.Warnf("FReD: Could not determine self NodeId using address '%s' from all replicas list for keygroup '%s'. Attempting to find from keygroup's current replicas.", selfAddr, storageKeygroup)
		for _, r := range keygroupInfo.Replica {
			if r.Host == selfAddr {
				selfNodeId = r.NodeId
				log.Infof("FReD: Determined self NodeId '%s' from keygroup '%s' existing replicas.", selfNodeId, storageKeygroup)
				break
			}
		}
	}

	if selfNodeId == "" {
		log.Warnf("FReD: Self NodeId could not be definitively determined for keygroup '%s' using address '%s'. Replication to other nodes will proceed; self-node might not be skipped if its ID is unknown.", storageKeygroup, selfAddr)
	} else {
		log.Infof("FReD: Self NodeId determined as '%s' for keygroup '%s' using address '%s'.", selfNodeId, storageKeygroup, selfAddr)
	}

	// Build a set of current replica NodeIds for this keygroup
	currentReplicas := make(map[string]struct{})
	for _, r := range keygroupInfo.Replica {
		currentReplicas[r.NodeId] = struct{}{}
	}

	log.Infof("FReD: Replicating keygroup '%s' to other nodes if necessary. Current known replicas: %d.", storageKeygroup, len(currentReplicas))
	for _, node := range allReplicasResp.Replicas {
		if selfNodeId != "" && node.NodeId == selfNodeId {
			log.Debugf("FReD: Skipping replication of keygroup '%s' to self node '%s'.", storageKeygroup, selfNodeId)
			continue
		}
		if _, alreadyReplica := currentReplicas[node.NodeId]; alreadyReplica {
			log.Debugf("FReD: Node '%s' is already a replica of keygroup '%s'. Skipping.", node.NodeId, storageKeygroup)
			continue
		}

		log.Infof("FReD: Attempting to add node '%s' (Host: %s) as a replica for keygroup '%s'.", node.NodeId, node.Host, storageKeygroup)
		addReplicaReq := &fredClient.AddReplicaRequest{
			Keygroup: storageKeygroup,
			NodeId:   node.NodeId,
			Expiry:   expiry,
		}
		_, errAddReplica := grpcClient.AddReplica(context.Background(), addReplicaReq)
		if errAddReplica != nil {
			s, ok := status.FromError(errAddReplica)
			if ok && s.Code() == codes.AlreadyExists {
				log.Infof("FReD: Node '%s' is already a replica of keygroup '%s' (confirmed by AddReplica).", node.NodeId, storageKeygroup)
			} else if ok {
				log.Errorf("FReD: Failed to replicate keygroup '%s' to node '%s' (Host: %s): %v (code: %s, message: %s)", storageKeygroup, node.NodeId, node.Host, errAddReplica, s.Code(), s.Message())
			} else {
				log.Errorf("FReD: Failed to replicate keygroup '%s' to node '%s' (Host: %s): %v", storageKeygroup, node.NodeId, node.Host, errAddReplica)
			}
		} else {
			log.Infof("FReD: Successfully initiated replication of keygroup '%s' to node '%s' (Host: %s).", storageKeygroup, node.NodeId, node.Host)
		}
	}
	return nil
}

// GetTokenizedSessionContext retrieves the tokenized session context and turn from FReD.
func (f *FReDContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, int, error) {
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
			return nil, 0, ErrFredNotFound
		}
		log.Errorf("FReD: Failed to read from keygroup '%s', id '%s': %v", f.keygroup, sessionID, err)
		return nil, 0, fmt.Errorf("failed to read from FReD: %w", err)
	}

	if readResp == nil || len(readResp.Data) == 0 {
		log.Warnf("FReD: Cache miss for session ID: '%s' in keygroup: '%s'. No data items returned.", sessionID, f.keygroup)
		return nil, 0, ErrFredNotFound // Or []int{}, nil if empty is not an error but a valid "not found" state for tokens
	}

	if len(readResp.Data) > 1 {
		log.Warnf("FReD: Expected 1 item for session ID '%s', but got %d. Using the first one.", sessionID, len(readResp.Data))
	}

	jsonData := readResp.Data[0].Val
	if jsonData == "" {
		log.Warnf("FReD: Cache hit for session ID: %s, but data is empty. Returning empty context and turn 0.", sessionID)
		return []int{}, 0, nil
	}

	log.Infof("FReD: Cache hit for session ID: %s in keygroup: %s", sessionID, f.keygroup)
	unmarshalStartTime := time.Now()
	var data FredContextData
	errUnmarshal := json.Unmarshal([]byte(jsonData), &data)
	log.Debugf("FReD: JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
	if errUnmarshal != nil {
		log.Errorf("FReD: Failed to unmarshal cached data for session ID %s: %v. Data: %s", sessionID, errUnmarshal, jsonData)
		return nil, 0, fmt.Errorf("failed to unmarshal cached data from FReD: %w", errUnmarshal)
	}
	return data.Context, data.Turn, nil
}

// UpdateSessionContext stores the provided tokenized context and new turn in FReD.
func (f *FReDContextStorage) UpdateSessionContext(sessionID string, newFullTokenizedContext []int, newTurn int) error {
	startTime := time.Now()
	defer func() {
		log.Infof("FReD: UpdateSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	log.Infof("FReD: Updating tokenized context cache for session ID: %s in keygroup: %s to turn %d", sessionID, f.keygroup, newTurn)

	if newFullTokenizedContext == nil {
		// This case might occur if we intend to clear the cache with an empty list.
		// Or, if it's an error state, the caller should handle it.
		// For now, assume nil means store an empty list.
		log.Warnf("FReD: newFullTokenizedContext is nil for session ID %s. Caching empty token list.", sessionID)
		newFullTokenizedContext = []int{}
	}

	data := FredContextData{
		Context: newFullTokenizedContext,
		Turn:    newTurn,
	}

	marshalStartTime := time.Now()
	tokenBytes, err := json.Marshal(data)
	log.Debugf("FReD: JSON marshal for new context data (session %s) took %s", sessionID, time.Since(marshalStartTime))
	if err != nil {
		log.Errorf("FReD: Failed to marshal data for FReD caching for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to marshal data for FReD: %w", err)
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
