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

const maxMessagesForUpdate = 10000 // TODO consider making maxMessages configurable or using a constant

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

func (r *RedisContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("Redis: GetTokenizedSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Redis: Attempting to retrieve tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	redisStartTime := time.Now()
	cachedTokenJSON, err := r.client.Get(ctx, cacheKey).Result()
	log.Debugf("Redis: GET for %s took %s", cacheKey, time.Since(redisStartTime))

	if err == nil {
		log.Infof("Redis: Cache hit for session ID: %s", sessionID)
		unmarshalStartTime := time.Now()
		var tokens []int
		err = json.Unmarshal([]byte(cachedTokenJSON), &tokens)
		log.Debugf("Redis: JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
		if err != nil {
			log.Errorf("Redis: Failed to unmarshal cached tokens for session ID %s: %v", sessionID, err)
			return nil, fmt.Errorf("failed to unmarshal cached tokens: %w", err)
		}
		return tokens, nil
	} else if err != redis.Nil {
		log.Errorf("Redis: Error checking Redis cache for session ID %s: %v", sessionID, err)
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}

	log.Warnf("Redis: Cache miss for session ID: %s. Generating and caching tokenized context.", sessionID)
	// Return redis.Nil to indicate not found, which will be checked by IsNotFoundError
	return nil, redis.Nil
}

func (r *RedisContextStorage) UpdateSessionContext(sessionID string, sessionManager *SessionManager.SQLiteSessionManager, llamaService *Llama.LlamaClient) error {
	startTime := time.Now()
	defer func() {
		log.Infof("Redis: UpdateSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Redis: Updating tokenized context cache for session ID: %s using cache key: %s", sessionID, cacheKey)

	getTextContextStartTime := time.Now()
	rawTextContext, err := sessionManager.GetTextSessionContext(sessionID, maxMessagesForUpdate)
	log.Debugf("Redis: GetTextSessionContext for session %s (update) took %s", sessionID, time.Since(getTextContextStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to retrieve text context for session ID %s during update: %v", sessionID, err)
		return err
	}

	var tokenBytes []byte
	if rawTextContext == "" {
		log.Warnf("Redis: No messages found for session ID %s during update. Caching empty token list.", sessionID)
		marshalStartTime := time.Now()
		tokenBytes, err = json.Marshal([]int{})
		log.Debugf("Redis: JSON marshal for empty token list (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			log.Errorf("Redis: Failed to marshal empty token list for session ID %s during update: %v", sessionID, err)
			return err
		}
	} else {
		tokenizeStartTime := time.Now()
		tokens, errTokenize := llamaService.Tokenize(rawTextContext)
		log.Debugf("Redis: Tokenization for session %s (update) took %s", sessionID, time.Since(tokenizeStartTime))
		if errTokenize != nil {
			log.Errorf("Redis: Failed to tokenize context for session ID %s during update: %v", sessionID, errTokenize)
			return errTokenize
		}

		marshalStartTime := time.Now()
		tokenBytes, err = json.Marshal(tokens)
		log.Debugf("Redis: JSON marshal for tokens (session %s) took %s", sessionID, time.Since(marshalStartTime))
		if err != nil {
			log.Errorf("Redis: Failed to marshal tokens for caching for session ID %s during update: %v", sessionID, err)
			return err
		}
	}

	redisSetStartTime := time.Now()
	err = r.client.Set(ctx, cacheKey, string(tokenBytes), 0).Err()
	log.Debugf("Redis: SET for %s took %s", cacheKey, time.Since(redisSetStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to update tokenized context in Redis for session ID %s: %v", sessionID, err)
		return err
	}

	log.Infof("Redis: Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

func (r *RedisContextStorage) DeleteSessionContext(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Infof("Redis: DeleteSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Redis: Attempting to delete tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	redisDelStartTime := time.Now()
	err := r.client.Del(ctx, cacheKey).Err()
	log.Debugf("Redis: DEL for %s took %s", cacheKey, time.Since(redisDelStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to delete tokenized context from Redis for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to delete redis key %s: %w", cacheKey, err)
	}

	log.Infof("Redis: Successfully deleted tokenized context from Redis for session ID: %s", sessionID)
	return nil
}

// IsNotFoundError checks if the error is redis.Nil, indicating a cache miss.
func (r *RedisContextStorage) IsNotFoundError(err error) bool {
	return err == redis.Nil
}
