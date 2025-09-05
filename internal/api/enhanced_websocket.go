package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// StockDatabase interface for symbol/token lookups
type StockDatabase interface {
	GetTokenForSymbol(symbol string) (string, bool)
	GetSymbolForToken(token string) (string, string, bool)
}

// Week52Data represents 52-week data structure (imported from stock package)
type Week52Data struct {
	Symbol         string    `json:"symbol"`
	Token          string    `json:"token"`
	Exchange       string    `json:"exchange"`
	Week52High     float64   `json:"week_52_high"`
	Week52Low      float64   `json:"week_52_low"`
	Week52HighDate string    `json:"week_52_high_date"`
	Week52LowDate  string    `json:"week_52_low_date"`
	LastClose      float64   `json:"last_close"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Week52ManagerAdapter wraps the stock.Week52Manager to match our interface
type Week52ManagerAdapter struct {
	manager interface {
		GetWeek52Data(symbol, exchange string) (interface{}, error)
	}
}

// GetWeek52Data adapts the stock.Week52Manager method to our interface
func (w *Week52ManagerAdapter) GetWeek52Data(symbol, exchange string) (*Week52Data, error) {
	data, err := w.manager.GetWeek52Data(symbol, exchange)
	if err != nil {
		return nil, err
	}

	// Convert from stock.Week52Data to api.Week52Data
	if stockData, ok := data.(interface {
		GetSymbol() string
		GetToken() string
		GetExchange() string
		GetWeek52High() float64
		GetWeek52Low() float64
		GetWeek52HighDate() string
		GetWeek52LowDate() string
		GetLastClose() float64
		GetUpdatedAt() time.Time
	}); ok {
		return &Week52Data{
			Symbol:         stockData.GetSymbol(),
			Token:          stockData.GetToken(),
			Exchange:       stockData.GetExchange(),
			Week52High:     stockData.GetWeek52High(),
			Week52Low:      stockData.GetWeek52Low(),
			Week52HighDate: stockData.GetWeek52HighDate(),
			Week52LowDate:  stockData.GetWeek52LowDate(),
			LastClose:      stockData.GetLastClose(),
			UpdatedAt:      stockData.GetUpdatedAt(),
		}, nil
	}

	// Fallback: use reflection or direct field access
	// This is a simple approach - we'll just create the adapter differently
	return nil, fmt.Errorf("unable to convert Week52Data type")
}

// Week52Manager interface for getting 52-week data
type Week52Manager interface {
	GetWeek52Data(symbol, exchange string) (*Week52Data, error)
}

// EnhancedWebSocketHandler handles persistent WebSocket connections with dynamic subscriptions
type EnhancedWebSocketHandler struct {
	upgrader      websocket.Upgrader
	stockDatabase StockDatabase
	week52Manager Week52Manager
	istLocation   *time.Location

	// Client management
	clients      map[string]*EnhancedClient // clientID -> client
	clientsMutex sync.RWMutex

	// Subscription management
	subscriptions     map[string]map[string]bool // token -> clientID -> subscribed
	subscriptionMutex sync.RWMutex

	// Market hours control
	marketOpen   time.Time
	marketClose  time.Time
	isMarketOpen bool
	marketMutex  sync.RWMutex

	// Periodic broadcasting
	periodicBroadcastInterval time.Duration
	periodicBroadcastTicker   *time.Ticker
	periodicBroadcastStop     chan bool
	periodicBroadcastRunning  bool
	periodicMutex             sync.RWMutex
}

// EnhancedClient represents a persistent WebSocket client
type EnhancedClient struct {
	ID               string
	conn             *websocket.Conn
	subscribedTokens map[string]bool // token -> subscribed
	lastPing         time.Time
	isAlive          bool
	createdAt        time.Time
	totalMessages    int64
	mutex            sync.Mutex

	// Client metadata
	userAgent string
	remoteIP  string
}

// WebSocketRequest represents incoming client requests
type WebSocketRequest struct {
	Type      string   `json:"type"`
	Action    string   `json:"action,omitempty"`    // subscribe, unsubscribe, ping
	Stocks    []string `json:"stocks,omitempty"`    // stock symbols or tokens
	ClientID  string   `json:"client_id,omitempty"` // persistent client identifier
	Timestamp int64    `json:"timestamp,omitempty"`
}

// WebSocketResponse represents outgoing server responses
type WebSocketResponse struct {
	Type      string      `json:"type"`
	Status    string      `json:"status,omitempty"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	ClientID  string      `json:"client_id,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// MarketDataMessage represents live market data broadcast
type MarketDataMessage struct {
	Type      string      `json:"type"`
	Symbol    string      `json:"symbol"`
	Token     string      `json:"token"`
	Exchange  string      `json:"exchange"`
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// NewEnhancedWebSocketHandler creates a new enhanced WebSocket handler
func NewEnhancedWebSocketHandler(stockDatabase StockDatabase, week52Manager Week52Manager) (*EnhancedWebSocketHandler, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	handler := &EnhancedWebSocketHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for now
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		stockDatabase:             stockDatabase,
		week52Manager:             week52Manager,
		istLocation:               istLocation,
		clients:                   make(map[string]*EnhancedClient),
		subscriptions:             make(map[string]map[string]bool),
		periodicBroadcastInterval: 2 * time.Minute, // Default: every 2 minutes
		periodicBroadcastStop:     make(chan bool),
		periodicBroadcastRunning:  false,
	}

	// Initialize market hours for today
	handler.updateMarketHours()

	// Start market hours monitor
	go handler.marketHoursMonitor()

	// Start periodic broadcaster
	handler.startPeriodicBroadcaster()

	log.Printf("✅ Enhanced WebSocket handler initialized with market hours control and periodic broadcasting")
	return handler, nil
}

// updateMarketHours sets market open/close times for current day
func (ewsh *EnhancedWebSocketHandler) updateMarketHours() {
	ewsh.marketMutex.Lock()
	defer ewsh.marketMutex.Unlock()

	now := time.Now().In(ewsh.istLocation)

	// Market hours: 9:15 AM to 3:30 PM IST
	ewsh.marketOpen = time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, ewsh.istLocation)
	ewsh.marketClose = time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, ewsh.istLocation)

	// Check if market is currently open
	ewsh.isMarketOpen = now.After(ewsh.marketOpen) && now.Before(ewsh.marketClose)

	log.Printf("📊 Market hours updated: Open=%s, Close=%s, IsOpen=%v",
		ewsh.marketOpen.Format("15:04"), ewsh.marketClose.Format("15:04"), ewsh.isMarketOpen)
}

// marketHoursMonitor monitors market hours and updates status
func (ewsh *EnhancedWebSocketHandler) marketHoursMonitor() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().In(ewsh.istLocation)

		ewsh.marketMutex.Lock()
		wasOpen := ewsh.isMarketOpen
		ewsh.isMarketOpen = now.After(ewsh.marketOpen) && now.Before(ewsh.marketClose)
		ewsh.marketMutex.Unlock()

		// Market status changed
		if wasOpen != ewsh.isMarketOpen {
			if ewsh.isMarketOpen {
				log.Printf("🔔 Market OPENED - Starting data streaming")
				ewsh.broadcastMarketStatus("market_opened", "Market is now open - data streaming started")
			} else {
				log.Printf("🔔 Market CLOSED - Stopping data streaming")
				ewsh.broadcastMarketStatus("market_closed", "Market is now closed - data streaming stopped")
			}
		}

		// Update market hours at midnight
		if now.Hour() == 0 && now.Minute() == 0 {
			ewsh.updateMarketHours()
		}
	}
}

// HandleEnhancedWebSocket handles enhanced WebSocket connections
func (ewsh *EnhancedWebSocketHandler) HandleEnhancedWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := ewsh.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Extract client information
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		clientID = fmt.Sprintf("client_%d", time.Now().UnixNano())
	}

	userAgent := r.Header.Get("User-Agent")
	remoteIP := r.RemoteAddr

	log.Printf("🔌 Enhanced WebSocket client connecting: %s from %s", clientID, remoteIP)

	// Check if client already exists (reconnection)
	ewsh.clientsMutex.Lock()
	existingClient, exists := ewsh.clients[clientID]
	if exists {
		// Close old connection
		existingClient.conn.Close()
		log.Printf("🔄 Client %s reconnected - closing old connection", clientID)
	}

	// Create new client
	client := &EnhancedClient{
		ID:               clientID,
		conn:             conn,
		subscribedTokens: make(map[string]bool),
		lastPing:         time.Now(),
		isAlive:          true,
		createdAt:        time.Now(),
		totalMessages:    0,
		userAgent:        userAgent,
		remoteIP:         remoteIP,
	}

	// If reconnection, restore previous subscriptions
	if exists {
		client.subscribedTokens = existingClient.subscribedTokens
		log.Printf("🔄 Restored %d subscriptions for client %s", len(client.subscribedTokens), clientID)
	}

	ewsh.clients[clientID] = client
	ewsh.clientsMutex.Unlock()

	// Send welcome message
	ewsh.sendWelcomeMessage(client)

	// Handle client messages
	ewsh.handleEnhancedClientMessages(client)

	// Cleanup on disconnect
	ewsh.cleanupClient(client)
}

// sendWelcomeMessage sends initial connection message
func (ewsh *EnhancedWebSocketHandler) sendWelcomeMessage(client *EnhancedClient) {
	ewsh.marketMutex.RLock()
	isMarketOpen := ewsh.isMarketOpen
	marketOpen := ewsh.marketOpen.Format("15:04")
	marketClose := ewsh.marketClose.Format("15:04")
	ewsh.marketMutex.RUnlock()

	response := WebSocketResponse{
		Type:     "welcome",
		Status:   "connected",
		ClientID: client.ID,
		Data: map[string]interface{}{
			"message":           "Connected to Odin Streamer Enhanced WebSocket",
			"client_id":         client.ID,
			"features":          []string{"dynamic_subscriptions", "market_hours_control", "persistent_connection", "real_time_data"},
			"market_hours":      fmt.Sprintf("%s - %s IST", marketOpen, marketClose),
			"market_open":       isMarketOpen,
			"current_time":      time.Now().In(ewsh.istLocation).Format("15:04:05"),
			"subscribed_stocks": len(client.subscribedTokens),
			"instructions": map[string]string{
				"subscribe":   `{"type": "request", "action": "subscribe", "stocks": ["RELIANCE", "TCS"]}`,
				"unsubscribe": `{"type": "request", "action": "unsubscribe", "stocks": ["RELIANCE"]}`,
				"ping":        `{"type": "request", "action": "ping"}`,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()
}

// handleEnhancedClientMessages handles incoming client messages
func (ewsh *EnhancedWebSocketHandler) handleEnhancedClientMessages(client *EnhancedClient) {
	for {
		var request WebSocketRequest
		err := client.conn.ReadJSON(&request)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️ WebSocket error for client %s: %v", client.ID, err)
			}
			break
		}

		client.totalMessages++
		client.lastPing = time.Now()

		// Process request
		ewsh.processClientRequest(client, &request)
	}
}

// processClientRequest processes incoming client requests
func (ewsh *EnhancedWebSocketHandler) processClientRequest(client *EnhancedClient, request *WebSocketRequest) {
	switch request.Action {
	case "subscribe":
		ewsh.handleSubscribe(client, request.Stocks)
	case "unsubscribe":
		ewsh.handleUnsubscribe(client, request.Stocks)
	case "ping":
		ewsh.handlePing(client)
	case "get_subscriptions":
		ewsh.handleGetSubscriptions(client)
	case "market_status":
		ewsh.handleMarketStatus(client)
	default:
		ewsh.sendErrorResponse(client, fmt.Sprintf("Unknown action: %s", request.Action))
	}
}

// handleSubscribe handles stock subscription requests (supports both symbols and tokens)
func (ewsh *EnhancedWebSocketHandler) handleSubscribe(client *EnhancedClient, stocks []string) {
	if len(stocks) == 0 {
		ewsh.sendErrorResponse(client, "No stocks provided for subscription")
		return
	}

	subscribedStocks := []string{}
	failedStocks := []string{}

	for _, stock := range stocks {
		stock = strings.ToUpper(strings.TrimSpace(stock))
		if stock == "" {
			continue
		}

		var token string
		var exists bool
		var displayName string

		// Check if it's a token number (numeric) or symbol
		if isNumeric(stock) {
			// It's a token number - verify it exists
			if symbol, exchange, tokenExists := ewsh.stockDatabase.GetSymbolForToken(stock); tokenExists {
				token = stock
				exists = true
				displayName = fmt.Sprintf("%s (%s)", symbol, exchange)
			} else {
				exists = false
			}
		} else {
			// It's a symbol - get token
			token, exists = ewsh.stockDatabase.GetTokenForSymbol(stock)
			displayName = stock
		}

		if !exists {
			failedStocks = append(failedStocks, stock)
			continue
		}

		// Add to client subscriptions
		client.mutex.Lock()
		client.subscribedTokens[token] = true
		client.mutex.Unlock()

		// Add to global subscriptions
		ewsh.subscriptionMutex.Lock()
		if ewsh.subscriptions[token] == nil {
			ewsh.subscriptions[token] = make(map[string]bool)
		}
		ewsh.subscriptions[token][client.ID] = true
		ewsh.subscriptionMutex.Unlock()

		subscribedStocks = append(subscribedStocks, displayName)
	}

	// Send response
	response := WebSocketResponse{
		Type:     "subscription_response",
		Status:   "success",
		ClientID: client.ID,
		Data: map[string]interface{}{
			"action":            "subscribe",
			"subscribed_stocks": subscribedStocks,
			"failed_stocks":     failedStocks,
			"total_subscribed":  len(client.subscribedTokens),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()

	log.Printf("📊 Client %s subscribed to %d stocks: %v", client.ID, len(subscribedStocks), subscribedStocks)
}

// handleUnsubscribe handles stock unsubscription requests (supports both symbols and tokens)
func (ewsh *EnhancedWebSocketHandler) handleUnsubscribe(client *EnhancedClient, stocks []string) {
	if len(stocks) == 0 {
		ewsh.sendErrorResponse(client, "No stocks provided for unsubscription")
		return
	}

	unsubscribedStocks := []string{}

	for _, stock := range stocks {
		stock = strings.ToUpper(strings.TrimSpace(stock))
		if stock == "" {
			continue
		}

		var token string
		var exists bool
		var displayName string

		// Check if it's a token number (numeric) or symbol
		if isNumeric(stock) {
			// It's a token number - verify it exists
			if symbol, exchange, tokenExists := ewsh.stockDatabase.GetSymbolForToken(stock); tokenExists {
				token = stock
				exists = true
				displayName = fmt.Sprintf("%s (%s)", symbol, exchange)
			} else {
				exists = false
			}
		} else {
			// It's a symbol - get token
			token, exists = ewsh.stockDatabase.GetTokenForSymbol(stock)
			displayName = stock
		}

		if !exists {
			continue
		}

		// Remove from client subscriptions
		client.mutex.Lock()
		if client.subscribedTokens[token] {
			delete(client.subscribedTokens, token)
			unsubscribedStocks = append(unsubscribedStocks, displayName)
		}
		client.mutex.Unlock()

		// Remove from global subscriptions
		ewsh.subscriptionMutex.Lock()
		if ewsh.subscriptions[token] != nil {
			delete(ewsh.subscriptions[token], client.ID)
			// Clean up empty subscription maps
			if len(ewsh.subscriptions[token]) == 0 {
				delete(ewsh.subscriptions, token)
			}
		}
		ewsh.subscriptionMutex.Unlock()
	}

	// Send response
	response := WebSocketResponse{
		Type:     "subscription_response",
		Status:   "success",
		ClientID: client.ID,
		Data: map[string]interface{}{
			"action":              "unsubscribe",
			"unsubscribed_stocks": unsubscribedStocks,
			"total_subscribed":    len(client.subscribedTokens),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()

	log.Printf("📊 Client %s unsubscribed from %d stocks: %v", client.ID, len(unsubscribedStocks), unsubscribedStocks)
}

// handlePing handles ping requests
func (ewsh *EnhancedWebSocketHandler) handlePing(client *EnhancedClient) {
	response := WebSocketResponse{
		Type:      "pong",
		Status:    "alive",
		ClientID:  client.ID,
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()
}

// handleGetSubscriptions returns current subscriptions
func (ewsh *EnhancedWebSocketHandler) handleGetSubscriptions(client *EnhancedClient) {
	client.mutex.Lock()
	tokens := make([]string, 0, len(client.subscribedTokens))
	for token := range client.subscribedTokens {
		tokens = append(tokens, token)
	}
	client.mutex.Unlock()

	// Convert tokens to symbols
	symbols := []string{}
	for _, token := range tokens {
		if symbol, _, exists := ewsh.stockDatabase.GetSymbolForToken(token); exists {
			symbols = append(symbols, symbol)
		}
	}

	response := WebSocketResponse{
		Type:     "subscriptions",
		Status:   "success",
		ClientID: client.ID,
		Data: map[string]interface{}{
			"subscribed_tokens":  tokens,
			"subscribed_symbols": symbols,
			"total_subscribed":   len(tokens),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()
}

// handleMarketStatus returns current market status
func (ewsh *EnhancedWebSocketHandler) handleMarketStatus(client *EnhancedClient) {
	ewsh.marketMutex.RLock()
	isMarketOpen := ewsh.isMarketOpen
	marketOpen := ewsh.marketOpen.Format("15:04")
	marketClose := ewsh.marketClose.Format("15:04")
	ewsh.marketMutex.RUnlock()

	response := WebSocketResponse{
		Type:     "market_status",
		Status:   "success",
		ClientID: client.ID,
		Data: map[string]interface{}{
			"market_open":    isMarketOpen,
			"market_hours":   fmt.Sprintf("%s - %s IST", marketOpen, marketClose),
			"current_time":   time.Now().In(ewsh.istLocation).Format("15:04:05"),
			"next_open":      ewsh.getNextMarketOpen().Format("2006-01-02 15:04:05"),
			"streaming_data": isMarketOpen,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()
}

// BroadcastMarketData broadcasts market data to subscribed clients (only during market hours)
func (ewsh *EnhancedWebSocketHandler) BroadcastMarketData(token string, symbol string, exchange string, data interface{}) {
	// Check if market is open
	ewsh.marketMutex.RLock()
	if !ewsh.isMarketOpen {
		ewsh.marketMutex.RUnlock()
		return // Don't broadcast outside market hours
	}
	ewsh.marketMutex.RUnlock()

	// Get subscribed clients for this token
	ewsh.subscriptionMutex.RLock()
	subscribedClients, exists := ewsh.subscriptions[token]
	if !exists || len(subscribedClients) == 0 {
		ewsh.subscriptionMutex.RUnlock()
		return
	}
	ewsh.subscriptionMutex.RUnlock()

	// Create market data message
	message := MarketDataMessage{
		Type:      "market_data",
		Symbol:    symbol,
		Token:     token,
		Exchange:  exchange,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	// Broadcast to subscribed clients
	ewsh.clientsMutex.RLock()
	for clientID := range subscribedClients {
		if client, exists := ewsh.clients[clientID]; exists && client.isAlive {
			client.mutex.Lock()
			if err := client.conn.WriteJSON(message); err != nil {
				client.isAlive = false
				log.Printf("⚠️ Failed to send data to client %s: %v", clientID, err)
			}
			client.mutex.Unlock()
		}
	}
	ewsh.clientsMutex.RUnlock()
}

// broadcastMarketStatus broadcasts market status changes to all clients
func (ewsh *EnhancedWebSocketHandler) broadcastMarketStatus(statusType, message string) {
	response := WebSocketResponse{
		Type:    statusType,
		Status:  "info",
		Message: message,
		Data: map[string]interface{}{
			"market_open":  ewsh.isMarketOpen,
			"current_time": time.Now().In(ewsh.istLocation).Format("15:04:05"),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	ewsh.clientsMutex.RLock()
	for _, client := range ewsh.clients {
		if client.isAlive {
			client.mutex.Lock()
			client.conn.WriteJSON(response)
			client.mutex.Unlock()
		}
	}
	ewsh.clientsMutex.RUnlock()
}

// sendErrorResponse sends error response to client
func (ewsh *EnhancedWebSocketHandler) sendErrorResponse(client *EnhancedClient, message string) {
	response := WebSocketResponse{
		Type:      "error",
		Status:    "error",
		Message:   message,
		ClientID:  client.ID,
		Timestamp: time.Now().UnixMilli(),
	}

	client.mutex.Lock()
	client.conn.WriteJSON(response)
	client.mutex.Unlock()
}

// cleanupClient cleans up client resources on disconnect
func (ewsh *EnhancedWebSocketHandler) cleanupClient(client *EnhancedClient) {
	log.Printf("🔌 Client %s disconnected after %v", client.ID, time.Since(client.createdAt))

	// Remove from clients map
	ewsh.clientsMutex.Lock()
	delete(ewsh.clients, client.ID)
	ewsh.clientsMutex.Unlock()

	// Remove from all subscriptions
	ewsh.subscriptionMutex.Lock()
	for token := range ewsh.subscriptions {
		delete(ewsh.subscriptions[token], client.ID)
		// Clean up empty subscription maps
		if len(ewsh.subscriptions[token]) == 0 {
			delete(ewsh.subscriptions, token)
		}
	}
	ewsh.subscriptionMutex.Unlock()
}

// getNextMarketOpen returns the next market opening time
func (ewsh *EnhancedWebSocketHandler) getNextMarketOpen() time.Time {
	now := time.Now().In(ewsh.istLocation)
	nextOpen := time.Date(now.Year(), now.Month(), now.Day(), 9, 15, 0, 0, ewsh.istLocation)

	// If today's market has already opened, get tomorrow's opening
	if now.After(nextOpen) {
		nextOpen = nextOpen.Add(24 * time.Hour)
	}

	return nextOpen
}

// startPeriodicBroadcaster starts the periodic 52-week data broadcaster
func (ewsh *EnhancedWebSocketHandler) startPeriodicBroadcaster() {
	ewsh.periodicMutex.Lock()
	defer ewsh.periodicMutex.Unlock()

	if ewsh.periodicBroadcastRunning {
		return
	}

	ewsh.periodicBroadcastTicker = time.NewTicker(ewsh.periodicBroadcastInterval)
	ewsh.periodicBroadcastRunning = true

	go ewsh.periodicBroadcastLoop()

	log.Printf("📡 Periodic 52-week data broadcaster started (interval: %v)", ewsh.periodicBroadcastInterval)
}

// stopPeriodicBroadcaster stops the periodic broadcaster
func (ewsh *EnhancedWebSocketHandler) stopPeriodicBroadcaster() {
	ewsh.periodicMutex.Lock()
	defer ewsh.periodicMutex.Unlock()

	if !ewsh.periodicBroadcastRunning {
		return
	}

	ewsh.periodicBroadcastRunning = false
	if ewsh.periodicBroadcastTicker != nil {
		ewsh.periodicBroadcastTicker.Stop()
	}
	ewsh.periodicBroadcastStop <- true

	log.Printf("📡 Periodic broadcaster stopped")
}

// periodicBroadcastLoop runs the periodic broadcasting loop
func (ewsh *EnhancedWebSocketHandler) periodicBroadcastLoop() {
	log.Printf("📡 Periodic broadcast loop started - sending 52-week data every %v", ewsh.periodicBroadcastInterval)

	for {
		select {
		case <-ewsh.periodicBroadcastTicker.C:
			ewsh.performPeriodicBroadcast()

		case <-ewsh.periodicBroadcastStop:
			log.Printf("📡 Periodic broadcast loop stopped")
			return
		}
	}
}

// performPeriodicBroadcast sends 52-week data for all subscribed tokens
func (ewsh *EnhancedWebSocketHandler) performPeriodicBroadcast() {
	startTime := time.Now()

	// Get all subscribed tokens
	ewsh.subscriptionMutex.RLock()
	subscribedTokens := make([]string, 0, len(ewsh.subscriptions))
	for token := range ewsh.subscriptions {
		if len(ewsh.subscriptions[token]) > 0 { // Has active subscribers
			subscribedTokens = append(subscribedTokens, token)
		}
	}
	ewsh.subscriptionMutex.RUnlock()

	if len(subscribedTokens) == 0 {
		return // No active subscriptions
	}

	log.Printf("📡 Broadcasting 52-week data for %d subscribed tokens", len(subscribedTokens))

	broadcastCount := 0
	errorCount := 0

	for _, token := range subscribedTokens {
		// Get symbol and exchange for token
		symbol, exchange, exists := ewsh.stockDatabase.GetSymbolForToken(token)
		if !exists {
			errorCount++
			continue
		}

		// Get 52-week data from database
		week52Data, err := ewsh.week52Manager.GetWeek52Data(symbol, exchange)
		if err != nil {
			errorCount++
			continue
		}

		// Create periodic market data message
		periodicData := ewsh.createPeriodicMarketData(week52Data, startTime)

		// Broadcast to subscribed clients
		ewsh.broadcastPeriodicData(token, periodicData)
		broadcastCount++
	}

	duration := time.Since(startTime)
	if broadcastCount > 0 {
		log.Printf("📡 Periodic broadcast completed: %d stocks, %d errors, took %v",
			broadcastCount, errorCount, duration)
	}
}

// createPeriodicMarketData creates periodic market data from 52-week data
func (ewsh *EnhancedWebSocketHandler) createPeriodicMarketData(week52Data *Week52Data, broadcastTime time.Time) map[string]interface{} {
	// Check if data is recent (within last 24 hours)
	hasRecentData := time.Since(week52Data.UpdatedAt) < 24*time.Hour

	return map[string]interface{}{
		"symbol":            week52Data.Symbol,
		"token":             week52Data.Token,
		"exchange":          week52Data.Exchange,
		"week_52_high":      week52Data.Week52High,
		"week_52_low":       week52Data.Week52Low,
		"week_52_high_date": week52Data.Week52HighDate,
		"week_52_low_date":  week52Data.Week52LowDate,
		"last_close":        week52Data.LastClose,
		"day_high":          week52Data.Week52High, // Use 52-week high as day high if no recent data
		"day_low":           week52Data.Week52Low,  // Use 52-week low as day low if no recent data
		"data_type":         "periodic_52week",
		"broadcast_time":    broadcastTime.Format(time.RFC3339),
		"last_updated":      week52Data.UpdatedAt.Format(time.RFC3339),
		"timestamp":         broadcastTime.UnixMilli(),
		"has_recent_data":   hasRecentData,
		"ltp":               week52Data.LastClose, // Use last close as LTP for consistency
		"volume":            0,                    // No volume data for periodic updates
		"percent_change":    0.0,                  // No percent change for periodic updates
	}
}

// broadcastPeriodicData broadcasts periodic data to subscribed clients
func (ewsh *EnhancedWebSocketHandler) broadcastPeriodicData(token string, data map[string]interface{}) {
	// Get subscribed clients for this token
	ewsh.subscriptionMutex.RLock()
	subscribedClients, exists := ewsh.subscriptions[token]
	if !exists || len(subscribedClients) == 0 {
		ewsh.subscriptionMutex.RUnlock()
		return
	}
	ewsh.subscriptionMutex.RUnlock()

	// Create periodic data message
	message := MarketDataMessage{
		Type:      "periodic_52week_data",
		Symbol:    data["symbol"].(string),
		Token:     token,
		Exchange:  data["exchange"].(string),
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	// Broadcast to subscribed clients
	ewsh.clientsMutex.RLock()
	for clientID := range subscribedClients {
		if client, exists := ewsh.clients[clientID]; exists && client.isAlive {
			client.mutex.Lock()
			if err := client.conn.WriteJSON(message); err != nil {
				client.isAlive = false
				log.Printf("⚠️ Failed to send periodic data to client %s: %v", clientID, err)
			}
			client.mutex.Unlock()
		}
	}
	ewsh.clientsMutex.RUnlock()
}

// GetStats returns enhanced WebSocket handler statistics
func (ewsh *EnhancedWebSocketHandler) GetStats() map[string]interface{} {
	ewsh.clientsMutex.RLock()
	ewsh.subscriptionMutex.RLock()
	ewsh.marketMutex.RLock()
	ewsh.periodicMutex.RLock()
	defer ewsh.clientsMutex.RUnlock()
	defer ewsh.subscriptionMutex.RUnlock()
	defer ewsh.marketMutex.RUnlock()
	defer ewsh.periodicMutex.RUnlock()

	totalSubscriptions := 0
	for _, clients := range ewsh.subscriptions {
		totalSubscriptions += len(clients)
	}

	return map[string]interface{}{
		"connected_clients":           len(ewsh.clients),
		"total_subscriptions":         totalSubscriptions,
		"unique_tokens":               len(ewsh.subscriptions),
		"market_open":                 ewsh.isMarketOpen,
		"market_hours":                fmt.Sprintf("%s - %s", ewsh.marketOpen.Format("15:04"), ewsh.marketClose.Format("15:04")),
		"timezone":                    ewsh.istLocation.String(),
		"streaming_active":            ewsh.isMarketOpen,
		"next_market_open":            ewsh.getNextMarketOpen().Format("2006-01-02 15:04:05"),
		"periodic_broadcast_active":   ewsh.periodicBroadcastRunning,
		"periodic_broadcast_interval": ewsh.periodicBroadcastInterval.String(),
	}
}

// isNumeric checks if a string contains only numeric characters
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, char := range s {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// RegisterEnhancedRoutes registers enhanced WebSocket routes
func (ewsh *EnhancedWebSocketHandler) RegisterEnhancedRoutes() {
	http.HandleFunc("/enhanced-stream", ewsh.HandleEnhancedWebSocket)
	log.Printf("✅ Enhanced WebSocket route registered: /enhanced-stream?client_id=YOUR_CLIENT_ID")
}
