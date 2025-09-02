package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang-market-service/internal/historical"
	"golang-market-service/internal/storage"

	"github.com/gorilla/websocket"
)

// EnhancedWebSocketHandler handles persistent WebSocket connections with dynamic subscriptions
type EnhancedWebSocketHandler struct {
	upgrader         websocket.Upgrader
	redisAdapter     *storage.RedisAdapter
	timescaleAdapter *storage.TimescaleDBAdapter
	historicalClient *historical.IndiraTradeClient
	stockDatabase    StockDatabase
	istLocation      *time.Location

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
func NewEnhancedWebSocketHandler(redisAdapter *storage.RedisAdapter, timescaleAdapter *storage.TimescaleDBAdapter, historicalClient *historical.IndiraTradeClient, stockDatabase StockDatabase) (*EnhancedWebSocketHandler, error) {
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
		redisAdapter:     redisAdapter,
		timescaleAdapter: timescaleAdapter,
		historicalClient: historicalClient,
		stockDatabase:    stockDatabase,
		istLocation:      istLocation,
		clients:          make(map[string]*EnhancedClient),
		subscriptions:    make(map[string]map[string]bool),
	}

	// Initialize market hours for today
	handler.updateMarketHours()

	// Start market hours monitor
	go handler.marketHoursMonitor()

	log.Printf("✅ Enhanced WebSocket handler initialized with market hours control")
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

// handleSubscribe handles stock subscription requests
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

		// Get token for symbol
		token, exists := ewsh.stockDatabase.GetTokenForSymbol(stock)
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

		subscribedStocks = append(subscribedStocks, stock)
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

// handleUnsubscribe handles stock unsubscription requests
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

		// Get token for symbol
		token, exists := ewsh.stockDatabase.GetTokenForSymbol(stock)
		if !exists {
			continue
		}

		// Remove from client subscriptions
		client.mutex.Lock()
		if client.subscribedTokens[token] {
			delete(client.subscribedTokens, token)
			unsubscribedStocks = append(unsubscribedStocks, stock)
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

// GetStats returns enhanced WebSocket handler statistics
func (ewsh *EnhancedWebSocketHandler) GetStats() map[string]interface{} {
	ewsh.clientsMutex.RLock()
	ewsh.subscriptionMutex.RLock()
	ewsh.marketMutex.RLock()
	defer ewsh.clientsMutex.RUnlock()
	defer ewsh.subscriptionMutex.RUnlock()
	defer ewsh.marketMutex.RUnlock()

	totalSubscriptions := 0
	for _, clients := range ewsh.subscriptions {
		totalSubscriptions += len(clients)
	}

	return map[string]interface{}{
		"connected_clients":   len(ewsh.clients),
		"total_subscriptions": totalSubscriptions,
		"unique_tokens":       len(ewsh.subscriptions),
		"market_open":         ewsh.isMarketOpen,
		"market_hours":        fmt.Sprintf("%s - %s", ewsh.marketOpen.Format("15:04"), ewsh.marketClose.Format("15:04")),
		"timezone":            ewsh.istLocation.String(),
		"streaming_active":    ewsh.isMarketOpen,
		"next_market_open":    ewsh.getNextMarketOpen().Format("2006-01-02 15:04:05"),
	}
}

// RegisterEnhancedRoutes registers enhanced WebSocket routes
func (ewsh *EnhancedWebSocketHandler) RegisterEnhancedRoutes() {
	http.HandleFunc("/enhanced-stream", ewsh.HandleEnhancedWebSocket)
	log.Printf("✅ Enhanced WebSocket route registered: /enhanced-stream?client_id=YOUR_CLIENT_ID")
}
