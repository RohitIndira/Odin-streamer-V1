package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// setupHistoricalAPIEndpoints sets up all historical and continuous data API endpoints
func (ms *MarketService) setupHistoricalAPIEndpoints() {
	// Historical data endpoint
	// GET /api/historical/{symbol}?from={timestamp}&to={timestamp}&interval={1MIN|5MIN}
	http.HandleFunc("/api/historical/", ms.handleHistoricalData)

	// Continuous series endpoint (historical + live merged)
	// GET /api/continuous/{symbol}?from={timestamp}&interval={1MIN}&period={1D|1W|1M}
	http.HandleFunc("/api/continuous/", ms.handleContinuousData)

	// Chart data endpoint (optimized for charting libraries)
	// GET /api/chart/{symbol}?period={1D|1W|1M}&interval={1MIN|5MIN}
	http.HandleFunc("/api/chart/", ms.handleChartData)

	// Historical cache management
	http.HandleFunc("/api/historical/cache/stats", ms.handleHistoricalCacheStats)
	http.HandleFunc("/api/historical/cache/clear", ms.handleHistoricalCacheClear)

	// Backfill endpoint for filling gaps
	http.HandleFunc("/api/historical/backfill/", ms.handleBackfillData)

	log.Printf("✅ Historical API endpoints registered:")
	log.Printf("  GET  /api/historical/{symbol} - Get historical data")
	log.Printf("  GET  /api/continuous/{symbol} - Get continuous series (historical + live)")
	log.Printf("  GET  /api/chart/{symbol} - Get chart data")
	log.Printf("  GET  /api/historical/cache/stats - Get cache statistics")
	log.Printf("  POST /api/historical/cache/clear - Clear cache")
	log.Printf("  POST /api/historical/backfill/{symbol} - Backfill missing data")
}

// handleHistoricalData handles requests for pure historical data
func (ms *MarketService) handleHistoricalData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract symbol from URL path
	symbol := strings.TrimPrefix(r.URL.Path, "/api/historical/")
	if symbol == "" {
		http.Error(w, "Symbol required", http.StatusBadRequest)
		return
	}
	symbol = strings.ToUpper(symbol)

	// Parse query parameters
	query := r.URL.Query()
	interval := query.Get("interval")
	if interval == "" {
		interval = "1MIN" // Default to 1 minute
	}

	// Parse time range
	fromStr := query.Get("from")
	toStr := query.Get("to")

	var fromTime, toTime time.Time
	var err error

	if fromStr != "" {
		if fromTimestamp, err := strconv.ParseInt(fromStr, 10, 64); err == nil {
			fromTime = time.Unix(fromTimestamp/1000, 0) // Assume milliseconds
		} else {
			fromTime, err = time.Parse("2006-01-02 15:04:05", fromStr)
			if err != nil {
				http.Error(w, "Invalid from time format", http.StatusBadRequest)
				return
			}
		}
	} else {
		// Default to last 24 hours
		fromTime = time.Now().AddDate(0, 0, -1)
	}

	if toStr != "" {
		if toTimestamp, err := strconv.ParseInt(toStr, 10, 64); err == nil {
			toTime = time.Unix(toTimestamp/1000, 0) // Assume milliseconds
		} else {
			toTime, err = time.Parse("2006-01-02 15:04:05", toStr)
			if err != nil {
				http.Error(w, "Invalid to time format", http.StatusBadRequest)
				return
			}
		}
	} else {
		toTime = time.Now()
	}

	// Get historical data
	candles, err := ms.historicalManager.GetHistoricalData(symbol, interval, fromTime, toTime)
	if err != nil {
		log.Printf("❌ Error getting historical data for %s: %v", symbol, err)
		http.Error(w, fmt.Sprintf("Failed to get historical data: %v", err), http.StatusInternalServerError)
		return
	}

	// Prepare response
	response := map[string]interface{}{
		"symbol":      symbol,
		"interval":    interval,
		"from":        fromTime.Unix() * 1000,
		"to":          toTime.Unix() * 1000,
		"count":       len(candles),
		"data":        candles,
		"data_source": "HISTORICAL",
		"timestamp":   time.Now().Unix() * 1000,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	log.Printf("📊 Served %d historical candles for %s (%s)", len(candles), symbol, interval)
}

// handleContinuousData handles requests for continuous series (historical + live)
func (ms *MarketService) handleContinuousData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract symbol from URL path
	symbol := strings.TrimPrefix(r.URL.Path, "/api/continuous/")
	if symbol == "" {
		http.Error(w, "Symbol required", http.StatusBadRequest)
		return
	}
	symbol = strings.ToUpper(symbol)

	// Parse query parameters
	query := r.URL.Query()
	interval := query.Get("interval")
	if interval == "" {
		interval = "1MIN" // Default to 1 minute
	}

	period := query.Get("period")
	fromStr := query.Get("from")

	var fromTime time.Time
	var err error

	// Determine time range based on period or from parameter
	if fromStr != "" {
		if fromTimestamp, err := strconv.ParseInt(fromStr, 10, 64); err == nil {
			fromTime = time.Unix(fromTimestamp/1000, 0) // Assume milliseconds
		} else {
			fromTime, err = time.Parse("2006-01-02 15:04:05", fromStr)
			if err != nil {
				http.Error(w, "Invalid from time format", http.StatusBadRequest)
				return
			}
		}
	} else {
		// Use period to determine from time
		switch strings.ToUpper(period) {
		case "1D":
			fromTime = time.Now().AddDate(0, 0, -1)
		case "1W":
			fromTime = time.Now().AddDate(0, 0, -7)
		case "1M":
			fromTime = time.Now().AddDate(0, -1, 0)
		default:
			fromTime = time.Now().AddDate(0, 0, -1) // Default to 1 day
		}
	}

	// Get continuous series data
	series, err := ms.historicalManager.GetContinuousSeriesData(symbol, interval, fromTime)
	if err != nil {
		log.Printf("❌ Error getting continuous series for %s: %v", symbol, err)
		http.Error(w, fmt.Sprintf("Failed to get continuous series: %v", err), http.StatusInternalServerError)
		return
	}

	// Prepare response
	response := map[string]interface{}{
		"symbol":           symbol,
		"interval":         interval,
		"period":           period,
		"from":             fromTime.Unix() * 1000,
		"total_candles":    series.TotalCandles,
		"historical_count": series.HistoricalCount,
		"live_count":       series.LiveCount,
		"has_gaps":         series.HasGaps,
		"is_complete":      series.IsComplete,
		"data":             series.Data,
		"last_update":      series.LastUpdate.Unix() * 1000,
		"data_source":      "CONTINUOUS",
		"timestamp":        time.Now().Unix() * 1000,
	}

	if !series.HistoricalEnd.IsZero() {
		response["historical_end"] = series.HistoricalEnd.Unix() * 1000
	}
	if !series.LiveStart.IsZero() {
		response["live_start"] = series.LiveStart.Unix() * 1000
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	log.Printf("📈 Served continuous series for %s: %d historical + %d live = %d total candles",
		symbol, series.HistoricalCount, series.LiveCount, series.TotalCandles)
}

// handleChartData handles requests optimized for charting libraries
func (ms *MarketService) handleChartData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract symbol from URL path
	symbol := strings.TrimPrefix(r.URL.Path, "/api/chart/")
	if symbol == "" {
		http.Error(w, "Symbol required", http.StatusBadRequest)
		return
	}
	symbol = strings.ToUpper(symbol)

	// Parse query parameters
	query := r.URL.Query()
	interval := query.Get("interval")
	if interval == "" {
		interval = "1MIN"
	}

	period := query.Get("period")
	if period == "" {
		period = "1D"
	}

	// Determine time range
	var fromTime time.Time
	switch strings.ToUpper(period) {
	case "1D":
		fromTime = time.Now().AddDate(0, 0, -1)
	case "1W":
		fromTime = time.Now().AddDate(0, 0, -7)
	case "1M":
		fromTime = time.Now().AddDate(0, -1, 0)
	case "3M":
		fromTime = time.Now().AddDate(0, -3, 0)
	case "6M":
		fromTime = time.Now().AddDate(0, -6, 0)
	case "1Y":
		fromTime = time.Now().AddDate(-1, 0, 0)
	default:
		fromTime = time.Now().AddDate(0, 0, -1)
	}

	// Get continuous series data
	series, err := ms.historicalManager.GetContinuousSeriesData(symbol, interval, fromTime)
	if err != nil {
		log.Printf("❌ Error getting chart data for %s: %v", symbol, err)
		http.Error(w, fmt.Sprintf("Failed to get chart data: %v", err), http.StatusInternalServerError)
		return
	}

	// Format data for charting libraries (TradingView format)
	chartData := make([]map[string]interface{}, len(series.Data))
	for i, candle := range series.Data {
		chartData[i] = map[string]interface{}{
			"time":   candle.Timestamp,
			"open":   candle.Open,
			"high":   candle.High,
			"low":    candle.Low,
			"close":  candle.Close,
			"volume": candle.Volume,
			"source": candle.Source,
		}
	}

	// Prepare chart-optimized response
	response := map[string]interface{}{
		"s":      "ok", // TradingView status
		"symbol": symbol,
		"meta": map[string]interface{}{
			"interval":         interval,
			"period":           period,
			"total_candles":    series.TotalCandles,
			"historical_count": series.HistoricalCount,
			"live_count":       series.LiveCount,
			"has_gaps":         series.HasGaps,
			"exchange":         series.Exchange,
			"token":            series.Token,
		},
		"data":      chartData,
		"timestamp": time.Now().Unix() * 1000,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Allow CORS for charting
	json.NewEncoder(w).Encode(response)

	log.Printf("📊 Served chart data for %s: %d candles (%s, %s)",
		symbol, len(chartData), interval, period)
}

// handleHistoricalCacheStats returns cache statistics
func (ms *MarketService) handleHistoricalCacheStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := ms.historicalManager.GetCacheStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"stats":  stats,
	})
}

// handleHistoricalCacheClear clears the historical cache
func (ms *MarketService) handleHistoricalCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ms.historicalManager.ClearCache()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Historical cache cleared successfully",
	})

	log.Printf("🧹 Historical cache cleared via API")
}

// handleBackfillData handles backfilling missing historical data
func (ms *MarketService) handleBackfillData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract symbol from URL path
	symbol := strings.TrimPrefix(r.URL.Path, "/api/historical/backfill/")
	if symbol == "" {
		http.Error(w, "Symbol required", http.StatusBadRequest)
		return
	}
	symbol = strings.ToUpper(symbol)

	// Parse request body for backfill parameters
	var request struct {
		Interval string `json:"interval"`
		FromTime string `json:"from_time"`
		ToTime   string `json:"to_time"`
		Force    bool   `json:"force"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if request.Interval == "" {
		request.Interval = "1MIN"
	}

	// Parse time range
	fromTime, err := time.Parse("2006-01-02 15:04:05", request.FromTime)
	if err != nil {
		http.Error(w, "Invalid from_time format", http.StatusBadRequest)
		return
	}

	toTime, err := time.Parse("2006-01-02 15:04:05", request.ToTime)
	if err != nil {
		http.Error(w, "Invalid to_time format", http.StatusBadRequest)
		return
	}

	// Get token and exchange for symbol
	token, exists := ms.stockDatabase.GetTokenForSymbol(symbol)
	if !exists {
		http.Error(w, "Symbol not found", http.StatusNotFound)
		return
	}

	_, exchange, exists := ms.stockDatabase.GetSymbolForToken(token)
	if !exists {
		http.Error(w, "Token mapping not found", http.StatusNotFound)
		return
	}

	// Fetch and store historical data
	candles, err := ms.historicalManager.FetchHistoricalData(symbol, token, exchange, request.Interval, fromTime, toTime)
	if err != nil {
		log.Printf("❌ Error backfilling data for %s: %v", symbol, err)
		http.Error(w, fmt.Sprintf("Failed to backfill data: %v", err), http.StatusInternalServerError)
		return
	}

	// Store the data
	if err := ms.historicalManager.StoreHistoricalData(candles, request.Interval); err != nil {
		log.Printf("❌ Error storing backfilled data for %s: %v", symbol, err)
		http.Error(w, fmt.Sprintf("Failed to store backfilled data: %v", err), http.StatusInternalServerError)
		return
	}

	// Clear cache to force refresh
	ms.historicalManager.ClearCache()

	response := map[string]interface{}{
		"status":        "success",
		"message":       "Data backfilled successfully",
		"symbol":        symbol,
		"interval":      request.Interval,
		"from_time":     request.FromTime,
		"to_time":       request.ToTime,
		"candles_added": len(candles),
		"backfilled_at": time.Now().Unix() * 1000,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	log.Printf("🔄 Backfilled %d candles for %s (%s) from %s to %s",
		len(candles), symbol, request.Interval, request.FromTime, request.ToTime)
}
