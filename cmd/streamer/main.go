package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang-market-service/internal/api"
	"golang-market-service/internal/bridge"
	"golang-market-service/internal/candle"
	"golang-market-service/internal/historical"
	"golang-market-service/internal/stock"
	"golang-market-service/internal/storage"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// Config holds application configuration
type Config struct {
	StockDBPath   string
	RedisURL      string
	TimescaleURL  string
	APIBaseURL    string
	HistoricalURL string
	Port          string
	PythonScript  string
}

// LiveMarketData represents comprehensive market data with 52-week info
type LiveMarketData struct {
	// Basic market data
	Symbol        string  `json:"symbol"`
	Token         string  `json:"token"`
	Exchange      string  `json:"exchange"`
	LTP           float64 `json:"ltp"`
	Open          float64 `json:"open"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Close         float64 `json:"close"`
	PrevClose     float64 `json:"prev_close"`
	Volume        int64   `json:"volume"`
	PercentChange float64 `json:"percent_change"`

	// 52-week data from database
	Week52High     float64 `json:"week_52_high"`
	Week52Low      float64 `json:"week_52_low"`
	Week52HighDate string  `json:"week_52_high_date"`
	Week52LowDate  string  `json:"week_52_low_date"`

	// Day high/low tracking
	DayHigh float64 `json:"day_high"`
	DayLow  float64 `json:"day_low"`

	// Additional data
	AvgVolume5D int64     `json:"avg_volume_5d"`
	Timestamp   int64     `json:"timestamp"`
	LastUpdated time.Time `json:"last_updated"`

	// New record flags
	IsNewWeek52High bool `json:"is_new_week_52_high"`
	IsNewWeek52Low  bool `json:"is_new_week_52_low"`
}

// WebSocketMessage represents enhanced WebSocket message
type WebSocketMessage struct {
	Type      string      `json:"type"`
	Symbol    string      `json:"symbol,omitempty"`
	Token     string      `json:"token,omitempty"`
	Exchange  string      `json:"exchange,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// Application represents the main streaming application
type Application struct {
	config *Config

	// Core components
	stockDB        *stock.Database
	redisAdapter   *storage.RedisAdapter
	timescaleDB    *storage.TimescaleDBAdapter
	candleEngine   *candle.CandleEngine
	streamerBridge *bridge.StreamerBridge
	week52Manager  *stock.Week52Manager

	// API components
	intradayAPI          *api.IntradayAPI
	websocketAPI         *api.WebSocketHandler
	enhancedWebSocketAPI *api.EnhancedWebSocketHandler
	historicalClient     *historical.IndiraTradeClient

	// Enhanced WebSocket for live streaming
	wsUpgrader websocket.Upgrader
	wsClients  map[*websocket.Conn]*WSClient
	wsMutex    sync.RWMutex

	// Python bridge
	pythonProcess *exec.Cmd

	// HTTP server
	server *http.Server

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// WSClient represents a WebSocket client connection with dynamic subscription management
type WSClient struct {
	conn              *websocket.Conn
	subscribedTokens  map[string]bool
	subscribedSymbols map[string]bool // Track symbols for easy lookup
	clientID          string
	lastPing          time.Time
	isAlive           bool
	mutex             sync.Mutex
}

// SubscriptionMessage represents WebSocket subscription messages
type SubscriptionMessage struct {
	Type   string   `json:"type"`   // "subscribe", "unsubscribe", "list_subscriptions"
	Stocks []string `json:"stocks"` // Stock symbols to subscribe/unsubscribe
	Token  string   `json:"token"`  // Single token (alternative to stocks)
	Symbol string   `json:"symbol"` // Single symbol (alternative to stocks)
}

// SubscriptionResponse represents subscription response
type SubscriptionResponse struct {
	Type       string   `json:"type"`
	Success    bool     `json:"success"`
	Message    string   `json:"message"`
	Subscribed []string `json:"subscribed,omitempty"`
	Failed     []string `json:"failed,omitempty"`
	TotalCount int      `json:"total_count"`
	Timestamp  int64    `json:"timestamp"`
}

func init() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Warning: .env file not found, using system environment variables")
	}
}

func main() {
	log.Printf("🚀 Starting Odin Streamer v2.0 - Enhanced Live Market Data Streaming")

	config := loadConfig()
	app, err := NewApplication(config)
	if err != nil {
		log.Fatalf("❌ Failed to create application: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	app.ctx = ctx
	app.cancel = cancel

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := app.Start(); err != nil {
		log.Fatalf("❌ Failed to start application: %v", err)
	}

	<-sigChan
	log.Printf("🛑 Received shutdown signal")

	if err := app.Stop(); err != nil {
		log.Printf("⚠️ Error during shutdown: %v", err)
	}

	log.Printf("✅ Application shutdown complete")
}

func loadConfig() *Config {
	return &Config{
		StockDBPath:   getEnvOrDefault("STOCK_DB", "cmd/streamer/stocks.db"),
		RedisURL:      getEnvOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		TimescaleURL:  getEnvOrDefault("TIMESCALE_URL", "postgres://odin_user:odin_password@localhost:5432/odin_streamer?sslmode=disable"),
		APIBaseURL:    getEnvOrDefault("API_BASE_URL", "https://uatdev.indiratrade.com/companies-details"),
		HistoricalURL: getEnvOrDefault("HISTORICAL_API_URL", "https://trading.indiratrade.com:3000"),
		Port:          getEnvOrDefault("WS_PORT", "8080"),
		PythonScript:  getEnvOrDefault("PYTHON_SCRIPT", "scripts/b2c_bridge.py"),
	}
}

func NewApplication(config *Config) (*Application, error) {
	app := &Application{
		config:    config,
		wsClients: make(map[*websocket.Conn]*WSClient),
		wsUpgrader: websocket.Upgrader{
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	if err := app.initializeComponents(); err != nil {
		return nil, fmt.Errorf("failed to initialize components: %w", err)
	}

	return app, nil
}

func (app *Application) initializeComponents() error {
	var err error

	// Initialize stock database
	log.Printf("📊 Initializing stock database...")
	app.stockDB, err = stock.NewDatabase(app.config.StockDBPath, app.config.APIBaseURL)
	if err != nil {
		return fmt.Errorf("failed to initialize stock database: %w", err)
	}

	// Initialize 52-week manager
	log.Printf("📈 Initializing 52-week manager...")
	app.week52Manager, err = stock.NewWeek52Manager(app.stockDB)
	if err != nil {
		return fmt.Errorf("failed to initialize 52-week manager: %w", err)
	}

	// Initialize Redis adapter
	log.Printf("🔴 Initializing Redis adapter...")
	app.redisAdapter, err = storage.NewRedisAdapter(app.config.RedisURL)
	if err != nil {
		return fmt.Errorf("failed to initialize Redis adapter: %w", err)
	}

	// Initialize TimescaleDB adapter
	log.Printf("🐘 Initializing TimescaleDB adapter...")
	app.timescaleDB, err = storage.NewTimescaleDBAdapter(app.config.TimescaleURL)
	if err != nil {
		return fmt.Errorf("failed to initialize TimescaleDB adapter: %w", err)
	}

	// Initialize candle engine
	log.Printf("🕯️ Initializing candle engine...")
	app.candleEngine, err = candle.NewCandleEngine()
	if err != nil {
		return fmt.Errorf("failed to initialize candle engine: %w", err)
	}

	// Initialize streamer bridge
	log.Printf("🌉 Initializing streamer bridge...")
	app.streamerBridge, err = bridge.NewStreamerBridge(app.redisAdapter, app.timescaleDB)
	if err != nil {
		return fmt.Errorf("failed to initialize streamer bridge: %w", err)
	}

	// Initialize historical client
	log.Printf("📈 Initializing historical client...")
	app.historicalClient, err = historical.NewIndiraTradeClient(app.config.HistoricalURL)
	if err != nil {
		return fmt.Errorf("failed to initialize historical client: %w", err)
	}

	// Initialize API components
	log.Printf("🌐 Initializing API components...")
	app.intradayAPI, err = api.NewIntradayAPI(app.redisAdapter, app.timescaleDB, app.historicalClient)
	if err != nil {
		return fmt.Errorf("failed to initialize intraday API: %w", err)
	}

	app.websocketAPI, err = api.NewWebSocketHandler(app.redisAdapter, app.timescaleDB, app.historicalClient, app.stockDB)
	if err != nil {
		return fmt.Errorf("failed to initialize WebSocket handler: %w", err)
	}

	// Initialize enhanced WebSocket handler
	log.Printf("🌐 Initializing enhanced WebSocket handler...")
	app.enhancedWebSocketAPI, err = api.NewEnhancedWebSocketHandler(app.redisAdapter, app.timescaleDB, app.historicalClient, app.stockDB)
	if err != nil {
		return fmt.Errorf("failed to initialize enhanced WebSocket handler: %w", err)
	}

	log.Printf("✅ All components initialized successfully")
	return nil
}

func (app *Application) Start() error {
	log.Printf("🚀 Starting enhanced live market data streaming...")

	// Load stock data
	if err := app.loadStockData(); err != nil {
		return fmt.Errorf("failed to load stock data: %w", err)
	}

	// Start core components
	if err := app.startCoreComponents(); err != nil {
		return fmt.Errorf("failed to start core components: %w", err)
	}

	// Start Python bridge for live data
	if err := app.startPythonBridge(); err != nil {
		return fmt.Errorf("failed to start Python bridge: %w", err)
	}

	// Setup HTTP routes
	app.setupRoutes()

	// Start HTTP server
	if err := app.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	log.Printf("✅ Enhanced live market data streaming started successfully")
	log.Printf("🌐 WebSocket endpoint: ws://localhost:%s/live-stream", app.config.Port)
	log.Printf("📊 Features: Live market data + 52-week high/low + candles + real-time updates")

	return nil
}

func (app *Application) loadStockData() error {
	log.Printf("📊 Loading stock data...")

	if err := app.stockDB.InitializeFromDatabase(); err != nil {
		log.Printf("⚠️ Warning: Failed to initialize from database: %v", err)
	}

	stats := app.stockDB.GetStats()
	totalStocks := stats["total_stocks"].(int)

	if totalStocks < 1000 {
		log.Printf("🔄 Database has only %d stocks, fetching from API...", totalStocks)

		// First fetch and store stocks from API
		result, err := app.stockDB.FetchAndStoreStocks()
		if err != nil {
			return fmt.Errorf("failed to fetch and store stocks: %w", err)
		}

		log.Printf("✅ Successfully stored %d stocks from API", result.ActiveStocks)

		// Then update co_code for all stocks
		if err := app.stockDB.UpdateCoCodeForAllStocks(); err != nil {
			log.Printf("⚠️ Warning: Failed to update co_code: %v", err)
			// Don't fail startup, continue with existing data
		}
	} else {
		log.Printf("✅ Database contains %d stocks", totalStocks)
	}

	// Check and populate missing 52-week data automatically
	if err := app.ensure52WeekDataComplete(); err != nil {
		log.Printf("⚠️ Warning: Failed to ensure 52-week data completeness: %v", err)
		// Don't fail startup, just log the warning
	}

	return nil
}

// ensure52WeekDataComplete intelligently checks and populates only missing 52-week data
func (app *Application) ensure52WeekDataComplete() error {
	log.Printf("📈 Performing intelligent 52-week data check...")

	// Get 52-week statistics
	week52Stats := app.stockDB.Get52WeekStats()
	totalStocks := week52Stats["total_stocks"].(int)
	stocksWith52Week := week52Stats["stocks_with_52week"].(int)
	missing52Week := week52Stats["missing_52week"].(int)

	log.Printf("📊 52-week data status:")
	log.Printf("   📈 Total stocks in database: %d", totalStocks)
	log.Printf("   ✅ Stocks with existing 52-week data: %d", stocksWith52Week)
	log.Printf("   ❌ Stocks missing 52-week data: %d", missing52Week)
	log.Printf("   📊 Current coverage: %.1f%%", week52Stats["coverage_percentage"].(float64))

	// Only fetch missing data if there are stocks without 52-week data
	if missing52Week > 0 {
		log.Printf("🎯 Smart Update: Only fetching 52-week data for %d stocks that don't have it", missing52Week)
		log.Printf("⏱️  Estimated time: %d-%.0f minutes (only for missing stocks)", missing52Week/100, float64(missing52Week)/60.0)
		log.Printf("💾 Existing 52-week data for %d stocks will be preserved", stocksWith52Week)

		// Test API connectivity first
		log.Printf("🧪 Testing StocksEmoji API connectivity...")
		if err := app.stockDB.TestStocksEmojiAPI(); err != nil {
			log.Printf("⚠️ StocksEmoji API test failed: %v", err)
			log.Printf("📊 Continuing with existing 52-week data (%d stocks)", stocksWith52Week)
			return nil // Don't fail startup, just continue with existing data
		}

		// Fetch 52-week data only for stocks that don't have it
		log.Printf("🚀 Starting smart 52-week data update (missing stocks only)...")
		startTime := time.Now()

		// Use the update method to fetch 52-week data
		if err := app.stockDB.UpdateMissing52WeekDataFromStocksEmoji(); err != nil {
			log.Printf("⚠️ Failed to update 52-week data: %v", err)
			log.Printf("📊 Continuing with existing 52-week data (%d stocks)", stocksWith52Week)
			return nil // Don't fail startup, just continue with existing data
		}
		duration := time.Since(startTime)

		// Get updated statistics
		updatedStats := app.stockDB.Get52WeekStats()
		updatedWith52Week := updatedStats["stocks_with_52week"].(int)
		updatedCoverage := updatedStats["coverage_percentage"].(float64)
		newlyAdded := updatedWith52Week - stocksWith52Week

		log.Printf("✅ Smart 52-week data update completed in %v!", duration)
		log.Printf("📊 Final results:")
		log.Printf("   ✅ Total stocks with 52-week data: %d (was %d)", updatedWith52Week, stocksWith52Week)
		log.Printf("   📈 Coverage improved: %.1f%% (was %.1f%%)", updatedCoverage, week52Stats["coverage_percentage"].(float64))
		log.Printf("   🎯 Newly added 52-week data for: %d stocks", newlyAdded)
		log.Printf("   💾 Preserved existing data for: %d stocks", stocksWith52Week)

		if updatedWith52Week == totalStocks {
			log.Printf("🎉 Perfect! All %d stocks now have complete 52-week data", totalStocks)
		} else {
			log.Printf("📊 Current status: %d/%d stocks have 52-week data (%.1f%% coverage)",
				updatedWith52Week, totalStocks, updatedCoverage)
		}

	} else {
		log.Printf("🎉 Excellent! All %d stocks already have complete 52-week data", totalStocks)
		log.Printf("💾 No API calls needed - using existing persistent data")
	}

	log.Printf("⚡ Smart 52-week high/low detection is ready!")
	log.Printf("🔄 Runtime updates will only process subscribed stocks for efficiency")
	return nil
}

func (app *Application) startCoreComponents() error {
	log.Printf("⚡ Starting core components...")

	// Start streamer bridge
	if err := app.streamerBridge.Start(); err != nil {
		return fmt.Errorf("failed to start streamer bridge: %w", err)
	}

	// Setup candle engine callbacks
	app.candleEngine.SetPublishCallback(func(update candle.CandleUpdate) error {
		app.websocketAPI.BroadcastCandleUpdate(update)
		return nil
	})

	log.Printf("✅ Core components started")
	return nil
}

func (app *Application) startPythonBridge() error {
	tokenPairs, err := app.stockDB.GetAllTokenExchangePairs()
	if err != nil {
		return fmt.Errorf("failed to get token pairs: %w", err)
	}

	log.Printf("🐍 Starting Python bridge with %d token:exchange pairs", len(tokenPairs))

	args := []string{app.config.PythonScript}
	args = append(args, tokenPairs...)

	app.pythonProcess = exec.Command("python", args...)

	stdout, err := app.pythonProcess.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := app.pythonProcess.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := app.pythonProcess.Start(); err != nil {
		return fmt.Errorf("failed to start Python process: %w", err)
	}

	// Start data processing goroutines
	app.wg.Add(2)
	go app.processPythonOutput(stdout)
	go app.processPythonErrors(stderr)

	log.Printf("✅ Python bridge started successfully")
	return nil
}

func (app *Application) processPythonOutput(stdout io.ReadCloser) {
	defer app.wg.Done()
	defer stdout.Close()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Try to parse as JSON market data
		var marketData map[string]interface{}
		if err := json.Unmarshal([]byte(line), &marketData); err != nil {
			log.Printf("🐍 Python Output: %s", line)
			continue
		}

		// Process and broadcast live market data
		app.processLiveMarketData(marketData)
	}
}

func (app *Application) processPythonErrors(stderr io.ReadCloser) {
	defer app.wg.Done()
	defer stderr.Close()

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			log.Printf("🐍 Python Error: %s", line)
		}
	}
}

func (app *Application) processLiveMarketData(data map[string]interface{}) {
	// Extract basic market data
	token, _ := data["token"].(string)
	if token == "" {
		return
	}

	// Get symbol and exchange from token using stock database
	symbol, exchange, exists := app.stockDB.GetSymbolForToken(token)
	if !exists {
		// Skip unknown tokens
		return
	}

	// Get 52-week data from database
	week52Data, err := app.week52Manager.GetWeek52Data(symbol, exchange)
	if err != nil {
		log.Printf("⚠️ Failed to get 52-week data for %s: %v", symbol, err)
		week52Data = &stock.Week52Data{} // Use empty data
	}

	// Extract market data values with proper type handling
	ltp, _ := data["ltp"].(float64)
	high, _ := data["high"].(float64)
	low, _ := data["low"].(float64)
	open, _ := data["open"].(float64)
	close, _ := data["close"].(float64)

	// Handle volume with multiple type possibilities (int64, float64, or int)
	var volume int64
	switch v := data["volume"].(type) {
	case int64:
		volume = v
	case float64:
		volume = int64(v)
	case int:
		volume = int64(v)
	default:
		volume = 0
	}

	// Handle timestamp with multiple type possibilities
	var timestamp int64
	switch t := data["timestamp"].(type) {
	case int64:
		timestamp = t
	case float64:
		timestamp = int64(t)
	case int:
		timestamp = int64(t)
	default:
		timestamp = time.Now().UnixMilli()
	}

	// Use current time if timestamp not provided
	if timestamp == 0 {
		timestamp = time.Now().UnixMilli()
	}

	// Check for new 52-week records
	isNewWeek52High := false
	isNewWeek52Low := false

	if high > 0 && low > 0 {
		newRecord, err := app.week52Manager.UpdateDayHighLow(symbol, exchange, high, low)
		if err != nil {
			log.Printf("⚠️ Failed to update day high/low for %s: %v", symbol, err)
		} else if newRecord {
			// Refresh 52-week data after update
			if updatedData, err := app.week52Manager.GetWeek52Data(symbol, exchange); err == nil {
				week52Data = updatedData
				isNewWeek52High = high >= week52Data.Week52High
				isNewWeek52Low = low <= week52Data.Week52Low && week52Data.Week52Low > 0
			}
		}
	}

	// Calculate percent change
	percentChange := 0.0
	prevClose, _ := data["prev_close"].(float64)
	if prevClose > 0 && ltp > 0 {
		percentChange = ((ltp - prevClose) / prevClose) * 100
	}

	// Create market data for StreamerBridge
	marketData := bridge.MarketData{
		Symbol:        symbol,
		Token:         token,
		LTP:           ltp,
		High:          high,
		Low:           low,
		Open:          open,
		Close:         close,
		Volume:        volume,
		PercentChange: percentChange,
		Week52High:    week52Data.Week52High,
		Week52Low:     week52Data.Week52Low,
		PrevClose:     prevClose,
		AvgVolume5D:   volume, // Use current volume as estimate
		Timestamp:     timestamp,
	}

	// Feed data into StreamerBridge for candle processing
	select {
	case app.streamerBridge.GetMarketDataChannel() <- marketData:
		// Successfully sent to bridge
	default:
		// Channel full, skip this tick
		log.Printf("⚠️ StreamerBridge channel full, skipping tick for %s", symbol)
	}

	// Create comprehensive live market data for WebSocket broadcasting
	liveData := LiveMarketData{
		Symbol:          symbol,
		Token:           token,
		Exchange:        exchange,
		LTP:             ltp,
		Open:            open,
		High:            high,
		Low:             low,
		Close:           close,
		Volume:          volume,
		Week52High:      week52Data.Week52High,
		Week52Low:       week52Data.Week52Low,
		Week52HighDate:  week52Data.Week52HighDate,
		Week52LowDate:   week52Data.Week52LowDate,
		DayHigh:         high,
		DayLow:          low,
		Timestamp:       timestamp,
		LastUpdated:     time.Now(),
		IsNewWeek52High: isNewWeek52High,
		IsNewWeek52Low:  isNewWeek52Low,
		PercentChange:   percentChange,
		PrevClose:       prevClose,
		AvgVolume5D:     volume,
	}

	// Broadcast to WebSocket clients
	app.broadcastLiveData(liveData)
}

func (app *Application) broadcastLiveData(data LiveMarketData) {
	// Broadcast to enhanced WebSocket clients (with market hours control and subscriptions)
	app.enhancedWebSocketAPI.BroadcastMarketData(data.Token, data.Symbol, data.Exchange, data)

	// Broadcast to legacy WebSocket clients
	app.wsMutex.RLock()
	defer app.wsMutex.RUnlock()

	message := WebSocketMessage{
		Type:      "live_market_data",
		Symbol:    data.Symbol,
		Token:     data.Token,
		Exchange:  data.Exchange,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	// Special message for new 52-week records
	if data.IsNewWeek52High || data.IsNewWeek52Low {
		recordType := "new_52_week_record"
		if data.IsNewWeek52High && data.IsNewWeek52Low {
			recordType = "new_52_week_high_low"
		} else if data.IsNewWeek52High {
			recordType = "new_52_week_high"
		} else {
			recordType = "new_52_week_low"
		}

		recordMessage := WebSocketMessage{
			Type:      recordType,
			Symbol:    data.Symbol,
			Token:     data.Token,
			Exchange:  data.Exchange,
			Data:      data,
			Timestamp: time.Now().UnixMilli(),
		}

		// Broadcast record message first
		for conn, client := range app.wsClients {
			if client.isAlive && (len(client.subscribedTokens) == 0 || client.subscribedTokens[data.Token]) {
				client.mutex.Lock()
				if err := conn.WriteJSON(recordMessage); err != nil {
					client.isAlive = false
				}
				client.mutex.Unlock()
			}
		}
	}

	// Broadcast regular market data
	for conn, client := range app.wsClients {
		if client.isAlive && (len(client.subscribedTokens) == 0 || client.subscribedTokens[data.Token]) {
			client.mutex.Lock()
			if err := conn.WriteJSON(message); err != nil {
				client.isAlive = false
			}
			client.mutex.Unlock()
		}
	}
}

func (app *Application) setupRoutes() {
	mux := http.NewServeMux()

	// Enhanced live streaming WebSocket
	mux.HandleFunc("/live-stream", app.handleLiveStream)

	// Enhanced WebSocket with persistent connections and dynamic subscriptions
	mux.HandleFunc("/enhanced-stream", app.enhancedWebSocketAPI.HandleEnhancedWebSocket)

	// Original API routes
	mux.HandleFunc("/api/health", app.handleHealth)
	mux.HandleFunc("/api/stocks/stats", app.handleStockStats)
	mux.HandleFunc("/api/stocks/refresh", app.handleStockRefresh)
	mux.HandleFunc("/api/52week/stats", app.handle52WeekStats)
	mux.HandleFunc("/api/enhanced/stats", app.handleEnhancedStats)

	// Intraday API routes
	mux.HandleFunc("/intraday/", app.intradayAPI.HandleIntradayRequest)
	mux.HandleFunc("/intraday/stats", app.intradayAPI.HandleIntradayStats)
	mux.HandleFunc("/intraday/health", app.intradayAPI.HandleIntradayHealth)

	// Original WebSocket for candles
	mux.HandleFunc("/stream", app.websocketAPI.HandleWebSocket)

	// Root endpoint
	mux.HandleFunc("/", app.handleRoot)

	app.server = &http.Server{
		Addr:    ":" + app.config.Port,
		Handler: mux,
	}

	log.Printf("✅ Enhanced HTTP routes configured")
}

func (app *Application) handleLiveStream(w http.ResponseWriter, r *http.Request) {
	conn, err := app.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Parse optional stocks parameter
	stocksParam := r.URL.Query().Get("stocks")

	client := &WSClient{
		conn:              conn,
		subscribedTokens:  make(map[string]bool),
		subscribedSymbols: make(map[string]bool),
		lastPing:          time.Now(),
		isAlive:           true,
	}

	// Subscribe to specific stocks if provided
	if stocksParam != "" {
		stockSymbols := strings.Split(strings.ToUpper(stocksParam), ",")
		for _, symbol := range stockSymbols {
			if token, exists := app.stockDB.GetTokenForSymbol(symbol); exists {
				client.subscribedTokens[token] = true
			}
		}
		log.Printf("🔌 WebSocket client connected with subscriptions: %v", stockSymbols)
	} else {
		log.Printf("🔌 WebSocket client connected for all market data")
	}

	// Register client
	app.wsMutex.Lock()
	app.wsClients[conn] = client
	app.wsMutex.Unlock()

	// Send welcome message
	welcomeMsg := WebSocketMessage{
		Type: "welcome",
		Data: map[string]interface{}{
			"message":      "Connected to Odin Streamer Live Market Data",
			"features":     []string{"live_market_data", "52_week_records", "candles", "real_time_updates"},
			"subscribed":   len(client.subscribedTokens) > 0,
			"total_stocks": len(client.subscribedTokens),
		},
		Timestamp: time.Now().UnixMilli(),
	}
	conn.WriteJSON(welcomeMsg)

	// Handle client messages
	app.handleWSClientMessages(client)

	// Cleanup on disconnect
	app.wsMutex.Lock()
	delete(app.wsClients, conn)
	app.wsMutex.Unlock()

	log.Printf("🔌 WebSocket client disconnected")
}

func (app *Application) handleWSClientMessages(client *WSClient) {
	for {
		var rawMsg map[string]interface{}
		err := client.conn.ReadJSON(&rawMsg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️ WebSocket error: %v", err)
			}
			break
		}

		msgType, ok := rawMsg["type"].(string)
		if !ok {
			continue
		}

		switch msgType {
		case "ping":
			client.lastPing = time.Now()
			pongMsg := WebSocketMessage{
				Type:      "pong",
				Timestamp: time.Now().UnixMilli(),
			}
			client.mutex.Lock()
			client.conn.WriteJSON(pongMsg)
			client.mutex.Unlock()

		case "subscribe":
			app.handleSubscription(client, rawMsg, true)

		case "unsubscribe":
			app.handleSubscription(client, rawMsg, false)

		case "list_subscriptions":
			app.handleListSubscriptions(client)

		default:
			log.Printf("⚠️ Unknown message type: %s", msgType)
		}
	}
}

// handleSubscription processes subscribe/unsubscribe requests
func (app *Application) handleSubscription(client *WSClient, rawMsg map[string]interface{}, subscribe bool) {
	client.mutex.Lock()
	defer client.mutex.Unlock()

	// Initialize maps if needed
	if client.subscribedTokens == nil {
		client.subscribedTokens = make(map[string]bool)
	}
	if client.subscribedSymbols == nil {
		client.subscribedSymbols = make(map[string]bool)
	}

	var subscribed []string
	var failed []string
	action := "subscribe"
	if !subscribe {
		action = "unsubscribe"
	}

	// Handle single symbol
	if symbol, ok := rawMsg["symbol"].(string); ok && symbol != "" {
		symbol = strings.ToUpper(symbol)
		if token, exists := app.stockDB.GetTokenForSymbol(symbol); exists {
			if subscribe {
				client.subscribedTokens[token] = true
				client.subscribedSymbols[symbol] = true
				subscribed = append(subscribed, symbol)
				log.Printf("📊 Client subscribed to %s (token: %s)", symbol, token)
			} else {
				delete(client.subscribedTokens, token)
				delete(client.subscribedSymbols, symbol)
				subscribed = append(subscribed, symbol)
				log.Printf("📊 Client unsubscribed from %s (token: %s)", symbol, token)
			}
		} else {
			failed = append(failed, symbol)
			log.Printf("⚠️ Symbol not found: %s", symbol)
		}
	}

	// Handle single token
	if token, ok := rawMsg["token"].(string); ok && token != "" {
		if symbol, exchange, exists := app.stockDB.GetSymbolForToken(token); exists {
			if subscribe {
				client.subscribedTokens[token] = true
				client.subscribedSymbols[symbol] = true
				subscribed = append(subscribed, fmt.Sprintf("%s:%s", symbol, exchange))
				log.Printf("📊 Client subscribed to %s:%s (token: %s)", symbol, exchange, token)
			} else {
				delete(client.subscribedTokens, token)
				delete(client.subscribedSymbols, symbol)
				subscribed = append(subscribed, fmt.Sprintf("%s:%s", symbol, exchange))
				log.Printf("📊 Client unsubscribed from %s:%s (token: %s)", symbol, exchange, token)
			}
		} else {
			failed = append(failed, token)
			log.Printf("⚠️ Token not found: %s", token)
		}
	}

	// Handle multiple stocks
	if stocksRaw, ok := rawMsg["stocks"]; ok {
		var stocks []string

		// Handle both []interface{} and []string
		switch v := stocksRaw.(type) {
		case []interface{}:
			for _, stock := range v {
				if s, ok := stock.(string); ok {
					stocks = append(stocks, s)
				}
			}
		case []string:
			stocks = v
		}

		for _, stock := range stocks {
			stock = strings.ToUpper(strings.TrimSpace(stock))
			if stock == "" {
				continue
			}

			if token, exists := app.stockDB.GetTokenForSymbol(stock); exists {
				if subscribe {
					client.subscribedTokens[token] = true
					client.subscribedSymbols[stock] = true
					subscribed = append(subscribed, stock)
					log.Printf("📊 Client subscribed to %s (token: %s)", stock, token)
				} else {
					delete(client.subscribedTokens, token)
					delete(client.subscribedSymbols, stock)
					subscribed = append(subscribed, stock)
					log.Printf("📊 Client unsubscribed from %s (token: %s)", stock, token)
				}
			} else {
				failed = append(failed, stock)
				log.Printf("⚠️ Symbol not found: %s", stock)
			}
		}
	}

	// Send response
	response := SubscriptionResponse{
		Type:       action + "_response",
		Success:    len(subscribed) > 0,
		Message:    fmt.Sprintf("%s completed", strings.Title(action)),
		Subscribed: subscribed,
		Failed:     failed,
		TotalCount: len(client.subscribedTokens),
		Timestamp:  time.Now().UnixMilli(),
	}

	if len(subscribed) == 0 && len(failed) > 0 {
		response.Success = false
		response.Message = fmt.Sprintf("%s failed - no valid symbols found", strings.Title(action))
	}

	client.conn.WriteJSON(response)

	// Log subscription status
	log.Printf("🔌 Client now has %d active subscriptions", len(client.subscribedTokens))
}

// handleListSubscriptions sends current subscription list to client
func (app *Application) handleListSubscriptions(client *WSClient) {
	client.mutex.Lock()
	defer client.mutex.Unlock()

	var subscriptions []string
	for symbol := range client.subscribedSymbols {
		subscriptions = append(subscriptions, symbol)
	}

	response := SubscriptionResponse{
		Type:       "subscription_list",
		Success:    true,
		Message:    "Current subscriptions",
		Subscribed: subscriptions,
		TotalCount: len(subscriptions),
		Timestamp:  time.Now().UnixMilli(),
	}

	client.conn.WriteJSON(response)
}

func (app *Application) startHTTPServer() error {
	log.Printf("🌐 Starting HTTP server on port %s", app.config.Port)

	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ HTTP server error: %v", err)
		}
	}()

	return nil
}

func (app *Application) Stop() error {
	log.Printf("🛑 Stopping application...")

	if app.cancel != nil {
		app.cancel()
	}

	// Stop HTTP server
	if app.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		app.server.Shutdown(ctx)
	}

	// Stop Python bridge
	if app.pythonProcess != nil {
		app.pythonProcess.Process.Kill()
		app.pythonProcess.Wait()
	}

	// Stop core components
	if app.streamerBridge != nil {
		app.streamerBridge.Stop()
	}

	if app.candleEngine != nil {
		app.candleEngine.Close()
	}

	// Close database connections
	if app.stockDB != nil {
		app.stockDB.Close()
	}

	if app.redisAdapter != nil {
		app.redisAdapter.Close()
	}

	if app.timescaleDB != nil {
		app.timescaleDB.Close()
	}

	app.wg.Wait()
	log.Printf("✅ Application stopped successfully")
	return nil
}

// HTTP Handlers

func (app *Application) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	stats := map[string]interface{}{
		"service":   "Odin Streamer v2.0 - Enhanced Live Market Data",
		"status":    "running",
		"timestamp": time.Now().Format(time.RFC3339),
		"features": []string{
			"Live market data streaming",
			"52-week high/low tracking",
			"Real-time candle generation",
			"WebSocket broadcasting",
			"Historical data API",
		},
		"endpoints": map[string]string{
			"live_stream":   "/live-stream (WebSocket)",
			"candle_stream": "/stream?stocks=SYMBOL1,SYMBOL2 (WebSocket)",
			"health":        "/api/health",
			"stock_stats":   "/api/stocks/stats",
			"intraday_data": "/intraday/{exchange}/{token}",
			"52week_stats":  "/api/52week/stats",
		},
		"websocket_clients": len(app.wsClients),
	}

	writeJSON(w, stats)
}

func (app *Application) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"components": map[string]string{
			"stock_database":  "healthy",
			"redis":           "healthy",
			"timescale":       "healthy",
			"candle_engine":   "healthy",
			"streamer_bridge": "healthy",
			"python_bridge":   "healthy",
			"week52_manager":  "healthy",
		},
		"websocket_clients":     len(app.wsClients),
		"python_bridge_running": app.pythonProcess != nil,
	}

	writeJSON(w, health)
}

func (app *Application) handleStockStats(w http.ResponseWriter, r *http.Request) {
	stats := app.stockDB.GetStats()
	response := map[string]interface{}{
		"database_stats":    stats,
		"websocket_clients": len(app.wsClients),
		"service_status":    "running",
		"timestamp":         time.Now().Format(time.RFC3339),
	}
	writeJSON(w, response)
}

func (app *Application) handleStockRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := app.stockDB.UpdateCoCodeForAllStocks(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to refresh stock data: %v", err), http.StatusInternalServerError)
		return
	}

	stats := app.stockDB.GetStats()
	response := map[string]interface{}{
		"success":        true,
		"message":        "Stock data refreshed successfully",
		"database_stats": stats,
		"timestamp":      time.Now().Format(time.RFC3339),
	}
	writeJSON(w, response)
}

func (app *Application) handle52WeekStats(w http.ResponseWriter, r *http.Request) {
	stats := app.stockDB.Get52WeekStats()

	response := map[string]interface{}{
		"week_52_stats": stats,
		"timestamp":     time.Now().Format(time.RFC3339),
	}
	writeJSON(w, response)
}

func (app *Application) handleEnhancedStats(w http.ResponseWriter, r *http.Request) {
	enhancedStats := app.enhancedWebSocketAPI.GetStats()
	streamerStats := app.streamerBridge.GetStats()

	response := map[string]interface{}{
		"enhanced_websocket": enhancedStats,
		"streamer_bridge":    streamerStats,
		"timestamp":          time.Now().Format(time.RFC3339),
	}
	writeJSON(w, response)
}

// Utility functions

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func writeJSON(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(data)
}
