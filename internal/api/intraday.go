package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang-market-service/internal/candle"
	"golang-market-service/internal/historical"
	"golang-market-service/internal/storage"
)

// IntradayAPI handles intraday candlestick API endpoints
type IntradayAPI struct {
	redisAdapter     *storage.RedisAdapter
	timescaleAdapter *storage.TimescaleDBAdapter
	historicalClient *historical.IndiraTradeClient
	istLocation      *time.Location
}

// IntradayResponse represents the standard intraday API response
type IntradayResponse struct {
	Success      bool            `json:"success"`
	Exchange     string          `json:"exchange"`
	ScripToken   string          `json:"scrip_token"`
	Symbol       string          `json:"symbol"`
	Date         string          `json:"date"`
	MarketOpen   string          `json:"market_open"`
	MarketClose  string          `json:"market_close"`
	TotalCandles int             `json:"total_candles"`
	DataSource   string          `json:"data_source"`
	Candles      []candle.Candle `json:"candles"`
	Metadata     Metadata        `json:"metadata"`
	Timestamp    int64           `json:"timestamp"`
}

// Metadata contains additional information about the data
type Metadata struct {
	CacheHit         bool    `json:"cache_hit"`
	HistoricalCount  int     `json:"historical_count"`
	SyntheticCount   int     `json:"synthetic_count"`
	RealtimeCount    int     `json:"realtime_count"`
	HasGaps          bool    `json:"has_gaps"`
	PreviousDayClose float64 `json:"previous_day_close,omitempty"`
	ProcessingTimeMs int64   `json:"processing_time_ms"`
}

// NewIntradayAPI creates a new intraday API handler
func NewIntradayAPI(redisAdapter *storage.RedisAdapter, timescaleAdapter *storage.TimescaleDBAdapter, historicalClient *historical.IndiraTradeClient) (*IntradayAPI, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	api := &IntradayAPI{
		redisAdapter:     redisAdapter,
		timescaleAdapter: timescaleAdapter,
		historicalClient: historicalClient,
		istLocation:      istLocation,
	}

	log.Printf("✅ Intraday API initialized")
	return api, nil
}

// HandleIntradayRequest handles GET /intraday/{exchange}/{scrip_token}/{symbol}
func (api *IntradayAPI) HandleIntradayRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Parse URL path: /intraday/{exchange}/{scrip_token}/{symbol}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		api.sendErrorResponse(w, "Invalid URL format. Expected: /intraday/{exchange}/{scrip_token}/{symbol}", http.StatusBadRequest)
		return
	}

	exchange := strings.ToUpper(pathParts[1])
	scripToken := pathParts[2]
	symbol := strings.ToUpper(pathParts[3])

	// Parse query parameters
	query := r.URL.Query()
	dateStr := query.Get("date")
	forceRefresh := query.Get("force_refresh") == "true"
	includeMetadata := query.Get("include_metadata") != "false" // Default true

	// Always use current date (IST) - auto-detect current trading day
	now := time.Now().In(api.istLocation)
	targetDate := now

	// If date parameter is provided, use it (for historical data)
	if dateStr != "" {
		var err error
		targetDate, err = time.ParseInLocation("2006-01-02", dateStr, api.istLocation)
		if err != nil {
			api.sendErrorResponse(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
	}

	log.Printf("📊 Intraday request: %s:%s:%s for %s (auto-current: %v, force_refresh: %v)",
		exchange, scripToken, symbol, targetDate.Format("2006-01-02"), dateStr == "", forceRefresh)

	var response *IntradayResponse
	var err error

	if !forceRefresh {
		// Try Redis cache first
		response, err = api.getFromCache(exchange, scripToken, symbol, targetDate)
		if err != nil {
			log.Printf("⚠️ Cache error: %v", err)
		}
	}

	if response == nil {
		// Cache miss or force refresh - fetch from historical API
		response, err = api.fetchFromHistoricalAPI(exchange, scripToken, symbol, targetDate)
		if err != nil {
			api.sendErrorResponse(w, fmt.Sprintf("Failed to fetch intraday data: %v", err), http.StatusInternalServerError)
			return
		}

		// Store in cache for future requests
		if api.redisAdapter != nil {
			if err := api.storeInCache(response, targetDate); err != nil {
				log.Printf("⚠️ Failed to store in cache: %v", err)
			}
		}
	}

	// Update metadata
	if includeMetadata {
		response.Metadata.ProcessingTimeMs = time.Since(startTime).Milliseconds()
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode response: %v", err)
		return
	}

	log.Printf("✅ Served intraday data for %s:%s (%d candles, %dms)",
		exchange, scripToken, response.TotalCandles, response.Metadata.ProcessingTimeMs)
}

// getFromCache retrieves intraday data from Redis cache
func (api *IntradayAPI) getFromCache(exchange, scripToken, symbol string, date time.Time) (*IntradayResponse, error) {
	if api.redisAdapter == nil {
		return nil, fmt.Errorf("Redis adapter not available")
	}

	cache, err := api.redisAdapter.GetDayCandles(exchange, scripToken, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get from cache: %w", err)
	}

	if cache == nil {
		return nil, nil // Cache miss
	}

	// Convert cache to response format
	response := &IntradayResponse{
		Success:      true,
		Exchange:     exchange,
		ScripToken:   scripToken,
		Symbol:       symbol,
		Date:         date.Format("2006-01-02"),
		MarketOpen:   cache.MarketOpen.Format("2006-01-02T15:04:05-07:00"),
		MarketClose:  cache.MarketClose.Format("2006-01-02T15:04:05-07:00"),
		TotalCandles: len(cache.Candles),
		DataSource:   "redis_cache",
		Candles:      cache.Candles,
		Metadata: Metadata{
			CacheHit: true,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	// Calculate metadata statistics
	api.calculateMetadata(response)

	log.Printf("📦 Cache hit for %s:%s on %s (%d candles)",
		exchange, scripToken, date.Format("2006-01-02"), len(cache.Candles))

	return response, nil
}

// fetchFromHistoricalAPI fetches data from IndiraTrade API with fill algorithm
func (api *IntradayAPI) fetchFromHistoricalAPI(exchange, scripToken, symbol string, date time.Time) (*IntradayResponse, error) {
	if api.historicalClient == nil {
		return nil, fmt.Errorf("historical client not available")
	}

	// Fetch and fill intraday data
	result, err := api.historicalClient.FetchAndFillIntradayData(exchange, scripToken, symbol, date)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	// Create market hours for the date
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, api.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, api.istLocation)

	// Convert to response format
	response := &IntradayResponse{
		Success:      true,
		Exchange:     exchange,
		ScripToken:   scripToken,
		Symbol:       symbol,
		Date:         date.Format("2006-01-02"),
		MarketOpen:   marketOpen.Format("2006-01-02T15:04:05-07:00"),
		MarketClose:  marketClose.Format("2006-01-02T15:04:05-07:00"),
		TotalCandles: len(result.Candles),
		DataSource:   "historical_api",
		Candles:      result.Candles,
		Metadata: Metadata{
			CacheHit:         false,
			HistoricalCount:  result.ProvidedMinutes,
			SyntheticCount:   result.SyntheticMinutes,
			HasGaps:          result.HasGaps,
			PreviousDayClose: result.PreviousDayClose,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	log.Printf("📈 Fetched from historical API for %s:%s (%d total, %d synthetic)",
		exchange, scripToken, result.TotalMinutes, result.SyntheticMinutes)

	return response, nil
}

// storeInCache stores the response data in Redis cache
func (api *IntradayAPI) storeInCache(response *IntradayResponse, date time.Time) error {
	if api.redisAdapter == nil {
		return fmt.Errorf("Redis adapter not available")
	}

	return api.redisAdapter.StoreDayCandles(response.Exchange, response.ScripToken, date, response.Candles)
}

// calculateMetadata calculates metadata statistics from candles
func (api *IntradayAPI) calculateMetadata(response *IntradayResponse) {
	var historicalCount, syntheticCount, realtimeCount int

	for _, candleData := range response.Candles {
		switch candleData.Source {
		case "historical_api":
			historicalCount++
		case "synthetic":
			syntheticCount++
		case "realtime":
			realtimeCount++
		}
	}

	response.Metadata.HistoricalCount = historicalCount
	response.Metadata.SyntheticCount = syntheticCount
	response.Metadata.RealtimeCount = realtimeCount
	response.Metadata.HasGaps = syntheticCount > 0
}

// getSymbolFromToken gets symbol from token (implement based on your stock database)
func (api *IntradayAPI) getSymbolFromToken(token string) string {
	// This should be implemented to lookup symbol from your stock database
	// For now, return empty string to use token as fallback
	return ""
}

// sendErrorResponse sends a standardized error response
func (api *IntradayAPI) sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(statusCode)

	errorResponse := map[string]interface{}{
		"success":   false,
		"error":     message,
		"timestamp": time.Now().UnixMilli(),
	}

	json.NewEncoder(w).Encode(errorResponse)
	log.Printf("❌ API Error (%d): %s", statusCode, message)
}

// HandleIntradayStats handles GET /intraday/stats
func (api *IntradayAPI) HandleIntradayStats(w http.ResponseWriter, r *http.Request) {
	stats := make(map[string]interface{})

	// Redis stats
	if api.redisAdapter != nil {
		redisStats, err := api.redisAdapter.GetCacheStats()
		if err != nil {
			log.Printf("⚠️ Failed to get Redis stats: %v", err)
		} else {
			stats["redis"] = redisStats
		}
	}

	// TimescaleDB stats
	if api.timescaleAdapter != nil {
		tsStats, err := api.timescaleAdapter.GetCandleStats()
		if err != nil {
			log.Printf("⚠️ Failed to get TimescaleDB stats: %v", err)
		} else {
			stats["timescaledb"] = tsStats
		}
	}

	// Historical client stats
	if api.historicalClient != nil {
		stats["historical_client"] = api.historicalClient.GetStats()
	}

	// API stats
	stats["api"] = map[string]interface{}{
		"timezone":     api.istLocation.String(),
		"market_open":  "09:15 IST",
		"market_close": "15:30 IST",
		"endpoints": []string{
			"GET /intraday/{exchange}/{scrip_token}",
			"GET /intraday/stats",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"stats":     stats,
		"timestamp": time.Now().UnixMilli(),
	})
}

// HandleIntradayHealth handles GET /intraday/health
func (api *IntradayAPI) HandleIntradayHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status": "healthy",
		"components": map[string]string{
			"redis":      "unknown",
			"timescale":  "unknown",
			"historical": "available",
		},
	}

	// Check Redis health
	if api.redisAdapter != nil {
		if err := api.redisAdapter.Ping(); err != nil {
			health["components"].(map[string]string)["redis"] = "unhealthy"
			health["status"] = "degraded"
		} else {
			health["components"].(map[string]string)["redis"] = "healthy"
		}
	}

	// Check TimescaleDB health
	if api.timescaleAdapter != nil {
		if err := api.timescaleAdapter.Ping(); err != nil {
			health["components"].(map[string]string)["timescale"] = "unhealthy"
			health["status"] = "degraded"
		} else {
			health["components"].(map[string]string)["timescale"] = "healthy"
		}
	}

	statusCode := http.StatusOK
	if health["status"] == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(health)
}

// RegisterRoutes registers all intraday API routes
func (api *IntradayAPI) RegisterRoutes() {
	http.HandleFunc("/intraday/", api.HandleIntradayRequest)
	http.HandleFunc("/intraday/stats", api.HandleIntradayStats)
	http.HandleFunc("/intraday/health", api.HandleIntradayHealth)

	log.Printf("✅ Intraday API routes registered:")
	log.Printf("  GET /intraday/{exchange}/{scrip_token} - Get intraday candles")
	log.Printf("  GET /intraday/stats - Get API statistics")
	log.Printf("  GET /intraday/health - Get health status")
}

// SetSymbolLookup sets a function to lookup symbols from tokens
func (api *IntradayAPI) SetSymbolLookup(lookupFunc func(string) string) {
	// Store the lookup function for use in getSymbolFromToken
	// This is a placeholder - implement based on your needs
}
