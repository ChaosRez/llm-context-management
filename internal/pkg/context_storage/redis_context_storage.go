package context_storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

// RedisContextData is the structure stored as JSON in Redis.
type RedisContextData struct {
	Context []int `json:"context"`
	Turn    int   `json:"turn"`
}

// RawRedisContextData is the structure stored as JSON in Redis for raw context.
type RawRedisContextData struct {
	Messages []RawMessage `json:"messages"`
	Turn     int          `json:"turn"`
}

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

func (r *RedisContextStorage) GetTokenizedSessionContext(sessionID string) ([]int, int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("Redis: GetTokenizedSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Redis: Attempting to retrieve tokenized context for session ID: %s from cache key: %s", sessionID, cacheKey)

	redisStartTime := time.Now()
	cachedJSON, err := r.client.Get(ctx, cacheKey).Result()
	log.Debugf("Redis: GET for %s took %s", cacheKey, time.Since(redisStartTime))

	if err == redis.Nil {
		log.Warnf("Redis: Cache miss for session ID: %s.", sessionID)
		return nil, 0, redis.Nil
	} else if err != nil {
		log.Errorf("Redis: Error checking Redis cache for session ID %s: %v", sessionID, err)
		return nil, 0, fmt.Errorf("failed to check cache: %w", err)
	}

	if cachedJSON == "" {
		log.Warnf("Redis: Cache hit for session ID: %s, but data is empty. Returning empty context and turn 0.", sessionID)
		return []int{}, 0, nil
	}

	log.Infof("Redis: Cache hit for session ID: %s", sessionID)
	unmarshalStartTime := time.Now()
	var data RedisContextData
	err = json.Unmarshal([]byte(cachedJSON), &data)
	log.Debugf("Redis: JSON unmarshal for session %s took %s", sessionID, time.Since(unmarshalStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to unmarshal cached data for session ID %s: %v. Data: %s", sessionID, err, cachedJSON)
		return nil, 0, fmt.Errorf("failed to unmarshal cached data from Redis: %w", err)
	}
	return data.Context, data.Turn, nil
}

func (r *RedisContextStorage) GetRawSessionContext(sessionID string) ([]RawMessage, int, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("Redis: GetRawSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "raw_ctx_" + sessionID
	log.Infof("Redis: Attempting to retrieve raw context for session ID: %s from cache key: %s", sessionID, cacheKey)

	redisStartTime := time.Now()
	cachedJSON, err := r.client.Get(ctx, cacheKey).Result()
	log.Debugf("Redis: GET for %s took %s", cacheKey, time.Since(redisStartTime))

	if err == redis.Nil {
		log.Warnf("Redis: Cache miss for raw session ID: %s.", sessionID)
		return nil, 0, redis.Nil
	} else if err != nil {
		log.Errorf("Redis: Error checking Redis cache for raw session ID %s: %v", sessionID, err)
		return nil, 0, fmt.Errorf("failed to check raw cache: %w", err)
	}

	if cachedJSON == "" {
		log.Warnf("Redis: Cache hit for raw session ID: %s, but data is empty. Returning empty context and turn 0.", sessionID)
		return []RawMessage{}, 0, nil
	}

	log.Infof("Redis: Cache hit for raw session ID: %s", sessionID)
	unmarshalStartTime := time.Now()
	var data RawRedisContextData
	err = json.Unmarshal([]byte(cachedJSON), &data)
	log.Debugf("Redis: JSON unmarshal for raw session %s took %s", sessionID, time.Since(unmarshalStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to unmarshal cached raw data for session ID %s: %v. Data: %s", sessionID, err, cachedJSON)
		return nil, 0, fmt.Errorf("failed to unmarshal cached raw data from Redis: %w", err)
	}
	return data.Messages, data.Turn, nil
}

func (r *RedisContextStorage) UpdateSessionContext(sessionID string, newFullTokenizedContext []int, newTurn int) error {
	startTime := time.Now()
	defer func() {
		log.Infof("Redis: UpdateSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "ctx_" + sessionID
	log.Infof("Redis: Updating tokenized context cache for session ID: %s to turn %d using cache key: %s", sessionID, newTurn, cacheKey)

	if newFullTokenizedContext == nil {
		log.Warnf("Redis: newFullTokenizedContext is nil for session ID %s. Caching empty token list.", sessionID)
		newFullTokenizedContext = []int{}
	}

	data := RedisContextData{
		Context: newFullTokenizedContext,
		Turn:    newTurn,
	}

	marshalStartTime := time.Now()
	dataBytes, err := json.Marshal(data)
	log.Debugf("Redis: JSON marshal for new context data (session %s) took %s", sessionID, time.Since(marshalStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to marshal data for caching for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to marshal data for Redis: %w", err)
	}

	redisSetStartTime := time.Now()
	err = r.client.Set(ctx, cacheKey, dataBytes, 0).Err()
	log.Debugf("Redis: SET for %s took %s", cacheKey, time.Since(redisSetStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to update tokenized context in Redis for session ID %s: %v", sessionID, err)
		return err
	}

	log.Infof("Redis: Tokenized context cache successfully updated for session ID: %s", sessionID)
	return nil
}

func (r *RedisContextStorage) UpdateRawSessionContext(sessionID string, newMessages []RawMessage, newTurn int) error {
	startTime := time.Now()
	defer func() {
		log.Infof("Redis: UpdateRawSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	cacheKey := "raw_ctx_" + sessionID
	log.Infof("Redis: Updating raw context cache for session ID: %s to turn %d using cache key: %s", sessionID, newTurn, cacheKey)

	if newMessages == nil {
		log.Warnf("Redis: newMessages is nil for session ID %s. Caching empty message list.", sessionID)
		newMessages = []RawMessage{}
	}

	data := RawRedisContextData{
		Messages: newMessages,
		Turn:     newTurn,
	}

	marshalStartTime := time.Now()
	dataBytes, err := json.Marshal(data)
	log.Debugf("Redis: JSON marshal for new raw context data (session %s) took %s", sessionID, time.Since(marshalStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to marshal raw data for caching for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to marshal raw data for Redis: %w", err)
	}

	redisSetStartTime := time.Now()
	err = r.client.Set(ctx, cacheKey, dataBytes, 0).Err()
	log.Debugf("Redis: SET for %s took %s", cacheKey, time.Since(redisSetStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to update raw context in Redis for session ID %s: %v", sessionID, err)
		return err
	}

	log.Infof("Redis: Raw context cache successfully updated for session ID: %s", sessionID)
	return nil
}

func (r *RedisContextStorage) DeleteSessionContext(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Infof("Redis: DeleteSessionContext for session %s took %s", sessionID, time.Since(startTime))
	}()

	ctx := context.Background()
	tokenCacheKey := "ctx_" + sessionID
	rawCacheKey := "raw_ctx_" + sessionID
	log.Infof("Redis: Attempting to delete context for session ID: %s from cache keys: %s, %s", sessionID, tokenCacheKey, rawCacheKey)

	redisDelStartTime := time.Now()
	err := r.client.Del(ctx, tokenCacheKey, rawCacheKey).Err()
	log.Debugf("Redis: DEL for session %s took %s", sessionID, time.Since(redisDelStartTime))
	if err != nil {
		log.Errorf("Redis: Failed to delete context from Redis for session ID %s: %v", sessionID, err)
		return fmt.Errorf("failed to delete redis keys for session %s: %w", sessionID, err)
	}

	log.Infof("Redis: Successfully deleted context from Redis for session ID: %s", sessionID)
	return nil
}

// IsNotFoundError checks if the error is redis.Nil, indicating a cache miss.
func (r *RedisContextStorage) IsNotFoundError(err error) bool {
	return err == redis.Nil
}
