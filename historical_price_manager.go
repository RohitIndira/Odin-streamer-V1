package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// HistoricalCandle represents a single OHLCV candle from your API
type HistoricalCandle struct {
	Symbol    string  `json:"symbol"`
	Open      float64 `json:"O"`
	High      float64 `json:"H"`
	Low       float64 `json:"L"`
	Close     float64 `json:"C"`
	Volume    int64   `json:"V"`
	DateTime  string  `json:"DT"` // "2025-08-25 09:15:00"
	Timestamp int64   `json:"timestamp"`
	Source    string  `json:"source"` // "HISTORICAL" or "LIVE"
	Exchange  string  `json:"exchange"`
	Token     string  `json:"token"`
}

// HistoricalAPIResponse represents your API response structure
type HistoricalAPIResponse struct {
	Status  string             `json:"status"`
	Message string             `json:"message"`
	Data    []HistoricalCandle `json:"data"`
}

// ContinuousSeries represents a merged historical + live data series
type ContinuousSeries struct {
	Symbol          string             `json:"symbol"`
	Token           string             `json:"token"`
	Exchange        string             `json:"exchange"`
	Interval        string             `json:"interval"`
	Data            []HistoricalCandle `json:"data"`
	HistoricalEnd   time.Time          `json:"historical_end"`
	LiveStart       time.Time          `json:"live_start"`
	LastUpdate      time.Time          `json:"last_update"`
	TotalCandles    int                `json:"total_candles"`
	HistoricalCount int                `json:"historical_count"`
	LiveCount       int                `json:"live_count"`
	HasGaps         bool               `json:"has_gaps"`
	IsComplete      bool               `json:"is_complete"`
}

// HistoricalPriceManager manages historical data fetching, caching, and merging
type HistoricalPriceManager struct {
	db            *sql.DB
	baseURL       string
	stockDatabase *StockDatabase
	broadcaster   *ZeroLatencyBroadcaster
	mutex         sync.RWMutex
	cache         map[string]*ContinuousSeries // symbol -> series cache
	lastLiveData  map[string]HistoricalCandle  // symbol -> last live candle
	istLocation   *time.Location
}

// NewHistoricalPriceManager creates a new historical price manager
func NewHistoricalPriceManager(dbPath string, baseURL string, stockDB *StockDatabase, broadcaster *ZeroLatencyBroadcaster) (*HistoricalPriceManager, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open historical database: %w", err)
	}

	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		log.Printf("⚠️ Failed to load IST timezone, using UTC: %v", err)
		istLocation = time.UTC
	}

	hpm := &HistoricalPriceManager{
		db:            db,
		baseURL:       baseURL,
		stockDatabase: stockDB,
		broadcaster:   broadcaster,
		cache:         make(map[string]*ContinuousSeries),
		lastLiveData:  make(map[string]HistoricalCandle),
		istLocation:   istLocation,
	}

	// Create tables
	if err := hpm.createTables(); err != nil {
		return nil, fmt.Errorf("failed to create historical tables: %w", err)
	}

	log.Printf("✅ Historical Price Manager initialized with IST timezone")
	return hpm, nil
}

// createTables creates the necessary database tables for historical data
func (hpm *HistoricalPriceManager) createTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS historical_candles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		symbol TEXT NOT NULL,
		token TEXT NOT NULL,
		exchange TEXT NOT NULL,
		interval_type TEXT NOT NULL,
		open_price REAL NOT NULL,
		high_price REAL NOT NULL,
		low_price REAL NOT NULL,
		close_price REAL NOT NULL,
		volume INTEGER NOT NULL,
		datetime TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		source TEXT DEFAULT 'HISTORICAL',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(symbol, interval_type, timestamp)
	);
	
	CREATE INDEX IF NOT EXISTS idx_historical_symbol_interval ON historical_candles(symbol, interval_type);
	CREATE INDEX IF NOT EXISTS idx_historical_timestamp ON historical_candles(timestamp);
	CREATE INDEX IF NOT EXISTS idx_historical_datetime ON historical_candles(datetime);
	CREATE INDEX IF NOT EXISTS idx_historical_token ON historical_candles(token);
	
	CREATE TABLE IF NOT EXISTS continuous_series_metadata (
		symbol TEXT PRIMARY KEY,
		token TEXT NOT NULL,
		exchange TEXT NOT NULL,
		interval_type TEXT NOT NULL,
		last_historical_timestamp INTEGER,
		last_live_timestamp INTEGER,
		total_candles INTEGER DEFAULT 0,
		historical_count INTEGER DEFAULT 0,
		live_count INTEGER DEFAULT 0,
		has_gaps BOOLEAN DEFAULT FALSE,
		last_updated DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := hpm.db.Exec(query)
	return err
}

// FetchHistoricalData fetches historical data from your API
func (hpm *HistoricalPriceManager) FetchHistoricalData(symbol, token, exchange, interval string, fromTime, toTime time.Time) ([]HistoricalCandle, error) {
	// Map source format for API
	source := "LIVE" // Use LIVE as source for historical API

	// Build API URL based on your format
	// https://trading.indiratrade.com:3000/v1/chart/data/NSE_EQ/1594/1/MIN/AERO?from=2025-08-24%2017:41:34&to=2025-08-25%2014:44:56&countBack=329

	// Format: /v1/chart/data/{EXCHANGE}_EQ/{TOKEN}/1/{INTERVAL}/{SYMBOL}
	// Use the original interval format in the URL path
	urlInterval := interval // Use original interval like "1MIN", "5MIN", "1DAY"
	apiURL := fmt.Sprintf("%s/v1/chart/data/%s_EQ/%s/1/%s/%s",
		hpm.baseURL, exchange, token, urlInterval, symbol)

	// Add query parameters
	params := url.Values{}
	params.Add("from", fromTime.In(hpm.istLocation).Format("2006-01-02 15:04:05"))
	params.Add("to", toTime.In(hpm.istLocation).Format("2006-01-02 15:04:05"))
	params.Add("source", source) // Add source parameter

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	log.Printf("🔄 Fetching historical data: %s", fullURL)

	resp, err := http.Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}
	defer resp.Body.Close()

	var apiResponse HistoricalAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode historical response: %w", err)
	}

	if apiResponse.Status != "success" {
		return nil, fmt.Errorf("API returned error: %s", apiResponse.Message)
	}

	// Process and enrich the data
	var candles []HistoricalCandle
	for _, candle := range apiResponse.Data {
		// Parse datetime and convert to timestamp
		dt, err := time.ParseInLocation("2006-01-02 15:04:05", candle.DateTime, hpm.istLocation)
		if err != nil {
			log.Printf("⚠️ Failed to parse datetime %s: %v", candle.DateTime, err)
			continue
		}

		enrichedCandle := HistoricalCandle{
			Symbol:    symbol,
			Token:     token,
			Exchange:  exchange,
			Open:      candle.Open,
			High:      candle.High,
			Low:       candle.Low,
			Close:     candle.Close,
			Volume:    candle.Volume,
			DateTime:  candle.DateTime,
			Timestamp: dt.Unix() * 1000, // Convert to milliseconds
			Source:    "HISTORICAL",
		}

		candles = append(candles, enrichedCandle)
	}

	log.Printf("✅ Fetched %d historical candles for %s", len(candles), symbol)
	return candles, nil
}

// StoreHistoricalData stores historical candles in SQLite
func (hpm *HistoricalPriceManager) StoreHistoricalData(candles []HistoricalCandle, interval string) error {
	if len(candles) == 0 {
		return nil
	}

	hpm.mutex.Lock()
	defer hpm.mutex.Unlock()

	tx, err := hpm.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO historical_candles 
		(symbol, token, exchange, interval_type, open_price, high_price, low_price, close_price, volume, datetime, timestamp, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, candle := range candles {
		_, err := stmt.Exec(
			candle.Symbol, candle.Token, candle.Exchange, interval,
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume,
			candle.DateTime, candle.Timestamp, candle.Source,
		)
		if err != nil {
			log.Printf("⚠️ Failed to insert candle for %s at %s: %v", candle.Symbol, candle.DateTime, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ Stored %d historical candles in database", len(candles))
	return nil
}

// GetHistoricalData retrieves historical data from cache/database
func (hpm *HistoricalPriceManager) GetHistoricalData(symbol, interval string, fromTime, toTime time.Time) ([]HistoricalCandle, error) {
	hpm.mutex.RLock()
	defer hpm.mutex.RUnlock()

	query := `
		SELECT symbol, token, exchange, open_price, high_price, low_price, close_price, volume, datetime, timestamp, source
		FROM historical_candles 
		WHERE symbol = ? AND interval_type = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC
	`

	rows, err := hpm.db.Query(query, symbol, interval, fromTime.Unix()*1000, toTime.Unix()*1000)
	if err != nil {
		return nil, fmt.Errorf("failed to query historical data: %w", err)
	}
	defer rows.Close()

	var candles []HistoricalCandle
	for rows.Next() {
		var candle HistoricalCandle
		err := rows.Scan(
			&candle.Symbol, &candle.Token, &candle.Exchange,
			&candle.Open, &candle.High, &candle.Low, &candle.Close, &candle.Volume,
			&candle.DateTime, &candle.Timestamp, &candle.Source,
		)
		if err != nil {
			log.Printf("⚠️ Error scanning candle: %v", err)
			continue
		}
		candles = append(candles, candle)
	}

	return candles, nil
}

// ProcessLiveData processes incoming live market data and updates continuous series
func (hpm *HistoricalPriceManager) ProcessLiveData(marketData MarketData) {
	hpm.mutex.Lock()
	defer hpm.mutex.Unlock()

	// Convert MarketData to HistoricalCandle format
	liveCandle := HistoricalCandle{
		Symbol:    marketData.Symbol,
		Token:     marketData.Token,
		Exchange:  "NSE", // Default, should be determined from token mapping
		Open:      marketData.Open,
		High:      marketData.High,
		Low:       marketData.Low,
		Close:     marketData.LTP, // Use LTP as close price
		Volume:    marketData.Volume,
		DateTime:  time.Unix(marketData.Timestamp/1000, 0).In(hpm.istLocation).Format("2006-01-02 15:04:05"),
		Timestamp: marketData.Timestamp,
		Source:    "LIVE",
	}

	// Update last live data
	hpm.lastLiveData[marketData.Symbol] = liveCandle

	// Update continuous series cache if exists
	if series, exists := hpm.cache[marketData.Symbol]; exists {
		// Add live data to series
		series.Data = append(series.Data, liveCandle)
		series.LiveCount++
		series.TotalCandles++
		series.LastUpdate = time.Now()
		series.LiveStart = time.Unix(marketData.Timestamp/1000, 0)
	}
}

// BuildContinuousSeries creates a continuous series by merging historical and live data
func (hpm *HistoricalPriceManager) BuildContinuousSeries(symbol, interval string, fromTime time.Time) (*ContinuousSeries, error) {
	// Get token and exchange for symbol
	token, exists := hpm.stockDatabase.GetTokenForSymbol(symbol)
	if !exists {
		return nil, fmt.Errorf("token not found for symbol %s", symbol)
	}

	symbolInfo, exchange, exists := hpm.stockDatabase.GetSymbolForToken(token)
	if !exists || symbolInfo != symbol {
		return nil, fmt.Errorf("symbol mapping inconsistent for %s", symbol)
	}

	// Check cache first
	hpm.mutex.RLock()
	if cached, exists := hpm.cache[symbol]; exists {
		// Return cached series if recent enough (within 1 minute)
		if time.Since(cached.LastUpdate) < time.Minute {
			hpm.mutex.RUnlock()
			return cached, nil
		}
	}
	hpm.mutex.RUnlock()

	// Fetch historical data
	toTime := time.Now().In(hpm.istLocation)
	historicalCandles, err := hpm.FetchHistoricalData(symbol, token, exchange, interval, fromTime, toTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	// Store historical data
	if err := hpm.StoreHistoricalData(historicalCandles, interval); err != nil {
		log.Printf("⚠️ Failed to store historical data: %v", err)
	}

	// Get live data if available
	hpm.mutex.RLock()
	liveCandle, hasLiveData := hpm.lastLiveData[symbol]
	hpm.mutex.RUnlock()

	// Build continuous series
	series := &ContinuousSeries{
		Symbol:          symbol,
		Token:           token,
		Exchange:        exchange,
		Interval:        interval,
		Data:            historicalCandles,
		LastUpdate:      time.Now(),
		TotalCandles:    len(historicalCandles),
		HistoricalCount: len(historicalCandles),
		LiveCount:       0,
		HasGaps:         false,
		IsComplete:      true,
	}

	// Add live data if available and recent
	if hasLiveData {
		liveTime := time.Unix(liveCandle.Timestamp/1000, 0)

		// Check if live data is newer than last historical data
		if len(historicalCandles) > 0 {
			lastHistorical := historicalCandles[len(historicalCandles)-1]
			lastHistoricalTime := time.Unix(lastHistorical.Timestamp/1000, 0)

			if liveTime.After(lastHistoricalTime) {
				series.Data = append(series.Data, liveCandle)
				series.LiveCount = 1
				series.TotalCandles++
				series.LiveStart = liveTime
				series.HistoricalEnd = lastHistoricalTime

				// Check for gaps (more than interval duration)
				gap := liveTime.Sub(lastHistoricalTime)
				expectedGap := time.Minute // For 1MIN interval
				if strings.Contains(interval, "5") {
					expectedGap = 5 * time.Minute
				}

				if gap > expectedGap*2 { // Allow some tolerance
					series.HasGaps = true
				}
			}
		} else {
			// No historical data, only live data
			series.Data = []HistoricalCandle{liveCandle}
			series.LiveCount = 1
			series.TotalCandles = 1
			series.HistoricalCount = 0
			series.LiveStart = liveTime
		}
	}

	// Update cache
	hpm.mutex.Lock()
	hpm.cache[symbol] = series
	hpm.mutex.Unlock()

	log.Printf("✅ Built continuous series for %s: %d historical + %d live = %d total candles",
		symbol, series.HistoricalCount, series.LiveCount, series.TotalCandles)

	return series, nil
}

// GetContinuousSeriesData returns continuous series data for API endpoints
func (hpm *HistoricalPriceManager) GetContinuousSeriesData(symbol, interval string, fromTime time.Time) (*ContinuousSeries, error) {
	return hpm.BuildContinuousSeries(symbol, interval, fromTime)
}

// ClearCache clears the in-memory cache
func (hpm *HistoricalPriceManager) ClearCache() {
	hpm.mutex.Lock()
	defer hpm.mutex.Unlock()

	hpm.cache = make(map[string]*ContinuousSeries)
	log.Printf("🧹 Historical price cache cleared")
}

// GetCacheStats returns cache statistics
func (hpm *HistoricalPriceManager) GetCacheStats() map[string]interface{} {
	hpm.mutex.RLock()
	defer hpm.mutex.RUnlock()

	return map[string]interface{}{
		"cached_series":    len(hpm.cache),
		"live_data_points": len(hpm.lastLiveData),
		"timezone":         hpm.istLocation.String(),
		"base_url":         hpm.baseURL,
	}
}

// Close closes the database connection
func (hpm *HistoricalPriceManager) Close() error {
	if hpm.db != nil {
		return hpm.db.Close()
	}
	return nil
}
