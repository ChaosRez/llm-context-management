package context_storage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	SessionManager "llm-context-management/internal/app/session_manager"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

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
	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Attempting to retrieve tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	// 1. Check Redis cache first.
	cachedTokenJSON, err := r.client.Get(ctx, cacheKey).Result()
	if err == nil {
		// Cache hit
		log.Infof("Cache hit for session ID: %s", sessionID)
		var tokens []int
		err = json.Unmarshal([]byte(cachedTokenJSON), &tokens)
		if err != nil {
			log.Errorf("Failed to unmarshal cached tokens for session ID %s: %v", sessionID, err)
			// TODO Decide if we should proceed to regenerate or return error. Regenerating might be safer.
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
	// Return nil, nil to indicate cache miss without error, letting the caller handle generation.
	return nil, nil
}

// UpdateSessionContext generates the latest raw text context, tokenizes it, and updates the cache in Redis.
func (r *RedisContextStorage) UpdateSessionContext(sessionID string, sessionManager *SessionManager.SQLiteSessionManager, llamaService *Llama.LlamaClient) error {
	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Updating tokenized context cache for session ID: %s using cache key: %s", sessionID, cacheKey)

	// 1. Get the latest raw text context (using a large number for maxMessages to effectively get all).
	// Consider making maxMessages configurable or using a constant
	const maxMessagesForUpdate = 10000
	rawTextContext, err := sessionManager.GetTextSessionContext(sessionID, maxMessagesForUpdate)
	if err != nil {
		log.Errorf("Failed to retrieve text context for session ID %s during update: %v", sessionID, err)
		return err
	}

	var tokenBytes []byte
	if rawTextContext == "" {
		log.Warnf("No messages found for session ID %s during update. Caching empty token list.", sessionID)
		// Marshal an empty slice to represent empty context
		tokenBytes, err = json.Marshal([]int{})
		if err != nil {
			// This should ideally not happen for an empty slice
			log.Errorf("Failed to marshal empty token list for session ID %s during update: %v", sessionID, err)
			return err
		}
	} else {
		// 2. Tokenize the context.
		tokens, err := llamaService.Tokenize(rawTextContext)
		if err != nil {
			log.Errorf("Failed to tokenize context for session ID %s during update: %v", sessionID, err)
			return err
		}

		// 3. Marshal tokens to JSON string for storage.
		tokenBytes, err = json.Marshal(tokens)
		if err != nil {
			log.Errorf("Failed to marshal tokens for caching for session ID %s during update: %v", sessionID, err)
			return err
		}
	}

	// 4. Update tokenized context in Redis.
	// Use Set with 0 expiration for persistence.
	err = r.client.Set(ctx, cacheKey, string(tokenBytes), 0).Err()
	if err != nil {
		log.Errorf("Failed to update tokenized context in Redis for session ID %s: %v", sessionID, err)
		return err
	}

	log.Infof("Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

// DeleteSessionContext removes the tokenized context for a session from Redis.
func (r *RedisContextStorage) DeleteSessionContext(sessionID string) error {
	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Attempting to delete tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	err := r.client.Del(ctx, cacheKey).Err()
	if err != nil {
		// Log error but don't necessarily fail the whole operation if deletion fails
		log.Errorf("Failed to delete tokenized context from Redis for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to delete redis key %s: %w", cacheKey, err)
	}

	log.Infof("Successfully deleted tokenized context from Redis for session ID: %s", sessionID)
	return nil
}
