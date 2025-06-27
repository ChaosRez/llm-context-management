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
