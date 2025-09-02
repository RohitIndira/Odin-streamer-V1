package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"golang-market-service/internal/candle"

	"github.com/go-redis/redis/v8"
)

// RedisAdapter handles Redis operations for candle caching and pub/sub
type RedisAdapter struct {
	client      *redis.Client
	ctx         context.Context
	istLocation *time.Location
}

// CandleCache represents cached candle data structure
type CandleCache struct {
	Exchange     string          `json:"exchange"`
	ScripToken   string          `json:"scrip_token"`
	Date         string          `json:"date"`    // YYYYMMDD format
	Candles      []candle.Candle `json:"candles"` // Full day minute candles
	LastUpdated  time.Time       `json:"last_updated"`
	TotalCandles int             `json:"total_candles"`
	MarketOpen   time.Time       `json:"market_open"`  // 09:15 IST for the date
	MarketClose  time.Time       `json:"market_close"` // 15:30 IST for the date
}

// LatestCandleCache represents the latest in-flight candle
type LatestCandleCache struct {
	Exchange    string        `json:"exchange"`
	ScripToken  string        `json:"scrip_token"`
	Candle      candle.Candle `json:"candle"`
	LastUpdated time.Time     `json:"last_updated"`
	IsComplete  bool          `json:"is_complete"` // true if candle is finalized
}

// NewRedisAdapter creates a new Redis adapter
func NewRedisAdapter(redisURL string) (*RedisAdapter, error) {
	// Parse Redis URL or use default options
	var rdb *redis.Client

	if redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
		}
		rdb = redis.NewClient(opt)
	} else {
		// Default Redis configuration
		rdb = redis.NewClient(&redis.Options{
			Addr:     "localhost:6379",
			Password: "",
			DB:       0,
		})
	}

	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	adapter := &RedisAdapter{
		client:      rdb,
		ctx:         context.Background(),
		istLocation: istLocation,
	}

	// Test connection
	if err := adapter.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Printf("✅ Redis adapter initialized successfully")
	return adapter, nil
}

// Ping tests the Redis connection
func (ra *RedisAdapter) Ping() error {
	_, err := ra.client.Ping(ra.ctx).Result()
	return err
}

// StoreDayCandles stores full day candles for a token
func (ra *RedisAdapter) StoreDayCandles(exchange, scripToken string, date time.Time, candles []candle.Candle) error {
	dateStr := date.In(ra.istLocation).Format("20060102")
	key := fmt.Sprintf("candles:%s:%s:%s", exchange, scripToken, dateStr)

	// Create market open/close times for the date
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, ra.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, ra.istLocation)

	cache := CandleCache{
		Exchange:     exchange,
		ScripToken:   scripToken,
		Date:         dateStr,
		Candles:      candles,
		LastUpdated:  time.Now().In(ra.istLocation),
		TotalCandles: len(candles),
		MarketOpen:   marketOpen,
		MarketClose:  marketClose,
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("failed to marshal candle cache: %w", err)
	}

	// Store with expiration (keep for 7 days)
	expiration := 7 * 24 * time.Hour
	if err := ra.client.Set(ra.ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("failed to store day candles: %w", err)
	}

	log.Printf("📦 Stored %d candles for %s:%s on %s", len(candles), exchange, scripToken, dateStr)
	return nil
}

// GetDayCandles retrieves full day candles for a token
func (ra *RedisAdapter) GetDayCandles(exchange, scripToken string, date time.Time) (*CandleCache, error) {
	dateStr := date.In(ra.istLocation).Format("20060102")
	key := fmt.Sprintf("candles:%s:%s:%s", exchange, scripToken, dateStr)

	data, err := ra.client.Get(ra.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Cache miss
		}
		return nil, fmt.Errorf("failed to get day candles: %w", err)
	}

	var cache CandleCache
	if err := json.Unmarshal([]byte(data), &cache); err != nil {
		return nil, fmt.Errorf("failed to unmarshal candle cache: %w", err)
	}

	return &cache, nil
}

// StoreLatestCandle stores the latest in-flight candle
func (ra *RedisAdapter) StoreLatestCandle(candleData candle.Candle, isComplete bool) error {
	key := fmt.Sprintf("intraday_latest:%s:%s", candleData.Exchange, candleData.ScripToken)

	cache := LatestCandleCache{
		Exchange:    candleData.Exchange,
		ScripToken:  candleData.ScripToken,
		Candle:      candleData,
		LastUpdated: time.Now().In(ra.istLocation),
		IsComplete:  isComplete,
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("failed to marshal latest candle: %w", err)
	}

	// Store with short expiration (5 minutes for in-flight candles)
	expiration := 5 * time.Minute
	if err := ra.client.Set(ra.ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("failed to store latest candle: %w", err)
	}

	return nil
}

// GetLatestCandle retrieves the latest in-flight candle
func (ra *RedisAdapter) GetLatestCandle(exchange, scripToken string) (*LatestCandleCache, error) {
	key := fmt.Sprintf("intraday_latest:%s:%s", exchange, scripToken)

	data, err := ra.client.Get(ra.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Cache miss
		}
		return nil, fmt.Errorf("failed to get latest candle: %w", err)
	}

	var cache LatestCandleCache
	if err := json.Unmarshal([]byte(data), &cache); err != nil {
		return nil, fmt.Errorf("failed to unmarshal latest candle: %w", err)
	}

	return &cache, nil
}

// PublishCandleUpdate publishes a candle update to Redis pub/sub
func (ra *RedisAdapter) PublishCandleUpdate(update candle.CandleUpdate) error {
	channel := fmt.Sprintf("intraday_updates:%s:%s", update.Candle.Exchange, update.Candle.ScripToken)

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed to marshal candle update: %w", err)
	}

	if err := ra.client.Publish(ra.ctx, channel, data).Err(); err != nil {
		return fmt.Errorf("failed to publish candle update: %w", err)
	}

	return nil
}

// SubscribeToCandleUpdates subscribes to candle updates for specific tokens
func (ra *RedisAdapter) SubscribeToCandleUpdates(exchange, scripToken string) (*redis.PubSub, error) {
	channel := fmt.Sprintf("intraday_updates:%s:%s", exchange, scripToken)
	pubsub := ra.client.Subscribe(ra.ctx, channel)

	// Test subscription
	_, err := pubsub.Receive(ra.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to candle updates: %w", err)
	}

	log.Printf("📡 Subscribed to candle updates: %s", channel)
	return pubsub, nil
}

// AppendToIntradaySeries appends a candle to the intraday series
func (ra *RedisAdapter) AppendToIntradaySeries(candleData candle.Candle) error {
	dateStr := candleData.MinuteTS.In(ra.istLocation).Format("20060102")
	key := fmt.Sprintf("candles:%s:%s:%s", candleData.Exchange, candleData.ScripToken, dateStr)

	// Get existing cache
	cache, err := ra.GetDayCandles(candleData.Exchange, candleData.ScripToken, candleData.MinuteTS)
	if err != nil {
		return fmt.Errorf("failed to get existing day candles: %w", err)
	}

	if cache == nil {
		// Create new cache with single candle
		cache = &CandleCache{
			Exchange:     candleData.Exchange,
			ScripToken:   candleData.ScripToken,
			Date:         dateStr,
			Candles:      []candle.Candle{candleData},
			LastUpdated:  time.Now().In(ra.istLocation),
			TotalCandles: 1,
			MarketOpen:   time.Date(candleData.MinuteTS.Year(), candleData.MinuteTS.Month(), candleData.MinuteTS.Day(), 9, 15, 0, 0, ra.istLocation),
			MarketClose:  time.Date(candleData.MinuteTS.Year(), candleData.MinuteTS.Month(), candleData.MinuteTS.Day(), 15, 30, 0, 0, ra.istLocation),
		}
	} else {
		// Check if candle already exists (update) or append new
		found := false
		for i, existingCandle := range cache.Candles {
			if existingCandle.MinuteTS.Equal(candleData.MinuteTS) {
				// Update existing candle
				cache.Candles[i] = candleData
				found = true
				break
			}
		}

		if !found {
			// Append new candle and sort by timestamp
			cache.Candles = append(cache.Candles, candleData)
			// Simple sort - in production, consider more efficient sorting
			for i := len(cache.Candles) - 1; i > 0; i-- {
				if cache.Candles[i].MinuteTS.Before(cache.Candles[i-1].MinuteTS) {
					cache.Candles[i], cache.Candles[i-1] = cache.Candles[i-1], cache.Candles[i]
				} else {
					break
				}
			}
		}

		cache.TotalCandles = len(cache.Candles)
		cache.LastUpdated = time.Now().In(ra.istLocation)
	}

	// Store updated cache
	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("failed to marshal updated cache: %w", err)
	}

	expiration := 7 * 24 * time.Hour
	if err := ra.client.Set(ra.ctx, key, data, expiration).Err(); err != nil {
		return fmt.Errorf("failed to store updated cache: %w", err)
	}

	return nil
}

// GetIntradaySeriesFromTime gets intraday series from a specific time
func (ra *RedisAdapter) GetIntradaySeriesFromTime(exchange, scripToken string, fromTime time.Time) ([]candle.Candle, error) {
	cache, err := ra.GetDayCandles(exchange, scripToken, fromTime)
	if err != nil {
		return nil, err
	}

	if cache == nil {
		return []candle.Candle{}, nil // Empty series
	}

	// Filter candles from the specified time
	var filteredCandles []candle.Candle
	for _, candleData := range cache.Candles {
		if candleData.MinuteTS.Equal(fromTime) || candleData.MinuteTS.After(fromTime) {
			filteredCandles = append(filteredCandles, candleData)
		}
	}

	return filteredCandles, nil
}

// ClearDayCache clears the cache for a specific day
func (ra *RedisAdapter) ClearDayCache(exchange, scripToken string, date time.Time) error {
	dateStr := date.In(ra.istLocation).Format("20060102")
	key := fmt.Sprintf("candles:%s:%s:%s", exchange, scripToken, dateStr)

	if err := ra.client.Del(ra.ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to clear day cache: %w", err)
	}

	log.Printf("🧹 Cleared day cache for %s:%s on %s", exchange, scripToken, dateStr)
	return nil
}

// GetCacheStats returns Redis cache statistics
func (ra *RedisAdapter) GetCacheStats() (map[string]interface{}, error) {
	info, err := ra.client.Info(ra.ctx, "memory", "keyspace").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get Redis info: %w", err)
	}

	// Count candle-related keys
	candleKeys, err := ra.client.Keys(ra.ctx, "candles:*").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to count candle keys: %w", err)
	}

	latestKeys, err := ra.client.Keys(ra.ctx, "intraday_latest:*").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to count latest keys: %w", err)
	}

	return map[string]interface{}{
		"candle_cache_keys": len(candleKeys),
		"latest_cache_keys": len(latestKeys),
		"total_cache_keys":  len(candleKeys) + len(latestKeys),
		"redis_info":        info,
		"timezone":          ra.istLocation.String(),
		"connection_status": "connected",
	}, nil
}

// Close closes the Redis connection
func (ra *RedisAdapter) Close() error {
	if err := ra.client.Close(); err != nil {
		return fmt.Errorf("failed to close Redis connection: %w", err)
	}

	log.Printf("✅ Redis adapter closed")
	return nil
}

// FlushAll clears all Redis data (use with caution)
func (ra *RedisAdapter) FlushAll() error {
	if err := ra.client.FlushAll(ra.ctx).Err(); err != nil {
		return fmt.Errorf("failed to flush Redis: %w", err)
	}

	log.Printf("🧹 Redis flushed all data")
	return nil
}

// SetExpiration sets expiration for a specific key
func (ra *RedisAdapter) SetExpiration(key string, expiration time.Duration) error {
	if err := ra.client.Expire(ra.ctx, key, expiration).Err(); err != nil {
		return fmt.Errorf("failed to set expiration: %w", err)
	}

	return nil
}

// BatchStoreDayCandles stores multiple day candles in a pipeline for efficiency
func (ra *RedisAdapter) BatchStoreDayCandles(candleCaches []CandleCache) error {
	pipe := ra.client.Pipeline()

	for _, cache := range candleCaches {
		key := fmt.Sprintf("candles:%s:%s:%s", cache.Exchange, cache.ScripToken, cache.Date)

		data, err := json.Marshal(cache)
		if err != nil {
			return fmt.Errorf("failed to marshal cache for %s: %w", key, err)
		}

		expiration := 7 * 24 * time.Hour
		pipe.Set(ra.ctx, key, data, expiration)
	}

	_, err := pipe.Exec(ra.ctx)
	if err != nil {
		return fmt.Errorf("failed to execute batch store: %w", err)
	}

	log.Printf("📦 Batch stored %d candle caches", len(candleCaches))
	return nil
}
