package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang-market-service/internal/candle"
	"golang-market-service/internal/historical"
	"golang-market-service/internal/storage"

	"github.com/gorilla/websocket"
)

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	Type      string      `json:"type"`
	Symbol    string      `json:"symbol,omitempty"`
	Token     string      `json:"token,omitempty"`
	Exchange  string      `json:"exchange,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// InitDataMessage represents the initial data sent on connect
type InitDataMessage struct {
	Symbol       string          `json:"symbol"`
	Token        string          `json:"token"`
	Exchange     string          `json:"exchange"`
	Date         string          `json:"date"`
	MarketOpen   string          `json:"market_open"`
	MarketClose  string          `json:"market_close"`
	TotalCandles int             `json:"total_candles"`
	Candles      []candle.Candle `json:"candles"`
}

// StockDatabase interface for symbol/token lookups
type StockDatabase interface {
	GetTokenForSymbol(symbol string) (string, bool)
	GetSymbolForToken(token string) (string, string, bool)
}

// WebSocketHandler handles WebSocket connections for real-time candle data
type WebSocketHandler struct {
	upgrader         websocket.Upgrader
	redisAdapter     *storage.RedisAdapter
	timescaleAdapter *storage.TimescaleDBAdapter
	historicalClient *historical.IndiraTradeClient
	stockDatabase    StockDatabase
	istLocation      *time.Location
	clients          map[*websocket.Conn]*ClientConnection
	clientsMutex     sync.RWMutex
}

// ClientConnection represents a connected WebSocket client
type ClientConnection struct {
	conn             *websocket.Conn
	subscribedTokens map[string]bool
	lastPing         time.Time
	isAlive          bool
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(redisAdapter *storage.RedisAdapter, timescaleAdapter *storage.TimescaleDBAdapter, historicalClient *historical.IndiraTradeClient, stockDatabase StockDatabase) (*WebSocketHandler, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	handler := &WebSocketHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for now
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		redisAdapter:     redisAdapter,
		timescaleAdapter: timescaleAdapter,
		historicalClient: historicalClient,
		stockDatabase:    stockDatabase,
		istLocation:      istLocation,
		clients:          make(map[*websocket.Conn]*ClientConnection),
	}

	log.Printf("✅ WebSocket handler initialized with production stock database")
	return handler, nil
}

// HandleWebSocket handles WebSocket connections
func (wsh *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := wsh.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Parse stocks parameter from query string
	stocksParam := r.URL.Query().Get("stocks")
	if stocksParam == "" {
		wsh.sendError(conn, "Missing 'stocks' parameter")
		return
	}

	// Parse stock symbols (e.g., "RELIANCE,TCS,INFY")
	stockSymbols := strings.Split(strings.ToUpper(stocksParam), ",")

	log.Printf("🔌 WebSocket client connected, requesting: %v", stockSymbols)

	// Create client connection
	client := &ClientConnection{
		conn:             conn,
		subscribedTokens: make(map[string]bool),
		lastPing:         time.Now(),
		isAlive:          true,
	}

	// Register client
	wsh.clientsMutex.Lock()
	wsh.clients[conn] = client
	wsh.clientsMutex.Unlock()

	// Send init_data for each requested stock
	for _, symbol := range stockSymbols {
		if err := wsh.sendInitData(conn, symbol); err != nil {
			log.Printf("❌ Failed to send init data for %s: %v", symbol, err)
			wsh.sendError(conn, fmt.Sprintf("Failed to load data for %s", symbol))
			continue
		}

		// Mark token as subscribed for this client
		if token, exists := wsh.stockDatabase.GetTokenForSymbol(symbol); exists {
			client.subscribedTokens[token] = true
		}
	}

	// Handle incoming messages (ping/pong, etc.)
	wsh.handleClientMessages(client)

	// Cleanup on disconnect
	wsh.clientsMutex.Lock()
	delete(wsh.clients, conn)
	wsh.clientsMutex.Unlock()

	log.Printf("🔌 WebSocket client disconnected")
}

// sendInitData sends historical candle data for a symbol on connect
func (wsh *WebSocketHandler) sendInitData(conn *websocket.Conn, symbol string) error {
	// Get token and exchange from production stock database
	token, exists := wsh.stockDatabase.GetTokenForSymbol(symbol)
	if !exists {
		return fmt.Errorf("symbol %s not found in stock database", symbol)
	}

	_, exchange, exists := wsh.stockDatabase.GetSymbolForToken(token)
	if !exists {
		return fmt.Errorf("token %s not found in stock database", token)
	}

	// Get current date in IST
	now := time.Now().In(wsh.istLocation)

	// Try to get from cache first (reuse existing REST API logic)
	var candles []candle.Candle
	var dataSource string

	if wsh.redisAdapter != nil {
		cache, cacheErr := wsh.redisAdapter.GetDayCandles(exchange, token, now)
		if cacheErr == nil && cache != nil {
			candles = cache.Candles
			dataSource = "redis_cache"
			log.Printf("📦 Cache hit for %s:%s (%d candles)", exchange, token, len(candles))
		}
	}

	// If no cache, fetch from historical API (reuse existing logic)
	if len(candles) == 0 {
		// Convert exchange format for historical API (NSE -> NSE_EQ, BSE -> BSE_EQ)
		apiExchange := wsh.convertExchangeForAPI(exchange)

		result, err := wsh.historicalClient.FetchAndFillIntradayData(apiExchange, token, symbol, now)
		if err != nil {
			return fmt.Errorf("failed to fetch historical data: %w", err)
		}
		candles = result.Candles
		dataSource = "historical_api"

		// Store in cache for future requests (use original exchange format for cache key)
		if wsh.redisAdapter != nil {
			wsh.redisAdapter.StoreDayCandles(exchange, token, now, candles)
		}

		log.Printf("📈 Fetched from API for %s:%s (%d candles)", apiExchange, token, len(candles))
	}

	// Create market hours for the date
	marketOpen := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, wsh.istLocation)
	marketClose := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, wsh.istLocation)

	// Create init_data message
	initData := InitDataMessage{
		Symbol:       symbol,
		Token:        token,
		Exchange:     exchange,
		Date:         now.Format("2006-01-02"),
		MarketOpen:   marketOpen.Format("2006-01-02T15:04:05-07:00"),
		MarketClose:  marketClose.Format("2006-01-02T15:04:05-07:00"),
		TotalCandles: len(candles),
		Candles:      candles,
	}

	// Send init_data message
	message := WebSocketMessage{
		Type:      "init_data",
		Symbol:    symbol,
		Token:     token,
		Exchange:  exchange,
		Data:      initData,
		Timestamp: time.Now().UnixMilli(),
	}

	if err := conn.WriteJSON(message); err != nil {
		return fmt.Errorf("failed to send init_data: %w", err)
	}

	log.Printf("✅ Sent init_data for %s (%d candles from %s, source: %s)",
		symbol, len(candles), marketOpen.Format("15:04"), dataSource)

	return nil
}

// BroadcastCandleUpdate broadcasts candle updates to subscribed clients
func (wsh *WebSocketHandler) BroadcastCandleUpdate(update candle.CandleUpdate) {
	wsh.clientsMutex.RLock()
	defer wsh.clientsMutex.RUnlock()

	// Get symbol from token using stock database
	symbol, exchange, exists := wsh.stockDatabase.GetSymbolForToken(update.Candle.ScripToken)
	if !exists {
		log.Printf("⚠️ Symbol not found for token %s", update.Candle.ScripToken)
		return
	}

	// Create WebSocket message
	message := WebSocketMessage{
		Type:      fmt.Sprintf("candle:%s", update.Type), // "candle:update", "candle:close", "candle:patch"
		Symbol:    symbol,
		Token:     update.Candle.ScripToken,
		Exchange:  exchange,
		Data:      update.Candle,
		Timestamp: time.Now().UnixMilli(),
	}

	// Broadcast to all clients subscribed to this token
	for conn, client := range wsh.clients {
		if client.subscribedTokens[update.Candle.ScripToken] {
			if err := conn.WriteJSON(message); err != nil {
				log.Printf("⚠️ Failed to send candle update to client: %v", err)
				// Mark client as disconnected
				client.isAlive = false
			}
		}
	}

	log.Printf("📡 Broadcasted %s for %s (%s) to subscribed clients",
		update.Type, symbol, update.Candle.MinuteTS.Format("15:04"))
}

// sendError sends an error message to the client
func (wsh *WebSocketHandler) sendError(conn *websocket.Conn, message string) {
	errorMsg := WebSocketMessage{
		Type:      "error",
		Data:      map[string]string{"message": message},
		Timestamp: time.Now().UnixMilli(),
	}

	conn.WriteJSON(errorMsg)
}

// handleClientMessages handles incoming messages from clients
func (wsh *WebSocketHandler) handleClientMessages(client *ClientConnection) {
	for {
		var msg map[string]interface{}
		err := client.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️ WebSocket error: %v", err)
			}
			break
		}

		// Handle ping messages
		if msgType, ok := msg["type"].(string); ok && msgType == "ping" {
			client.lastPing = time.Now()

			// Send pong response
			pongMsg := WebSocketMessage{
				Type:      "pong",
				Timestamp: time.Now().UnixMilli(),
			}
			client.conn.WriteJSON(pongMsg)
		}
	}
}

// GetConnectedClients returns the number of connected clients
func (wsh *WebSocketHandler) GetConnectedClients() int {
	wsh.clientsMutex.RLock()
	defer wsh.clientsMutex.RUnlock()
	return len(wsh.clients)
}

// GetStats returns WebSocket handler statistics
func (wsh *WebSocketHandler) GetStats() map[string]interface{} {
	wsh.clientsMutex.RLock()
	defer wsh.clientsMutex.RUnlock()

	totalSubscriptions := 0
	for _, client := range wsh.clients {
		totalSubscriptions += len(client.subscribedTokens)
	}

	return map[string]interface{}{
		"connected_clients":   len(wsh.clients),
		"total_subscriptions": totalSubscriptions,
		"timezone":            wsh.istLocation.String(),
		"market_open":         "09:15 IST",
		"market_close":        "15:30 IST",
	}
}

// convertExchangeForAPI converts exchange format for historical API calls
// NSE -> NSE_EQ, BSE -> BSE_EQ
func (wsh *WebSocketHandler) convertExchangeForAPI(exchange string) string {
	switch strings.ToUpper(exchange) {
	case "NSE":
		return "NSE_EQ"
	case "BSE":
		return "BSE_EQ"
	default:
		// Return as-is for any other exchange formats
		return exchange
	}
}

// RegisterRoutes registers WebSocket routes
func (wsh *WebSocketHandler) RegisterRoutes() {
	http.HandleFunc("/stream", wsh.HandleWebSocket)
	log.Printf("✅ WebSocket route registered: /stream?stocks=SYMBOL1,SYMBOL2")
}
