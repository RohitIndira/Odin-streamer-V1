package tick

import (
	"fmt"
	"log"
	"time"

	"golang-market-service/internal/candle"
)

// MarketData represents the existing market data structure from main.go
type MarketData struct {
	Symbol        string  `json:"symbol"`
	Token         string  `json:"token"`
	LTP           float64 `json:"ltp"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Open          float64 `json:"open"`
	Close         float64 `json:"close"`
	Volume        int64   `json:"volume"`
	PercentChange float64 `json:"change"`
	Week52High    float64 `json:"week_52_high"`
	Week52Low     float64 `json:"week_52_low"`
	PrevClose     float64 `json:"prev_close"`
	AvgVolume5D   int64   `json:"avg_volume_5d"`
	Timestamp     int64   `json:"timestamp"`
}

// TickNormalizer handles normalization and deduplication of incoming ticks
type TickNormalizer struct {
	istLocation *time.Location

	// Deduplication tracking
	lastTicks map[string]*NormalizedTick

	// Statistics
	totalTicks      int64
	normalizedTicks int64
	duplicateTicks  int64
	errorTicks      int64
}

// NormalizedTick represents a normalized tick ready for candle processing
type NormalizedTick struct {
	Exchange   string
	ScripToken string
	Symbol     string
	Price      float64
	Volume     int64
	Timestamp  time.Time
	Hash       string // For deduplication
}

// NewTickNormalizer creates a new tick normalizer
func NewTickNormalizer() (*TickNormalizer, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	normalizer := &TickNormalizer{
		istLocation: istLocation,
		lastTicks:   make(map[string]*NormalizedTick),
	}

	log.Printf("✅ Tick Normalizer initialized with IST timezone")
	return normalizer, nil
}

// NormalizeMarketData converts MarketData to normalized tick format
func (tn *TickNormalizer) NormalizeMarketData(data MarketData) (*candle.Tick, error) {
	tn.totalTicks++

	// Validate required fields
	if data.Token == "" {
		tn.errorTicks++
		return nil, fmt.Errorf("missing token in market data")
	}

	if data.Symbol == "" {
		tn.errorTicks++
		return nil, fmt.Errorf("missing symbol in market data")
	}

	if data.LTP <= 0 {
		tn.errorTicks++
		return nil, fmt.Errorf("invalid LTP: %f", data.LTP)
	}

	// Convert timestamp to time.Time in IST
	var tickTime time.Time
	if data.Timestamp > 0 {
		// Assume timestamp is in milliseconds
		tickTime = time.Unix(data.Timestamp/1000, (data.Timestamp%1000)*1000000).In(tn.istLocation)
	} else {
		// Use current time if timestamp is missing
		tickTime = time.Now().In(tn.istLocation)
	}

	// Determine exchange from token or symbol patterns
	exchange := tn.determineExchange(data.Token, data.Symbol)

	// Create normalized tick
	normalizedTick := &NormalizedTick{
		Exchange:   exchange,
		ScripToken: data.Token,
		Symbol:     data.Symbol,
		Price:      data.LTP,
		Volume:     data.Volume,
		Timestamp:  tickTime,
		Hash:       tn.generateTickHash(data.Token, data.LTP, data.Volume, tickTime),
	}

	// Check for duplicates
	if tn.isDuplicate(normalizedTick) {
		tn.duplicateTicks++
		return nil, nil // Return nil for duplicates (not an error)
	}

	// Store for deduplication
	tn.lastTicks[data.Token] = normalizedTick
	tn.normalizedTicks++

	// Convert to candle.Tick format
	candleTick := &candle.Tick{
		Exchange:   normalizedTick.Exchange,
		ScripToken: normalizedTick.ScripToken,
		Symbol:     normalizedTick.Symbol,
		Price:      normalizedTick.Price,
		Volume:     normalizedTick.Volume,
		Timestamp:  normalizedTick.Timestamp,
	}

	return candleTick, nil
}

// determineExchange determines the exchange based on token and symbol patterns
func (tn *TickNormalizer) determineExchange(token, symbol string) string {
	// Default logic - can be enhanced based on token patterns
	// For now, assume NSE for most tokens, BSE for specific patterns

	// BSE tokens are typically longer numeric strings
	if len(token) >= 6 {
		return "BSE"
	}

	// NSE is default
	return "NSE"
}

// generateTickHash creates a hash for deduplication
func (tn *TickNormalizer) generateTickHash(token string, price float64, volume int64, timestamp time.Time) string {
	// Simple hash based on token, price, volume, and minute
	minuteTimestamp := timestamp.Truncate(time.Minute).Unix()
	return fmt.Sprintf("%s_%.4f_%d_%d", token, price, volume, minuteTimestamp)
}

// isDuplicate checks if the tick is a duplicate of the last processed tick
func (tn *TickNormalizer) isDuplicate(tick *NormalizedTick) bool {
	lastTick, exists := tn.lastTicks[tick.ScripToken]
	if !exists {
		return false
	}

	// Check if hash matches (same token, price, volume within same minute)
	return lastTick.Hash == tick.Hash
}

// GetStats returns normalizer statistics
func (tn *TickNormalizer) GetStats() map[string]interface{} {
	duplicateRate := float64(0)
	errorRate := float64(0)

	if tn.totalTicks > 0 {
		duplicateRate = float64(tn.duplicateTicks) / float64(tn.totalTicks) * 100
		errorRate = float64(tn.errorTicks) / float64(tn.totalTicks) * 100
	}

	return map[string]interface{}{
		"total_ticks":      tn.totalTicks,
		"normalized_ticks": tn.normalizedTicks,
		"duplicate_ticks":  tn.duplicateTicks,
		"error_ticks":      tn.errorTicks,
		"duplicate_rate":   fmt.Sprintf("%.2f%%", duplicateRate),
		"error_rate":       fmt.Sprintf("%.2f%%", errorRate),
		"success_rate":     fmt.Sprintf("%.2f%%", 100-duplicateRate-errorRate),
		"timezone":         tn.istLocation.String(),
	}
}

// ClearDeduplicationCache clears the deduplication cache
func (tn *TickNormalizer) ClearDeduplicationCache() {
	tn.lastTicks = make(map[string]*NormalizedTick)
	log.Printf("🧹 Tick deduplication cache cleared")
}

// ValidateMarketHours checks if the tick timestamp is within market hours
func (tn *TickNormalizer) ValidateMarketHours(timestamp time.Time) bool {
	istTime := timestamp.In(tn.istLocation)

	// Market hours: 09:15 - 15:30 IST
	marketOpen := 9*time.Hour + 15*time.Minute
	marketClose := 15*time.Hour + 30*time.Minute

	timeOfDay := time.Duration(istTime.Hour())*time.Hour +
		time.Duration(istTime.Minute())*time.Minute +
		time.Duration(istTime.Second())*time.Second

	return timeOfDay >= marketOpen && timeOfDay <= marketClose
}

// ProcessBatch processes multiple market data items in batch
func (tn *TickNormalizer) ProcessBatch(dataItems []MarketData) ([]*candle.Tick, []error) {
	var normalizedTicks []*candle.Tick
	var errors []error

	for _, data := range dataItems {
		tick, err := tn.NormalizeMarketData(data)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		if tick != nil { // Skip duplicates (nil return)
			normalizedTicks = append(normalizedTicks, tick)
		}
	}

	return normalizedTicks, errors
}

// Reset resets all statistics and caches
func (tn *TickNormalizer) Reset() {
	tn.totalTicks = 0
	tn.normalizedTicks = 0
	tn.duplicateTicks = 0
	tn.errorTicks = 0
	tn.lastTicks = make(map[string]*NormalizedTick)

	log.Printf("🔄 Tick Normalizer reset complete")
}
