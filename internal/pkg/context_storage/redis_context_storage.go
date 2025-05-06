package context_storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	SessionManager "llm-context-management/internal/app/session_manager"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

// TODO consider making maxMessages configurable or using a constant
const maxMessagesForUpdate = 10000

type RedisContextStorage struct {
	client *redis.Client
}

func NewRedisContextStorage(addr, password string, db int) *RedisContextStorage {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &RedisContextStorage{client: client}
}

// GetTokenizedSessionContext retrieves the tokenized context for a session, checking Redis cache first.
// It returns the tokenized context as []int.
func (r *RedisContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("GetTokenizedSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Attempting to retrieve tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	// 1. Check Redis cache first.
	redisStartTime := time.Now()
	cachedTokenJSON, err := r.client.Get(ctx, cacheKey).Result()
	log.Debugf("Redis GET for %s took %s", cacheKey, time.Since(redisStartTime))

	if err == nil {
		// Cache hit
		log.Infof("Cache hit for session ID: %s", sessionID)
		unmarshalStartTime := time.Now()
		var tokens []int
		err = json.Unmarshal([]byte(cachedTokenJSON), &tokens)
		log.Debugf("JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
		if err != nil {
			log.Errorf("Failed to unmarshal cached tokens for session ID %s: %v", sessionID, err)
			return nil, fmt.Errorf("failed to unmarshal cached tokens: %w", err)
		}
		return tokens, nil
	} else if err != redis.Nil {
		// Error other than cache miss
		log.Errorf("Error checking Redis cache for session ID %s: %v", sessionID, err)
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}

	// 2. Cache miss (err == redis.Nil)
	log.Warnf("Cache miss for session ID: %s. Generating and caching tokenized context.", sessionID)
	return nil, nil
}

// UpdateSessionContext generates the latest raw text context, tokenizes it, and updates the cache in Redis.
func (r *RedisContextStorage) UpdateSessionContext(sessionID string, sessionManager *SessionManager.SQLiteSessionManager, llamaService *Llama.LlamaClient) error {
	startTime := time.Now()
	defer func() {
		log.Infof("UpdateSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Updating tokenized context cache for session ID: %s using cache key: %s", sessionID, cacheKey)

	// 1. Get the latest raw text context (using a large number for maxMessages to effectively get all).
	getTextContextStartTime := time.Now()
	rawTextContext, err := sessionManager.GetTextSessionContext(sessionID, maxMessagesForUpdate)
	log.Debugf("GetTextSessionContext for session %s (update) took %s", sessionID, time.Since(getTextContextStartTime))
	if err != nil {
		log.Errorf("Failed to retrieve text context for session ID %s during update: %v", sessionID, err)
		return err
	}

	var tokenBytes []byte
	if rawTextContext == "" {
		log.Warnf("No messages found for session ID %s during update. Caching empty token list.", sessionID)
		marshalStartTime := time.Now()
		// Marshal an empty slice to represent empty context
		tokenBytes, err = json.Marshal([]int{})
		log.Debugf("JSON marshal for empty token list (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			// This should ideally not happen for an empty slice
			log.Errorf("Failed to marshal empty token list for session ID %s during update: %v", sessionID, err)
			return err
		}
	} else {
		// 2. Tokenize the context.
		tokenizeStartTime := time.Now()
		tokens, errTokenize := llamaService.Tokenize(rawTextContext)
		log.Debugf("Tokenization for session %s (update) took %s", sessionID, time.Since(tokenizeStartTime))
		if errTokenize != nil {
			log.Errorf("Failed to tokenize context for session ID %s during update: %v", sessionID, errTokenize)
			return errTokenize
		}

		marshalStartTime := time.Now()
		// 3. Marshal tokens to JSON string for storage.
		tokenBytes, err = json.Marshal(tokens)
		log.Debugf("JSON marshal for tokens (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			log.Errorf("Failed to marshal tokens for caching for session ID %s during update: %v", sessionID, err)
			return err
		}
	}

	redisSetStartTime := time.Now()
	// 4. Update tokenized context in Redis.
	// Use Set with 0 expiration for persistence.
	err = r.client.Set(ctx, cacheKey, string(tokenBytes), 0).Err()
	log.Debugf("Redis SET for %s took %s", cacheKey, time.Since(redisSetStartTime))
	if err != nil {
		log.Errorf("Failed to update tokenized context in Redis for session ID %s: %v", sessionID, err)
		return err
	}

	log.Infof("Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

// DeleteSessionContext removes the tokenized context for a session from Redis.
func (r *RedisContextStorage) DeleteSessionContext(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Infof("DeleteSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Attempting to delete tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	redisDelStartTime := time.Now()
	err := r.client.Del(ctx, cacheKey).Err()
	log.Debugf("Redis DEL for %s took %s", cacheKey, time.Since(redisDelStartTime))
	if err != nil {
		log.Errorf("Failed to delete tokenized context from Redis for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to delete redis key %s: %w", cacheKey, err)
	}

	log.Infof("Successfully deleted tokenized context from Redis for session ID: %s", sessionID)
	return nil
}
