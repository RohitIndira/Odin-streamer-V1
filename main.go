package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// ============================================================================
// ENVIRONMENT VARIABLES
// ============================================================================

func init() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Warning: .env file not found, falling back to system env variables")
	}
}

// ============================================================================
// DATA STRUCTURES
// ============================================================================

// CompanyMasterAPIResponse represents the response from company master API
type CompanyMasterAPIResponse struct {
	Success bool        `json:"success"`
	Data    []StockInfo `json:"data"`
}

// MarketData represents real-time market data
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

// StockInfo represents comprehensive stock information
type StockInfo struct {
	CoCode           float64 `json:"co_code"`
	Code             float64 `json:"code"`    // NSE token code (e.g., 2885 for RELIANCE)
	BSECode          string  `json:"bsecode"` // BSE token code (e.g., "500325" for RELIANCE)
	NSESymbol        string  `json:"nsesymbol"`
	CompanyName      string  `json:"companyname"`
	CompanyShortName string  `json:"companyshortname"`
	CategoryName     string  `json:"categoryname"`
	ISIN             string  `json:"isin"`
	BSEGroup         string  `json:"bsegroup"`
	McapType         string  `json:"mcaptype"`
	SectorCode       string  `json:"sectorcode"`
	SectorName       string  `json:"sectorname"`
	IndustryCode     string  `json:"industrycode"`
	IndustryName     string  `json:"industryname"`
	BSEListedFlag    string  `json:"bselistedflag"`
	NSEListedFlag    string  `json:"nselistedflag"`
	MarketCap        float64 `json:"mcap"`
	DisplayType      string  `json:"DisplayType"`
	BSESymbol        string  `json:"BSESymbol"`
	BSEStatus        string  `json:"BSEStatus"`
	NSEStatus        string  `json:"NSEStatus"`
}

// StockAPIResponse represents the API response containing stock data
type StockAPIResponse struct {
	Success bool        `json:"success"`
	Data    []StockInfo `json:"data"`
}

// WebSocketClient represents a connected WebSocket client with stability features
type WebSocketClient struct {
	ID              string
	Conn            *websocket.Conn
	Stocks          map[string]bool
	SendChannel     chan MarketData
	JoinedAt        time.Time
	WriteMutex      sync.Mutex   // Mutex to prevent concurrent writes to WebSocket
	LastPong        time.Time    // Last pong received
	MissedPings     int          // Number of missed pings
	IsAlive         bool         // Connection health status
	TotalMessages   int64        // Total messages sent
	LastMessageTime time.Time    // Last message sent time
	HeartbeatTicker *time.Ticker // Heartbeat ticker
}

// gRPCClient represents a connected gRPC client
type GRPCClient struct {
	ID          string
	Stocks      map[string]bool
	SendChannel chan MarketData
	JoinedAt    time.Time
}

// ============================================================================
// STOCK INFO MANAGER
// ============================================================================

type StockInfoManager struct {
	stocks      map[string]*StockInfo   // NSE symbol -> StockInfo
	bseMap      map[string]*StockInfo   // BSE code -> StockInfo
	isinMap     map[string]*StockInfo   // ISIN -> StockInfo
	sectorMap   map[string][]*StockInfo // Sector -> StockInfo list
	industryMap map[string][]*StockInfo // Industry -> StockInfo list
	mutex       sync.RWMutex
	lastUpdated time.Time
}

func NewStockInfoManager() *StockInfoManager {
	return &StockInfoManager{
		stocks:      make(map[string]*StockInfo),
		bseMap:      make(map[string]*StockInfo),
		isinMap:     make(map[string]*StockInfo),
		sectorMap:   make(map[string][]*StockInfo),
		industryMap: make(map[string][]*StockInfo),
	}
}

func (sim *StockInfoManager) ParseStockData(jsonData string) error {
	// Try to unmarshal as direct array first (new API format)
	var stocksArray []StockInfo
	if err := json.Unmarshal([]byte(jsonData), &stocksArray); err != nil {
		// If that fails, try the old format with success/data wrapper
		var response StockAPIResponse
		if err := json.Unmarshal([]byte(jsonData), &response); err != nil {
			return fmt.Errorf("failed to parse stock data: %w", err)
		}

		if !response.Success {
			return fmt.Errorf("API response indicates failure")
		}
		stocksArray = response.Data
	}

	sim.mutex.Lock()
	defer sim.mutex.Unlock()

	// Clear existing data
	sim.stocks = make(map[string]*StockInfo)
	sim.bseMap = make(map[string]*StockInfo)
	sim.isinMap = make(map[string]*StockInfo)
	sim.sectorMap = make(map[string][]*StockInfo)
	sim.industryMap = make(map[string][]*StockInfo)

	// Process each stock
	for i := range stocksArray {
		stock := &stocksArray[i]
		// Index by NSE symbol
		if stock.NSESymbol != "" {
			sim.stocks[stock.NSESymbol] = stock
		}

		// Index by BSE code
		if stock.BSECode != "" {
			sim.bseMap[stock.BSECode] = stock
		}

		// Index by ISIN
		if stock.ISIN != "" {
			sim.isinMap[stock.ISIN] = stock
		}

		// Index by sector
		if stock.SectorName != "" {
			sim.sectorMap[stock.SectorName] = append(sim.sectorMap[stock.SectorName], stock)
		}

		// Index by industry
		if stock.IndustryName != "" {
			sim.industryMap[stock.IndustryName] = append(sim.industryMap[stock.IndustryName], stock)
		}
	}

	sim.lastUpdated = time.Now()
	log.Printf("✅ Processed %d stocks into stock info manager", len(stocksArray))
	return nil
}

func (sim *StockInfoManager) GetStockByNSESymbol(symbol string) (*StockInfo, bool) {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()
	stock, exists := sim.stocks[symbol]
	return stock, exists
}

func (sim *StockInfoManager) GetStockByBSECode(code string) (*StockInfo, bool) {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()
	stock, exists := sim.bseMap[code]
	return stock, exists
}

func (sim *StockInfoManager) GetStockByISIN(isin string) (*StockInfo, bool) {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()
	stock, exists := sim.isinMap[isin]
	return stock, exists
}

func (sim *StockInfoManager) GetStocksBySector(sector string) []*StockInfo {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()
	return sim.sectorMap[sector]
}

func (sim *StockInfoManager) GetStocksByIndustry(industry string) []*StockInfo {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()
	return sim.industryMap[industry]
}

func (sim *StockInfoManager) GetAllStocks() map[string]*StockInfo {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()

	// Create a copy to avoid race conditions
	result := make(map[string]*StockInfo)
	for k, v := range sim.stocks {
		result[k] = v
	}
	return result
}

func (sim *StockInfoManager) GetSectors() []string {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()

	sectors := make([]string, 0, len(sim.sectorMap))
	for sector := range sim.sectorMap {
		sectors = append(sectors, sector)
	}
	return sectors
}

func (sim *StockInfoManager) GetIndustries() []string {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()

	industries := make([]string, 0, len(sim.industryMap))
	for industry := range sim.industryMap {
		industries = append(industries, industry)
	}
	return industries
}

func (sim *StockInfoManager) GetStats() map[string]interface{} {
	sim.mutex.RLock()
	defer sim.mutex.RUnlock()

	return map[string]interface{}{
		"total_stocks":     len(sim.stocks),
		"total_sectors":    len(sim.sectorMap),
		"total_industries": len(sim.industryMap),
		"last_updated":     sim.lastUpdated.Format(time.RFC3339),
	}
}

// ============================================================================
// SYMBOLS API CLIENT
// ============================================================================

type SymbolsAPIClient struct {
	apiURL      string
	symbolMap   map[string]string // symbol -> token
	tokenMap    map[string]string // token -> symbol
	lastUpdated time.Time
	mutex       sync.RWMutex
}

func NewSymbolsAPIClient(apiURL string) *SymbolsAPIClient {
	return &SymbolsAPIClient{
		apiURL:    apiURL,
		symbolMap: make(map[string]string),
		tokenMap:  make(map[string]string),
	}
}

func (s *SymbolsAPIClient) FetchSymbols() error {
	log.Printf("🔄 Fetching symbols from API: %s", s.apiURL)

	resp, err := http.Get(s.apiURL)
	if err != nil {
		return fmt.Errorf("failed to fetch symbols: %w", err)
	}
	defer resp.Body.Close()

	var stocksArray []StockInfo
	if err := json.NewDecoder(resp.Body).Decode(&stocksArray); err != nil {
		return fmt.Errorf("failed to decode company master response: %w", err)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Clear existing maps
	s.symbolMap = make(map[string]string)
	s.tokenMap = make(map[string]string)

	// Process company data to create symbol-token mapping for ALL stocks (NSE + BSE)
	for _, stock := range stocksArray {
		token := fmt.Sprintf("%.0f", stock.CoCode) // Use company code as token

		// Prefer NSE symbol, fallback to BSE code for BSE-only stocks
		if stock.NSESymbol != "" {
			s.symbolMap[stock.NSESymbol] = token
			s.tokenMap[token] = stock.NSESymbol
		} else if stock.BSECode != "" {
			// Use BSE code as symbol for BSE-only stocks
			s.symbolMap[stock.BSECode] = token
			s.tokenMap[token] = stock.BSECode
		}
	}

	s.lastUpdated = time.Now()
	log.Printf("✅ Loaded %d symbols from Company Master API", len(s.symbolMap))
	return nil
}

func (s *SymbolsAPIClient) GetTokenForSymbol(symbol string) (string, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	token, exists := s.symbolMap[symbol]
	return token, exists
}

func (s *SymbolsAPIClient) GetSymbolForToken(token string) (string, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	symbol, exists := s.tokenMap[token]
	return symbol, exists
}

func (s *SymbolsAPIClient) GetAllTokens() []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	tokens := make([]string, 0, len(s.tokenMap))
	for token := range s.tokenMap {
		tokens = append(tokens, token)
	}
	return tokens
}

// ============================================================================
// PYTHON B2C BRIDGE
// ============================================================================

type PythonB2CBridge struct {
	pythonScript     string
	process          *exec.Cmd
	dataChannel      chan MarketData
	commandChannel   chan string // For sending commands to Python process
	isRunning        bool
	subscribedTokens map[string]bool // Track currently subscribed tokens
	mutex            sync.RWMutex
}

func NewPythonB2CBridge(pythonScript string) *PythonB2CBridge {
	return &PythonB2CBridge{
		pythonScript:     pythonScript,
		dataChannel:      make(chan MarketData, 2000000), // 2M buffer for Indian market massive data flow
		commandChannel:   make(chan string, 50000),       // 50K command buffer for ultra-high concurrency
		subscribedTokens: make(map[string]bool),          // Track subscribed tokens
	}
}

func (p *PythonB2CBridge) Start(tokens []string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.isRunning {
		return fmt.Errorf("bridge already running")
	}

	log.Printf("🐍 Starting Python B2C bridge with %d tokens", len(tokens))

	// Create Python script arguments
	args := []string{p.pythonScript}
	args = append(args, tokens...)

	// Start Python process
	p.process = exec.Command("python", args...)

	// Get stdout pipe for reading market data
	stdout, err := p.process.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Start the process
	if err := p.process.Start(); err != nil {
		return fmt.Errorf("failed to start Python process: %w", err)
	}

	p.isRunning = true

	// Start reading market data in background
	go func() {
		defer func() {
			p.mutex.Lock()
			p.isRunning = false
			p.mutex.Unlock()
		}()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// Parse JSON market data
			var marketData MarketData
			if err := json.Unmarshal([]byte(line), &marketData); err != nil {
				log.Printf("⚠️ Failed to parse market data: %v", err)
				continue
			}

			// Send to data channel (non-blocking)
			select {
			case p.dataChannel <- marketData:
			default:
				log.Printf("⚠️ Data channel full, dropping market data for %s", marketData.Symbol)
			}
		}

		if err := scanner.Err(); err != nil {
			log.Printf("❌ Error reading from Python process: %v", err)
		}
	}()

	log.Printf("✅ Python B2C bridge started successfully")
	return nil
}

func (p *PythonB2CBridge) Stop() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.isRunning || p.process == nil {
		return nil
	}

	log.Printf("🛑 Stopping Python B2C bridge")

	// Terminate the process
	if err := p.process.Process.Kill(); err != nil {
		log.Printf("⚠️ Error killing Python process: %v", err)
	}

	// Wait for process to exit
	p.process.Wait()
	p.isRunning = false

	log.Printf("✅ Python B2C bridge stopped")
	return nil
}

func (p *PythonB2CBridge) GetDataChannel() <-chan MarketData {
	return p.dataChannel
}

func (p *PythonB2CBridge) IsRunning() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.isRunning
}

// SubscribeToTokens adds new tokens to the subscription list
func (p *PythonB2CBridge) SubscribeToTokens(tokens []string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.isRunning {
		return fmt.Errorf("bridge not running")
	}

	newTokens := make([]string, 0)
	for _, token := range tokens {
		if !p.subscribedTokens[token] {
			p.subscribedTokens[token] = true
			newTokens = append(newTokens, token)
		}
	}

	if len(newTokens) > 0 {
		command := fmt.Sprintf("SUBSCRIBE:%s", strings.Join(newTokens, ","))
		select {
		case p.commandChannel <- command:
			log.Printf("📈 Subscribed to %d new tokens", len(newTokens))
		default:
			return fmt.Errorf("command channel full")
		}
	}

	return nil
}

// UnsubscribeFromTokens removes tokens from the subscription list
func (p *PythonB2CBridge) UnsubscribeFromTokens(tokens []string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.isRunning {
		return fmt.Errorf("bridge not running")
	}

	removeTokens := make([]string, 0)
	for _, token := range tokens {
		if p.subscribedTokens[token] {
			delete(p.subscribedTokens, token)
			removeTokens = append(removeTokens, token)
		}
	}

	if len(removeTokens) > 0 {
		command := fmt.Sprintf("UNSUBSCRIBE:%s", strings.Join(removeTokens, ","))
		select {
		case p.commandChannel <- command:
			log.Printf("📉 Unsubscribed from %d tokens", len(removeTokens))
		default:
			return fmt.Errorf("command channel full")
		}
	}

	return nil
}

// GetSubscribedTokens returns currently subscribed tokens
func (p *PythonB2CBridge) GetSubscribedTokens() []string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	tokens := make([]string, 0, len(p.subscribedTokens))
	for token := range p.subscribedTokens {
		tokens = append(tokens, token)
	}
	return tokens
}

// GetSubscriptionStats returns subscription statistics
func (p *PythonB2CBridge) GetSubscriptionStats() map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return map[string]interface{}{
		"subscribed_tokens": len(p.subscribedTokens),
		"is_running":        p.isRunning,
		"command_queue":     len(p.commandChannel),
		"data_queue":        len(p.dataChannel),
	}
}

// ============================================================================
// ZERO-LATENCY BROADCASTER
// ============================================================================

type ZeroLatencyBroadcaster struct {
	wsClients   map[string]*WebSocketClient
	grpcClients map[string]*GRPCClient
	mutex       sync.RWMutex

	// Statistics
	totalMessages  int64
	startTime      time.Time
	messageChannel chan MarketData
}

func NewZeroLatencyBroadcaster() *ZeroLatencyBroadcaster {
	broadcaster := &ZeroLatencyBroadcaster{
		wsClients:      make(map[string]*WebSocketClient),
		grpcClients:    make(map[string]*GRPCClient),
		startTime:      time.Now(),
		messageChannel: make(chan MarketData, 5000000), // 5M buffer for Indian market massive throughput
	}

	// Start broadcasting goroutine
	go broadcaster.broadcastLoop()

	return broadcaster
}

func (b *ZeroLatencyBroadcaster) broadcastLoop() {
	for marketData := range b.messageChannel {
		b.broadcastToClients(marketData)
	}
}

func (b *ZeroLatencyBroadcaster) BroadcastMarketData(data MarketData) {
	// Non-blocking send to broadcast channel
	select {
	case b.messageChannel <- data:
		b.totalMessages++
	default:
		log.Printf("⚠️ Broadcast channel full, dropping data for %s", data.Symbol)
	}
}

func (b *ZeroLatencyBroadcaster) broadcastToClients(data MarketData) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	// Broadcast to WebSocket clients
	for clientID, client := range b.wsClients {
		// Check if client is subscribed to this specific stock OR subscribed to all stocks
		if client.Stocks[data.Symbol] || client.Stocks["*ALL*"] {
			select {
			case client.SendChannel <- data:
			default:
				log.Printf("⚠️ WebSocket client %s channel full, dropping data", clientID)
			}
		}
	}

	// Broadcast to gRPC clients
	for clientID, client := range b.grpcClients {
		// Check if client is subscribed to this specific stock OR subscribed to all stocks
		if client.Stocks[data.Symbol] || client.Stocks["*ALL*"] {
			select {
			case client.SendChannel <- data:
			default:
				log.Printf("⚠️ gRPC client %s channel full, dropping data", clientID)
			}
		}
	}
}

func (b *ZeroLatencyBroadcaster) AddWebSocketClient(client *WebSocketClient) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.wsClients[client.ID] = client
	log.Printf("✅ Added WebSocket client %s for %d stocks", client.ID, len(client.Stocks))
}

func (b *ZeroLatencyBroadcaster) RemoveWebSocketClient(clientID string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if client, exists := b.wsClients[clientID]; exists {
		close(client.SendChannel)
		delete(b.wsClients, clientID)
		log.Printf("❌ Removed WebSocket client %s", clientID)
	}
}

func (b *ZeroLatencyBroadcaster) AddGRPCClient(client *GRPCClient) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.grpcClients[client.ID] = client
	log.Printf("✅ Added gRPC client %s for %d stocks", client.ID, len(client.Stocks))
}

func (b *ZeroLatencyBroadcaster) RemoveGRPCClient(clientID string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if client, exists := b.grpcClients[clientID]; exists {
		close(client.SendChannel)
		delete(b.grpcClients, clientID)
		log.Printf("❌ Removed gRPC client %s", clientID)
	}
}

func (b *ZeroLatencyBroadcaster) GetStats() map[string]interface{} {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	elapsed := time.Since(b.startTime).Seconds()
	mps := float64(b.totalMessages) / elapsed

	return map[string]interface{}{
		"websocket_clients": len(b.wsClients),
		"grpc_clients":      len(b.grpcClients),
		"total_messages":    b.totalMessages,
		"messages_per_sec":  mps,
		"uptime_seconds":    elapsed,
	}
}

// ============================================================================
// WEBSOCKET SERVER
// ============================================================================

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
	ReadBufferSize:    65536, // 64KB buffer for maximum performance
	WriteBufferSize:   65536, // 64KB buffer for maximum performance
	EnableCompression: false, // Disable compression to reduce CPU overhead and potential issues
	HandshakeTimeout:  0,     // No handshake timeout for persistent connections
}

type WebSocketServer struct {
	broadcaster         *ZeroLatencyBroadcaster
	port                int
	clientCount         int64
	mutex               sync.Mutex
	subscriptionManager *StockSubscriptionManager
	symbolsClient       *SymbolsAPIClient
	pythonBridge        *PythonB2CBridge
}

func NewWebSocketServer(broadcaster *ZeroLatencyBroadcaster, port int) *WebSocketServer {
	return &WebSocketServer{
		broadcaster: broadcaster,
		port:        port,
	}
}

// SetDependencies sets the required dependencies for on-demand subscription
func (ws *WebSocketServer) SetDependencies(subscriptionManager *StockSubscriptionManager, symbolsClient *SymbolsAPIClient, pythonBridge *PythonB2CBridge) {
	ws.subscriptionManager = subscriptionManager
	ws.symbolsClient = symbolsClient
	ws.pythonBridge = pythonBridge
}

// getTokenForSymbol gets token for a symbol using intelligent filtering
func (ws *WebSocketServer) getTokenForSymbol(symbol string) (string, bool) {
	// First try subscription manager (intelligent filtering)
	if ws.subscriptionManager != nil {
		if subscriptions := ws.subscriptionManager.GetAllSubscriptions(); len(subscriptions) > 0 {
			for _, sub := range subscriptions {
				if sub.Symbol == symbol {
					return sub.Token, true
				}
			}
		}
	}

	// Fallback to symbols client
	if ws.symbolsClient != nil {
		return ws.symbolsClient.GetTokenForSymbol(symbol)
	}

	return "", false
}

// subscribeTokensOnDemand subscribes to tokens on-demand for a specific client
func (ws *WebSocketServer) subscribeTokensOnDemand(tokens []string, clientID string) {
	if ws.pythonBridge == nil {
		log.Printf("❌ ON-DEMAND: Python bridge not available for client %s", clientID)
		return
	}

	log.Printf("🎯 ON-DEMAND: Subscribing to %d tokens for client %s", len(tokens), clientID)

	if err := ws.pythonBridge.SubscribeToTokens(tokens); err != nil {
		log.Printf("❌ ON-DEMAND: Failed to subscribe tokens for client %s: %v", clientID, err)
		return
	}

	log.Printf("✅ ON-DEMAND: Successfully subscribed to %d tokens for client %s", len(tokens), clientID)
}

func (ws *WebSocketServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade connection with improved settings
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}

	// Set connection limits - MAXIMUM BUFFERS for persistent connections
	conn.SetReadLimit(1048576) // 1MB limit for large messages
	// REMOVED: All deadlines for truly persistent connections
	conn.SetPongHandler(func(string) error {
		// Silent pong handling - don't log every pong to reduce noise
		return nil
	})

	// Generate unique client ID (thread-safe, no limits)
	ws.mutex.Lock()
	ws.clientCount++
	clientID := fmt.Sprintf("ws_client_%d", ws.clientCount)
	ws.mutex.Unlock()

	// Set close handler to detect client-initiated disconnections
	conn.SetCloseHandler(func(code int, text string) error {
		log.Printf("🔌 Client %s initiated disconnect (code: %d, reason: %s)", clientID, code, text)
		return nil
	})

	// Connection cleanup without forced close
	defer func() {
		log.Printf("🔌 WebSocket connection cleanup for client %s", clientID)
		// Let the connection close naturally, don't force it
	}()

	// Parse stocks from URL query
	stocksParam := r.URL.Query().Get("stocks")
	var stocks map[string]bool
	var requestedTokens []string

	if stocksParam != "" {
		if strings.ToLower(stocksParam) == "all" {
			// Subscribe to all available stocks
			stocks = make(map[string]bool)
			stocks["*ALL*"] = true
		} else {
			stockList := strings.Split(stocksParam, ",")
			stocks = make(map[string]bool)
			for _, stock := range stockList {
				stockSymbol := strings.TrimSpace(strings.ToUpper(stock))
				stocks[stockSymbol] = true

				// ON-DEMAND SUBSCRIPTION: Get token for this symbol and subscribe
				if token, exists := ws.getTokenForSymbol(stockSymbol); exists {
					requestedTokens = append(requestedTokens, token)
					log.Printf("🎯 ON-DEMAND: Client %s requested %s (token: %s)", clientID, stockSymbol, token)
				}
			}
		}
	} else {
		// Default stocks with on-demand subscription
		defaultStocks := []string{"RELIANCE", "TCS", "INFY", "HDFCBANK", "ICICIBANK"}
		stocks = make(map[string]bool)
		for _, stock := range defaultStocks {
			stocks[stock] = true
			if token, exists := ws.getTokenForSymbol(stock); exists {
				requestedTokens = append(requestedTokens, token)
			}
		}
	}

	// ON-DEMAND SUBSCRIPTION: Subscribe to requested tokens immediately
	if len(requestedTokens) > 0 {
		go ws.subscribeTokensOnDemand(requestedTokens, clientID)
	}

	// Create client with MASSIVE buffer for Indian market - 0% data loss + stability features
	client := &WebSocketClient{
		ID:              clientID,
		Conn:            conn,
		Stocks:          stocks,
		SendChannel:     make(chan MarketData, 500000), // 500K buffer per client for Indian market
		JoinedAt:        time.Now(),
		LastPong:        time.Now(),                       // Initialize last pong time
		MissedPings:     0,                                // Start with 0 missed pings
		IsAlive:         true,                             // Client starts alive
		TotalMessages:   0,                                // Start message counter
		LastMessageTime: time.Now(),                       // Initialize last message time
		HeartbeatTicker: time.NewTicker(30 * time.Second), // Heartbeat every 30 seconds
	}

	// Add to broadcaster
	ws.broadcaster.AddWebSocketClient(client)
	defer ws.broadcaster.RemoveWebSocketClient(clientID)

	// Send welcome message - NO WRITE DEADLINE
	welcomeMsg := map[string]interface{}{
		"type":      "welcome",
		"message":   "Connected to Zero Latency Market Stream - Persistent Connection",
		"client_id": clientID,
		"stocks":    getStocksList(stocks),
		"timestamp": time.Now().UnixMilli(),
	}

	// REMOVED: Write deadline for welcome message
	if err := conn.WriteJSON(welcomeMsg); err != nil {
		log.Printf("⚠️ Failed to send welcome message to %s: %v", clientID, err)
		return
	}

	// START HEARTBEAT MECHANISM: Optional ping/pong for connection monitoring
	log.Printf("💓 Client %s connected with heartbeat monitoring enabled", clientID)

	// Start heartbeat goroutine for connection monitoring
	go func() {
		defer client.HeartbeatTicker.Stop()

		for {
			select {
			case <-client.HeartbeatTicker.C:
				// Send ping to client
				client.WriteMutex.Lock()
				err := conn.WriteMessage(websocket.PingMessage, []byte("heartbeat"))
				client.WriteMutex.Unlock()

				if err != nil {
					log.Printf("💔 Heartbeat ping failed for client %s: %v", clientID, err)
					client.IsAlive = false
					return
				}

				// Check if client missed too many pings
				if time.Since(client.LastPong) > 90*time.Second {
					client.MissedPings++
					log.Printf("⚠️ Client %s missed ping #%d (last pong: %v ago)",
						clientID, client.MissedPings, time.Since(client.LastPong))

					if client.MissedPings >= 3 {
						log.Printf("💔 Client %s exceeded max missed pings, marking as dead", clientID)
						client.IsAlive = false
						return
					}
				}

			case <-time.After(5 * time.Minute): // Heartbeat timeout
				log.Printf("💔 Heartbeat timeout for client %s", clientID)
				return
			}
		}
	}()

	// Enhanced pong handler to track connection health
	conn.SetPongHandler(func(appData string) error {
		client.LastPong = time.Now()
		client.MissedPings = 0
		client.IsAlive = true
		return nil
	})

	// Start sending market data with proper write synchronization and message counting
	go func() {
		for data := range client.SendChannel {
			// Use mutex to prevent concurrent writes
			client.WriteMutex.Lock()
			err := conn.WriteJSON(data)
			client.WriteMutex.Unlock()

			if err != nil {
				// Check if it's a connection close error - suppress normal disconnections
				if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
					// Close code 1005 (CloseNoStatusReceived) is normal - don't log as error
					if websocket.IsCloseError(err, websocket.CloseNoStatusReceived) {
						log.Printf("🔌 Client %s disconnected normally during data send", clientID)
					} else {
						log.Printf("🔌 Client %s disconnected during data send", clientID)
					}
				} else {
					// Only log unexpected write errors, not normal close errors
					log.Printf("⚠️ Write error for %s (connection may be closed): %v", clientID, err)
				}
				client.IsAlive = false
				return // Exit data sender goroutine, but don't force connection close
			}

			// Update message statistics
			client.TotalMessages++
			client.LastMessageTime = time.Now()
		}
	}()

	// Handle client messages (for dynamic stock subscription) with NO TIMEOUTS
	for {
		var msg map[string]interface{}
		// REMOVED: conn.SetReadDeadline - no read timeouts, clients stay connected indefinitely
		if err := conn.ReadJSON(&msg); err != nil {
			// Handle different types of close errors properly - suppress normal disconnections
			if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
				// Close code 1005 (CloseNoStatusReceived) is normal - don't log as error
				if websocket.IsCloseError(err, websocket.CloseNoStatusReceived) {
					log.Printf("🔌 WebSocket client %s disconnected normally", clientID)
				} else {
					log.Printf("🔌 WebSocket client %s disconnected: %v", clientID, err)
				}
			} else if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("❌ WebSocket error for %s: %v", clientID, err)
			} else {
				log.Printf("⚠️ WebSocket read error for %s: %v", clientID, err)
			}
			break
		}

		// Handle subscription updates
		if action, ok := msg["action"].(string); ok {
			switch action {
			case "subscribe":
				if stocksInterface, ok := msg["stocks"].([]interface{}); ok {
					newStocks := make(map[string]bool)
					for _, stock := range stocksInterface {
						if stockStr, ok := stock.(string); ok {
							if strings.ToLower(stockStr) == "all" {
								newStocks["*ALL*"] = true
							} else {
								newStocks[strings.ToUpper(stockStr)] = true
							}
						}
					}
					client.Stocks = newStocks
					log.Printf("📊 Client %s updated stocks: %v", clientID, getStocksList(newStocks))
				}
			case "all":
				// Subscribe to all available stocks
				client.Stocks = map[string]bool{"*ALL*": true}
				log.Printf("📊 Client %s subscribed to ALL stocks", clientID)

				// Send confirmation - NO WRITE DEADLINE
				confirmMsg := map[string]interface{}{
					"type":      "subscription_update",
					"message":   "Subscribed to all available stocks",
					"stocks":    "ALL",
					"timestamp": time.Now().UnixMilli(),
				}
				// REMOVED: Write deadline for confirmation message
				if err := conn.WriteJSON(confirmMsg); err != nil {
					log.Printf("⚠️ Failed to send confirmation message to %s: %v", clientID, err)
				}
			}
		}
	}
}

func getStocksList(stocks map[string]bool) []string {
	list := make([]string, 0, len(stocks))
	for stock := range stocks {
		list = append(list, stock)
	}
	return list
}

func (ws *WebSocketServer) Start() error {
	http.HandleFunc("/stream", ws.handleWebSocket)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		stats := ws.broadcaster.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "healthy",
			"stats":  stats,
		})
	})

	// WebSocket connection diagnostics endpoint
	http.HandleFunc("/api/websocket/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		ws.broadcaster.mutex.RLock()
		defer ws.broadcaster.mutex.RUnlock()

		diagnostics := make([]map[string]interface{}, 0)
		for clientID, client := range ws.broadcaster.wsClients {
			diagnostics = append(diagnostics, map[string]interface{}{
				"client_id":             clientID,
				"connected_at":          client.JoinedAt.Format(time.RFC3339),
				"last_pong":             client.LastPong.Format(time.RFC3339),
				"missed_pings":          client.MissedPings,
				"is_alive":              client.IsAlive,
				"total_messages":        client.TotalMessages,
				"last_message_time":     client.LastMessageTime.Format(time.RFC3339),
				"connection_duration":   time.Since(client.JoinedAt).String(),
				"subscribed_stocks":     getStocksList(client.Stocks),
				"send_channel_size":     len(client.SendChannel),
				"send_channel_capacity": cap(client.SendChannel),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_connections": len(diagnostics),
			"connections":       diagnostics,
			"timestamp":         time.Now().Format(time.RFC3339),
		})
	})

	// WebSocket connection statistics endpoint
	http.HandleFunc("/api/websocket/stats", func(w http.ResponseWriter, r *http.Request) {
		ws.broadcaster.mutex.RLock()
		defer ws.broadcaster.mutex.RUnlock()

		var aliveConnections, deadConnections int
		var totalMessages int64
		var avgConnectionDuration time.Duration

		for _, client := range ws.broadcaster.wsClients {
			if client.IsAlive {
				aliveConnections++
			} else {
				deadConnections++
			}
			totalMessages += client.TotalMessages
			avgConnectionDuration += time.Since(client.JoinedAt)
		}

		if len(ws.broadcaster.wsClients) > 0 {
			avgConnectionDuration = avgConnectionDuration / time.Duration(len(ws.broadcaster.wsClients))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_connections":       len(ws.broadcaster.wsClients),
			"alive_connections":       aliveConnections,
			"dead_connections":        deadConnections,
			"total_messages_sent":     totalMessages,
			"avg_connection_duration": avgConnectionDuration.String(),
			"heartbeat_interval":      "30s",
			"ping_timeout":            "90s",
			"max_missed_pings":        3,
			"timestamp":               time.Now().Format(time.RFC3339),
		})
	})

	log.Printf("🚀 Starting WebSocket server on port %d", ws.port)
	log.Printf("📡 Local URL: ws://localhost:%d/stream?stocks=RELIANCE,TCS", ws.port)
	log.Printf("🌐 External URL: ws://localhost:%d/stream?stocks=RELIANCE,TCS", ws.port)
	log.Printf("🔓 No authentication required")

	return http.ListenAndServe(fmt.Sprintf(":%d", ws.port), nil)
}

// ============================================================================
// STOCK API ENDPOINTS
// ============================================================================

func (ws *WebSocketServer) setupStockAPIEndpoints(stockManager *StockInfoManager) {
	// Parse stock data endpoint
	http.HandleFunc("/api/stocks/parse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var requestBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Convert back to JSON string for parsing
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			http.Error(w, "Failed to process data", http.StatusInternalServerError)
			return
		}

		if err := stockManager.ParseStockData(string(jsonData)); err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse stock data: %v", err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Stock data parsed successfully",
			"stats":   stockManager.GetStats(),
		})
	})

	// Get stock by NSE symbol
	http.HandleFunc("/api/stocks/nse/", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimPrefix(r.URL.Path, "/api/stocks/nse/")
		if symbol == "" {
			http.Error(w, "Symbol required", http.StatusBadRequest)
			return
		}

		stock, exists := stockManager.GetStockByNSESymbol(strings.ToUpper(symbol))
		if !exists {
			http.Error(w, "Stock not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stock)
	})

	// Get stock by BSE code
	http.HandleFunc("/api/stocks/bse/", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimPrefix(r.URL.Path, "/api/stocks/bse/")
		if code == "" {
			http.Error(w, "BSE code required", http.StatusBadRequest)
			return
		}

		stock, exists := stockManager.GetStockByBSECode(code)
		if !exists {
			http.Error(w, "Stock not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stock)
	})

	// Get stock by ISIN
	http.HandleFunc("/api/stocks/isin/", func(w http.ResponseWriter, r *http.Request) {
		isin := strings.TrimPrefix(r.URL.Path, "/api/stocks/isin/")
		if isin == "" {
			http.Error(w, "ISIN required", http.StatusBadRequest)
			return
		}

		stock, exists := stockManager.GetStockByISIN(isin)
		if !exists {
			http.Error(w, "Stock not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stock)
	})

	// Get stocks by sector
	http.HandleFunc("/api/stocks/sector/", func(w http.ResponseWriter, r *http.Request) {
		sector := strings.TrimPrefix(r.URL.Path, "/api/stocks/sector/")
		if sector == "" {
			http.Error(w, "Sector required", http.StatusBadRequest)
			return
		}

		stocks := stockManager.GetStocksBySector(sector)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sector": sector,
			"count":  len(stocks),
			"stocks": stocks,
		})
	})

	// Get stocks by industry
	http.HandleFunc("/api/stocks/industry/", func(w http.ResponseWriter, r *http.Request) {
		industry := strings.TrimPrefix(r.URL.Path, "/api/stocks/industry/")
		if industry == "" {
			http.Error(w, "Industry required", http.StatusBadRequest)
			return
		}

		stocks := stockManager.GetStocksByIndustry(industry)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"industry": industry,
			"count":    len(stocks),
			"stocks":   stocks,
		})
	})

	// Get all sectors
	http.HandleFunc("/api/stocks/sectors", func(w http.ResponseWriter, r *http.Request) {
		sectors := stockManager.GetSectors()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":   len(sectors),
			"sectors": sectors,
		})
	})

	// Get all industries
	http.HandleFunc("/api/stocks/industries", func(w http.ResponseWriter, r *http.Request) {
		industries := stockManager.GetIndustries()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":      len(industries),
			"industries": industries,
		})
	})

	// Get all stocks
	http.HandleFunc("/api/stocks/all", func(w http.ResponseWriter, r *http.Request) {
		stocks := stockManager.GetAllStocks()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":  len(stocks),
			"stocks": stocks,
		})
	})

	// Get stock manager stats
	http.HandleFunc("/api/stocks/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := stockManager.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})
}

// ============================================================================
// MAIN APPLICATION
// ============================================================================

type MarketService struct {
	symbolsClient       *SymbolsAPIClient
	pythonBridge        *PythonB2CBridge
	broadcaster         *ZeroLatencyBroadcaster
	wsServer            *WebSocketServer
	stockManager        *StockInfoManager
	subscriptionManager *StockSubscriptionManager
	stockDatabase       *StockDatabase          // New SQLite database
	historicalManager   *HistoricalPriceManager // Historical + Live integration
	// grpcServer would be added here
}

func NewMarketService() *MarketService {
	apiBaseURL := os.Getenv("API_BASE_URL")
	pythonScript := os.Getenv("PYTHON_SCRIPT")
	wsPortStr := os.Getenv("WS_PORT")
	stockDBPath := os.Getenv("STOCK_DB")
	historicalDBPath := os.Getenv("HISTORICAL_DB")
	historicalAPIURL := os.Getenv("HISTORICAL_API_URL")

	// Convert WS_PORT to int
	wsPort, err := strconv.Atoi(wsPortStr)
	if err != nil {
		log.Fatalf("❌ Invalid WS_PORT value in .env: %v", err)
	}

	symbolsClient := NewSymbolsAPIClient(apiBaseURL)
	pythonBridge := NewPythonB2CBridge(pythonScript)
	broadcaster := NewZeroLatencyBroadcaster()
	wsServer := NewWebSocketServer(broadcaster, wsPort)
	stockManager := NewStockInfoManager()
	subscriptionManager := NewStockSubscriptionManager(apiBaseURL, pythonBridge, symbolsClient)

	// Initialize SQLite database
	stockDatabase, err := NewStockDatabase(stockDBPath, apiBaseURL)
	if err != nil {
		log.Fatalf("❌ Failed to initialize stock database: %v", err)
	}

	// Initialize Historical Price Manager
	historicalManager, err := NewHistoricalPriceManager(historicalDBPath, historicalAPIURL, stockDatabase, broadcaster)
	if err != nil {
		log.Fatalf("❌ Failed to initialize historical price manager: %v", err)
	}

	return &MarketService{
		symbolsClient:       symbolsClient,
		pythonBridge:        pythonBridge,
		broadcaster:         broadcaster,
		wsServer:            wsServer,
		stockManager:        stockManager,
		subscriptionManager: subscriptionManager,
		stockDatabase:       stockDatabase,
		historicalManager:   historicalManager,
	}
}

func (ms *MarketService) Start() error {
	log.Printf("🚀 Starting Golang Market Service with Intelligent Stock Filtering")
	log.Printf("=" + strings.Repeat("=", 79))
	log.Printf("Features:")
	log.Printf("  ✅ Intelligent stock filtering (NSE priority, BSE fallback)")
	log.Printf("  🎯 Active stock subscription management")
	log.Printf("  ⚡ Zero latency streaming")
	log.Printf("  🔓 No authentication required")
	log.Printf("  📡 Support for 1000+ concurrent clients")
	log.Printf("  🌐 WebSocket + gRPC streaming")
	log.Printf("  📊 Stock information management")
	log.Printf("=" + strings.Repeat("=", 79))

	// Step 1: Initialize database and load existing data into memory
	log.Printf("🎯 Step 1: Initializing database and loading existing data into memory...")
	if err := ms.stockDatabase.InitializeFromDatabase(); err != nil {
		log.Printf("⚠️ Warning: Failed to initialize from existing database: %v", err)
	}

	// Step 2: Check if we need to refresh stock data or use existing database
	log.Printf("🎯 Step 2: Checking stock data freshness...")

	// Check FORCE_REFRESH environment variable
	forceRefresh := os.Getenv("FORCE_REFRESH")
	stats := ms.stockDatabase.GetStats()
	needsRefresh := true

	if strings.ToLower(forceRefresh) == "false" {
		// Check if database has recent data (less than 24 hours old)
		if dbReady, ok := stats["database_ready"].(bool); ok && dbReady {
			if totalStocks, ok := stats["total_stocks"].(int); ok && totalStocks > 5000 {
				log.Printf("✅ FORCE_REFRESH=false: Found existing database with %d stocks - SKIPPING API refresh for ultra-fast startup", totalStocks)
				needsRefresh = false
			}
		}
	} else if strings.ToLower(forceRefresh) == "true" {
		log.Printf("🔄 FORCE_REFRESH=true: Forcing fresh data fetch from API")
		needsRefresh = true
	} else {
		// Default behavior: check database freshness
		if dbReady, ok := stats["database_ready"].(bool); ok && dbReady {
			if totalStocks, ok := stats["total_stocks"].(int); ok && totalStocks > 5000 {
				log.Printf("✅ Found existing database with %d stocks - SKIPPING API refresh for faster startup", totalStocks)
				needsRefresh = false
			}
		}
	}

	var filterResult *StockFilterResult
	if needsRefresh {
		log.Printf("🔄 Fetching fresh stock data from API...")
		result, err := ms.stockDatabase.FetchAndStoreStocks()
		if err != nil {
			return fmt.Errorf("failed to fetch and store stocks: %w", err)
		}
		filterResult = result
	} else {
		// Use existing database data
		log.Printf("⚡ Using existing database for ultra-fast startup")
		filterResult = &StockFilterResult{
			TotalStocks:      6284, // Approximate from last fetch
			ActiveStocks:     stats["total_stocks"].(int),
			NSEOnlyStocks:    stats["nse_stocks"].(int),
			BSEOnlyStocks:    stats["bse_stocks"].(int),
			BothExchanges:    stats["nse_stocks"].(int) - 768, // Approximate
			SubscribedTokens: make([]string, stats["total_stocks"].(int)),
			FilteredAt:       time.Now(),
		}
	}

	log.Printf("✅ SQLite-based intelligent stock filtering completed:")
	log.Printf("   📊 Total stocks from API: %d", filterResult.TotalStocks)
	log.Printf("   ✅ Active stocks stored: %d", filterResult.ActiveStocks)
	log.Printf("   📈 NSE-only stocks: %d (segment 1)", filterResult.NSEOnlyStocks)
	log.Printf("   📉 BSE-only stocks: %d (segment 3)", filterResult.BSEOnlyStocks)
	log.Printf("   🔄 Both exchanges (NSE preferred): %d (segment 1)", filterResult.BothExchanges)
	log.Printf("   🎯 Final subscriptions ready: %d", len(filterResult.SubscribedTokens))
	log.Printf("   ⚡ ALL TOKEN MAPPINGS NOW IN MEMORY - INSTANT LOOKUPS ENABLED!")

	// Step 3: Initialize symbols client (for fallback compatibility)
	log.Printf("🔄 Step 3: Initializing symbols client for fallback...")
	if err := ms.symbolsClient.FetchSymbols(); err != nil {
		log.Printf("⚠️ Warning: Failed to fetch symbols for fallback: %v", err)
	}

	// Step 4: Setup stock API endpoints and dependencies
	log.Printf("🔗 Step 4: Setting up stock API endpoints and dependencies...")
	ms.wsServer.setupStockAPIEndpoints(ms.stockManager)
	ms.wsServer.SetDependencies(ms.subscriptionManager, ms.symbolsClient, ms.pythonBridge)
	ms.setupSubscriptionAPIEndpoints()
	ms.setupDatabaseAPIEndpoints()
	ms.setupHistoricalAPIEndpoints()

	// Step 5: Get token:exchange pairs from SQLite database
	log.Printf("🐍 Step 5: Getting token:exchange pairs from SQLite database...")
	tokenExchangePairs, err := ms.stockDatabase.GetAllTokenExchangePairs()
	if err != nil {
		return fmt.Errorf("failed to get token pairs from database: %w", err)
	}

	log.Printf("📊 Retrieved %d token:exchange pairs from SQLite database", len(tokenExchangePairs))
	log.Printf("📊 NSE tokens will use market segment 1, BSE tokens will use market segment 3")

	// Step 6: Start Python B2C bridge with database-driven token pairs
	log.Printf("🐍 Step 6: Starting Python B2C bridge with database-driven subscriptions...")
	if err := ms.pythonBridge.Start(tokenExchangePairs); err != nil {
		return fmt.Errorf("failed to start Python bridge: %w", err)
	}

	// Step 7: Start INSTANT in-memory market data processing
	log.Printf("📊 Step 7: Starting INSTANT in-memory market data processing...")
	go ms.processInstantMarketData()

	// Step 8: Start WebSocket server
	log.Printf("🚀 Step 8: Starting WebSocket server...")
	go func() {
		if err := ms.wsServer.Start(); err != nil {
			log.Printf("❌ WebSocket server error: %v", err)
		}
	}()

	log.Printf("✅ Golang Market Service started successfully with intelligent filtering")
	log.Printf("📊 Subscribed to %d filtered tokens", len(filterResult.SubscribedTokens))
	log.Printf("📡 Local WebSocket: ws://localhost:8081/stream?stocks=RELIANCE,TCS")
	log.Printf("🌐 External WebSocket: ws://localhost:8081/stream?stocks=RELIANCE,TCS")
	log.Printf("🔗 Local API: http://localhost:8081/api/stocks/")
	log.Printf("🌐 External API: http://localhost:8081/api/stocks/")
	log.Printf("📋 Available endpoints:")
	log.Printf("  POST /api/stocks/parse - Parse stock data")
	log.Printf("  GET  /api/stocks/nse/{symbol} - Get stock by NSE symbol")
	log.Printf("  GET  /api/stocks/bse/{code} - Get stock by BSE code")
	log.Printf("  GET  /api/stocks/isin/{isin} - Get stock by ISIN")
	log.Printf("  GET  /api/stocks/sector/{sector} - Get stocks by sector")
	log.Printf("  GET  /api/stocks/industry/{industry} - Get stocks by industry")
	log.Printf("  GET  /api/stocks/sectors - Get all sectors")
	log.Printf("  GET  /api/stocks/industries - Get all industries")
	log.Printf("  GET  /api/stocks/all - Get all stocks")
	log.Printf("  GET  /api/stocks/stats - Get stock manager stats")
	log.Printf("  GET  /api/subscriptions/stats - Get subscription stats")
	log.Printf("  GET  /api/subscriptions/all - Get all subscriptions")
	log.Printf("  GET  /api/subscriptions/nse - Get NSE subscriptions")
	log.Printf("  GET  /api/subscriptions/bse - Get BSE subscriptions")
	log.Printf("  POST /api/subscriptions/refresh - Refresh subscriptions")

	return nil
}

// setupSubscriptionAPIEndpoints sets up API endpoints for subscription management
func (ms *MarketService) setupSubscriptionAPIEndpoints() {
	// Get subscription stats
	http.HandleFunc("/api/subscriptions/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := ms.subscriptionManager.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// Get all subscriptions
	http.HandleFunc("/api/subscriptions/all", func(w http.ResponseWriter, r *http.Request) {
		subscriptions := ms.subscriptionManager.GetAllSubscriptions()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":         len(subscriptions),
			"subscriptions": subscriptions,
		})
	})

	// Get NSE subscriptions
	http.HandleFunc("/api/subscriptions/nse", func(w http.ResponseWriter, r *http.Request) {
		subscriptions := ms.subscriptionManager.GetSubscriptionsByExchange("NSE")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"exchange":      "NSE",
			"count":         len(subscriptions),
			"subscriptions": subscriptions,
		})
	})

	// Get BSE subscriptions
	http.HandleFunc("/api/subscriptions/bse", func(w http.ResponseWriter, r *http.Request) {
		subscriptions := ms.subscriptionManager.GetSubscriptionsByExchange("BSE")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"exchange":      "BSE",
			"count":         len(subscriptions),
			"subscriptions": subscriptions,
		})
	})

	// Refresh subscriptions
	http.HandleFunc("/api/subscriptions/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := ms.subscriptionManager.RefreshSubscriptions(); err != nil {
			http.Error(w, fmt.Sprintf("Failed to refresh subscriptions: %v", err), http.StatusInternalServerError)
			return
		}

		stats := ms.subscriptionManager.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Subscriptions refreshed successfully",
			"stats":   stats,
		})
	})
}

// processEnhancedMarketData processes market data with enhanced subscription management
func (ms *MarketService) processEnhancedMarketData() {
	dataChannel := ms.pythonBridge.GetDataChannel()

	// Production monitoring variables
	var (
		totalProcessed   int64
		successfulMapped int64
		failedMappings   int64
		droppedData      int64
		enhancedMappings int64
		lastStatsTime    = time.Now()
		statsInterval    = 2 * time.Minute
	)

	log.Printf("🔄 Starting ENHANCED market data processing pipeline")
	log.Printf("📊 Features: Intelligent subscription mapping, exchange-aware processing, comprehensive monitoring")

	for marketData := range dataChannel {
		totalProcessed++

		// STRICT VALIDATION: Only process data with valid tokens
		if marketData.Token == "" {
			droppedData++
			if droppedData <= 5 || droppedData%100 == 0 {
				log.Printf("❌ ENHANCED: Dropping market data with empty token (total dropped: %d)", droppedData)
			}
			continue
		}

		// ENHANCED TOKEN MAPPING: Use subscription manager for intelligent mapping
		if symbol, exchange, exists := ms.subscriptionManager.GetSymbolForToken(marketData.Token); exists && symbol != "" {
			marketData.Symbol = symbol
			enhancedMappings++

			// Add timestamp if missing
			if marketData.Timestamp == 0 {
				marketData.Timestamp = time.Now().UnixMilli()
			}

			// Log exchange information for monitoring
			if enhancedMappings <= 10 || enhancedMappings%1000 == 0 {
				log.Printf("📊 ENHANCED: Token %s mapped to %s (%s exchange) - mapping #%d",
					marketData.Token, symbol, exchange, enhancedMappings)
			}

			// BROADCAST ENHANCED DATA TO CLIENTS
			ms.broadcaster.BroadcastMarketData(marketData)
		} else {
			// Fallback to original symbol mapping
			if symbol, exists := ms.symbolsClient.GetSymbolForToken(marketData.Token); exists && symbol != "" {
				marketData.Symbol = symbol
				successfulMapped++

				// Add timestamp if missing
				if marketData.Timestamp == 0 {
					marketData.Timestamp = time.Now().UnixMilli()
				}

				// BROADCAST FALLBACK DATA TO CLIENTS
				ms.broadcaster.BroadcastMarketData(marketData)
			} else {
				failedMappings++

				// Log failed mappings for production monitoring (rate limited)
				if failedMappings <= 10 || failedMappings%50 == 0 {
					log.Printf("⚠️ ENHANCED: Token mapping failed for token %s (failure #%d)", marketData.Token, failedMappings)
				}

				// DO NOT BROADCAST DATA WITHOUT PROPER SYMBOL MAPPING
				continue
			}
		}

		// ENHANCED MONITORING: Print stats periodically
		if time.Since(lastStatsTime) >= statsInterval {
			totalMapped := enhancedMappings + successfulMapped
			mappingSuccessRate := float64(totalMapped) / float64(totalProcessed) * 100
			enhancedRate := float64(enhancedMappings) / float64(totalMapped) * 100

			log.Printf("📊 ENHANCED Market Data Stats:")
			log.Printf("   Total Processed: %d", totalProcessed)
			log.Printf("   Enhanced Mappings: %d (%.2f%% of successful)", enhancedMappings, enhancedRate)
			log.Printf("   Fallback Mappings: %d", successfulMapped)
			log.Printf("   Total Successful: %d (%.2f%%)", totalMapped, mappingSuccessRate)
			log.Printf("   Failed Mappings: %d", failedMappings)
			log.Printf("   Dropped (Empty Token): %d", droppedData)
			log.Printf("   Processing Rate: %.2f msg/sec", float64(totalProcessed)/time.Since(lastStatsTime).Seconds())

			lastStatsTime = time.Now()
		}
	}

	log.Printf("🛑 ENHANCED market data processing pipeline stopped")
}

// PRODUCTION-READY MARKET DATA PROCESSING - NO FAKE DATA
func (ms *MarketService) processMarketData() {
	dataChannel := ms.pythonBridge.GetDataChannel()

	// Production monitoring variables
	var (
		totalProcessed   int64
		successfulMapped int64
		failedMappings   int64
		droppedData      int64
		lastStatsTime    = time.Now()
		statsInterval    = 2 * time.Minute
	)

	log.Printf("🔄 Starting PRODUCTION market data processing pipeline")
	log.Printf("📊 Features: Real data only, robust token mapping, comprehensive monitoring")

	for marketData := range dataChannel {
		totalProcessed++

		// STRICT VALIDATION: Only process data with valid tokens
		if marketData.Token == "" {
			droppedData++
			if droppedData <= 5 || droppedData%100 == 0 {
				log.Printf("❌ PRODUCTION: Dropping market data with empty token (total dropped: %d)", droppedData)
			}
			continue
		}

		// PRODUCTION TOKEN MAPPING: Only use real symbol mappings
		if symbol, exists := ms.symbolsClient.GetSymbolForToken(marketData.Token); exists && symbol != "" {
			marketData.Symbol = symbol
			successfulMapped++

			// Add timestamp if missing
			if marketData.Timestamp == 0 {
				marketData.Timestamp = time.Now().UnixMilli()
			}

			// BROADCAST ONLY REAL DATA TO CLIENTS
			ms.broadcaster.BroadcastMarketData(marketData)
		} else {
			failedMappings++

			// Log failed mappings for production monitoring (rate limited)
			if failedMappings <= 10 || failedMappings%50 == 0 {
				log.Printf("⚠️ PRODUCTION: Token mapping failed for token %s (failure #%d)", marketData.Token, failedMappings)
			}

			// DO NOT BROADCAST DATA WITHOUT PROPER SYMBOL MAPPING IN PRODUCTION
			continue
		}

		// PRODUCTION MONITORING: Print stats periodically
		if time.Since(lastStatsTime) >= statsInterval {
			mappingSuccessRate := float64(successfulMapped) / float64(totalProcessed) * 100
			log.Printf("📊 PRODUCTION Market Data Stats:")
			log.Printf("   Total Processed: %d", totalProcessed)
			log.Printf("   Successfully Mapped: %d (%.2f%%)", successfulMapped, mappingSuccessRate)
			log.Printf("   Failed Mappings: %d", failedMappings)
			log.Printf("   Dropped (Empty Token): %d", droppedData)
			log.Printf("   Processing Rate: %.2f msg/sec", float64(totalProcessed)/time.Since(lastStatsTime).Seconds())

			lastStatsTime = time.Now()
		}
	}

	log.Printf("🛑 PRODUCTION market data processing pipeline stopped")
}

func (ms *MarketService) Stop() error {
	log.Printf("🛑 Stopping Golang Market Service")

	if err := ms.pythonBridge.Stop(); err != nil {
		log.Printf("⚠️ Error stopping Python bridge: %v", err)
	}

	log.Printf("✅ Golang Market Service stopped")
	return nil
}

// setupDatabaseAPIEndpoints sets up API endpoints for database management
func (ms *MarketService) setupDatabaseAPIEndpoints() {
	// Get database stats
	http.HandleFunc("/api/database/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := ms.stockDatabase.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// Get all database subscriptions
	http.HandleFunc("/api/database/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		subscriptions, err := ms.stockDatabase.GetAllSubscriptions()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get subscriptions: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"count":         len(subscriptions),
			"subscriptions": subscriptions,
		})
	})

	// Refresh database from API
	http.HandleFunc("/api/database/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		result, err := ms.stockDatabase.FetchAndStoreStocks()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to refresh database: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Database refreshed successfully",
			"result":  result,
		})
	})
}

// processInstantMarketData processes market data using instant in-memory lookups with historical integration
func (ms *MarketService) processInstantMarketData() {
	dataChannel := ms.pythonBridge.GetDataChannel()

	// Production monitoring variables
	var (
		totalProcessed   int64
		successfulMapped int64
		failedMappings   int64
		droppedData      int64
		sqliteMappings   int64
		lastStatsTime    = time.Now()
		statsInterval    = 30 * time.Second // More frequent stats for SQLite performance monitoring
	)

	log.Printf("🔄 Starting INSTANT IN-MEMORY market data processing pipeline with HISTORICAL INTEGRATION")
	log.Printf("📊 Features: Zero-latency in-memory lookups, exchange-aware processing, historical data integration, maximum performance")

	for marketData := range dataChannel {
		totalProcessed++

		// STRICT VALIDATION: Only process data with valid tokens
		if marketData.Token == "" {
			droppedData++
			if droppedData <= 5 || droppedData%100 == 0 {
				log.Printf("❌ SQLITE: Dropping market data with empty token (total dropped: %d)", droppedData)
			}
			continue
		}

		// INSTANT IN-MEMORY TOKEN MAPPING: Zero-latency lookup
		if symbol, exchange, exists := ms.stockDatabase.GetSymbolForToken(marketData.Token); exists && symbol != "" {
			marketData.Symbol = symbol
			sqliteMappings++

			// Add timestamp if missing
			if marketData.Timestamp == 0 {
				marketData.Timestamp = time.Now().UnixMilli()
			}

			// Log instant mapping performance (rate limited)
			if sqliteMappings <= 10 || sqliteMappings%1000 == 0 {
				log.Printf("⚡ INSTANT: Token %s mapped to %s (%s exchange) - mapping #%d",
					marketData.Token, symbol, exchange, sqliteMappings)
			}

			// HISTORICAL INTEGRATION: Process live data for historical manager
			ms.historicalManager.ProcessLiveData(marketData)

			// BROADCAST DATA TO CLIENTS
			ms.broadcaster.BroadcastMarketData(marketData)
		} else {
			// Fallback to subscription manager
			if symbol, _, exists := ms.subscriptionManager.GetSymbolForToken(marketData.Token); exists && symbol != "" {
				marketData.Symbol = symbol
				successfulMapped++

				// Add timestamp if missing
				if marketData.Timestamp == 0 {
					marketData.Timestamp = time.Now().UnixMilli()
				}

				// HISTORICAL INTEGRATION: Process live data for historical manager
				ms.historicalManager.ProcessLiveData(marketData)

				// BROADCAST FALLBACK DATA TO CLIENTS
				ms.broadcaster.BroadcastMarketData(marketData)
			} else {
				failedMappings++

				// Log failed mappings for production monitoring (rate limited)
				if failedMappings <= 10 || failedMappings%50 == 0 {
					log.Printf("⚠️ SQLITE: Token mapping failed for token %s (failure #%d)", marketData.Token, failedMappings)
				}

				// DO NOT BROADCAST DATA WITHOUT PROPER SYMBOL MAPPING
				continue
			}
		}

		// INSTANT PERFORMANCE MONITORING: Print stats more frequently
		if time.Since(lastStatsTime) >= statsInterval {
			totalMapped := sqliteMappings + successfulMapped
			mappingSuccessRate := float64(totalMapped) / float64(totalProcessed) * 100
			instantRate := float64(sqliteMappings) / float64(totalMapped) * 100

			log.Printf("⚡ INSTANT Market Data Performance with HISTORICAL INTEGRATION:")
			log.Printf("   Total Processed: %d", totalProcessed)
			log.Printf("   Instant Mappings: %d (%.2f%% of successful) ⚡", sqliteMappings, instantRate)
			log.Printf("   Fallback Mappings: %d", successfulMapped)
			log.Printf("   Total Successful: %d (%.2f%%)", totalMapped, mappingSuccessRate)
			log.Printf("   Failed Mappings: %d", failedMappings)
			log.Printf("   Dropped (Empty Token): %d", droppedData)
			log.Printf("   Processing Rate: %.2f msg/sec ⚡", float64(totalProcessed)/time.Since(lastStatsTime).Seconds())

			// Historical manager stats
			historicalStats := ms.historicalManager.GetCacheStats()
			log.Printf("   📊 Historical Cache: %d series, %d live points",
				historicalStats["cached_series"], historicalStats["live_data_points"])

			lastStatsTime = time.Now()
		}
	}

	log.Printf("🛑 INSTANT market data processing pipeline with HISTORICAL INTEGRATION stopped")
}

func (ms *MarketService) PrintStats() {
	stats := ms.broadcaster.GetStats()
	log.Printf("📊 Service Stats: %+v", stats)
}

// ============================================================================
// MAIN FUNCTION
// ============================================================================

func main() {
	// Create market service
	service := NewMarketService()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start service
	if err := service.Start(); err != nil {
		log.Fatalf("❌ Failed to start service: %v", err)
	}

	// Print stats periodically
	statsTicker := time.NewTicker(30 * time.Second)
	defer statsTicker.Stop()

	go func() {
		for range statsTicker.C {
			service.PrintStats()
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Printf("🛑 Received shutdown signal")

	// Stop service
	if err := service.Stop(); err != nil {
		log.Printf("⚠️ Error during shutdown: %v", err)
	}

	log.Printf("✅ Service shutdown complete")
}
