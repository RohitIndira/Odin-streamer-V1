package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StockSubscriptionManager handles intelligent stock filtering and subscription
type StockSubscriptionManager struct {
	apiURL          string
	allStocks       []StockInfo
	activeStocks    []StockInfo
	subscriptionMap map[string]SubscriptionInfo // token -> subscription info
	mutex           sync.RWMutex
	lastUpdated     time.Time
	pythonBridge    *PythonB2CBridge
	symbolsClient   *SymbolsAPIClient
}

// SubscriptionInfo contains details about a stock subscription
type SubscriptionInfo struct {
	Token        string     `json:"token"`
	Symbol       string     `json:"symbol"`
	Exchange     string     `json:"exchange"` // NSE or BSE
	CompanyName  string     `json:"company_name"`
	Status       string     `json:"status"` // Active, Inactive
	SubscribedAt time.Time  `json:"subscribed_at"`
	StockInfo    *StockInfo `json:"stock_info"`
}

// StockFilterResult contains the filtering results
type StockFilterResult struct {
	TotalStocks      int                `json:"total_stocks"`
	ActiveStocks     int                `json:"active_stocks"`
	NSEOnlyStocks    int                `json:"nse_only_stocks"`
	BSEOnlyStocks    int                `json:"bse_only_stocks"`
	BothExchanges    int                `json:"both_exchanges"`
	SubscribedTokens []string           `json:"subscribed_tokens"`
	Subscriptions    []SubscriptionInfo `json:"subscriptions"`
	FilteredAt       time.Time          `json:"filtered_at"`
}

// NewStockSubscriptionManager creates a new stock subscription manager
func NewStockSubscriptionManager(apiURL string, pythonBridge *PythonB2CBridge, symbolsClient *SymbolsAPIClient) *StockSubscriptionManager {
	return &StockSubscriptionManager{
		apiURL:          apiURL,
		allStocks:       make([]StockInfo, 0),
		activeStocks:    make([]StockInfo, 0),
		subscriptionMap: make(map[string]SubscriptionInfo),
		pythonBridge:    pythonBridge,
		symbolsClient:   symbolsClient,
	}
}

// FetchAndFilterStocks fetches stocks from API and applies intelligent filtering
func (ssm *StockSubscriptionManager) FetchAndFilterStocks() (*StockFilterResult, error) {
	ssm.mutex.Lock()
	defer ssm.mutex.Unlock()

	log.Printf("🔄 Fetching stocks from API: %s", ssm.apiURL)

	// Fetch data from API
	resp, err := http.Get(ssm.apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stocks: %w", err)
	}
	defer resp.Body.Close()

	var apiResponse StockAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	if !apiResponse.Success {
		return nil, fmt.Errorf("API response indicates failure")
	}

	ssm.allStocks = apiResponse.Data
	ssm.lastUpdated = time.Now()

	log.Printf("✅ Fetched %d stocks from API", len(ssm.allStocks))

	// Apply intelligent filtering and subscription logic
	return ssm.applyIntelligentFiltering()
}

// applyIntelligentFiltering applies the core business logic for stock filtering
func (ssm *StockSubscriptionManager) applyIntelligentFiltering() (*StockFilterResult, error) {
	var (
		activeStocks     []StockInfo
		subscriptions    []SubscriptionInfo
		subscribedTokens []string
		nseOnlyCount     int
		bseOnlyCount     int
		bothExchanges    int
	)

	// Clear existing subscriptions
	ssm.subscriptionMap = make(map[string]SubscriptionInfo)

	log.Printf("🎯 Applying intelligent stock filtering logic...")

	for _, stock := range ssm.allStocks {
		// Check if stock has active status on any exchange
		nseActive := strings.ToUpper(stock.NSEStatus) == "ACTIVE"
		bseActive := strings.ToUpper(stock.BSEStatus) == "ACTIVE"

		// Skip stocks that are not active on any exchange
		if !nseActive && !bseActive {
			continue
		}

		activeStocks = append(activeStocks, stock)

		// Apply subscription priority logic:
		// 1. If active on both NSE and BSE -> prefer NSE
		// 2. If active only on BSE -> use BSE
		// 3. If active only on NSE -> use NSE
		var selectedExchange, selectedSymbol string
		var token string

		if nseActive && bseActive {
			// Both exchanges active - prefer NSE
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			token = fmt.Sprintf("%.0f", stock.CoCode)
			bothExchanges++
			log.Printf("📊 Stock %s (%s): Active on both exchanges, selecting NSE", stock.CompanyShortName, stock.NSESymbol)
		} else if nseActive {
			// NSE only
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			token = fmt.Sprintf("%.0f", stock.CoCode)
			nseOnlyCount++
			log.Printf("📈 Stock %s (%s): NSE only", stock.CompanyShortName, stock.NSESymbol)
		} else if bseActive {
			// BSE only
			selectedExchange = "BSE"
			selectedSymbol = stock.BSECode
			token = fmt.Sprintf("%.0f", stock.CoCode)
			bseOnlyCount++
			log.Printf("📉 Stock %s (%s): BSE only", stock.CompanyShortName, stock.BSECode)
		}

		// Skip if no valid symbol found
		if selectedSymbol == "" || token == "" {
			log.Printf("⚠️ Skipping stock %s: no valid symbol/token", stock.CompanyShortName)
			continue
		}

		// Create subscription info
		subscription := SubscriptionInfo{
			Token:        token,
			Symbol:       selectedSymbol,
			Exchange:     selectedExchange,
			CompanyName:  stock.CompanyName,
			Status:       "Active",
			SubscribedAt: time.Now(),
			StockInfo:    &stock,
		}

		// Add to maps and lists
		ssm.subscriptionMap[token] = subscription
		subscriptions = append(subscriptions, subscription)
		subscribedTokens = append(subscribedTokens, token)
	}

	ssm.activeStocks = activeStocks

	result := &StockFilterResult{
		TotalStocks:      len(ssm.allStocks),
		ActiveStocks:     len(activeStocks),
		NSEOnlyStocks:    nseOnlyCount,
		BSEOnlyStocks:    bseOnlyCount,
		BothExchanges:    bothExchanges,
		SubscribedTokens: subscribedTokens,
		Subscriptions:    subscriptions,
		FilteredAt:       time.Now(),
	}

	log.Printf("✅ Stock filtering completed:")
	log.Printf("   📊 Total stocks: %d", result.TotalStocks)
	log.Printf("   ✅ Active stocks: %d", result.ActiveStocks)
	log.Printf("   📈 NSE only: %d", result.NSEOnlyStocks)
	log.Printf("   📉 BSE only: %d", result.BSEOnlyStocks)
	log.Printf("   🔄 Both exchanges (NSE preferred): %d", result.BothExchanges)
	log.Printf("   🎯 Total subscriptions: %d", len(subscribedTokens))

	return result, nil
}

// SubscribeToFilteredStocks subscribes to the filtered stocks via Python bridge
func (ssm *StockSubscriptionManager) SubscribeToFilteredStocks() error {
	ssm.mutex.RLock()
	tokens := make([]string, 0, len(ssm.subscriptionMap))
	for token := range ssm.subscriptionMap {
		tokens = append(tokens, token)
	}
	ssm.mutex.RUnlock()

	if len(tokens) == 0 {
		return fmt.Errorf("no tokens to subscribe to")
	}

	log.Printf("🎯 Subscribing to %d filtered tokens via Python bridge", len(tokens))

	// Subscribe via Python bridge
	if err := ssm.pythonBridge.SubscribeToTokens(tokens); err != nil {
		return fmt.Errorf("failed to subscribe to tokens: %w", err)
	}

	log.Printf("✅ Successfully subscribed to %d tokens", len(tokens))
	return nil
}

// GetSubscriptionInfo returns subscription info for a token
func (ssm *StockSubscriptionManager) GetSubscriptionInfo(token string) (SubscriptionInfo, bool) {
	ssm.mutex.RLock()
	defer ssm.mutex.RUnlock()

	info, exists := ssm.subscriptionMap[token]
	return info, exists
}

// GetAllSubscriptions returns all current subscriptions
func (ssm *StockSubscriptionManager) GetAllSubscriptions() []SubscriptionInfo {
	ssm.mutex.RLock()
	defer ssm.mutex.RUnlock()

	subscriptions := make([]SubscriptionInfo, 0, len(ssm.subscriptionMap))
	for _, sub := range ssm.subscriptionMap {
		subscriptions = append(subscriptions, sub)
	}
	return subscriptions
}

// GetSubscriptionsByExchange returns subscriptions filtered by exchange
func (ssm *StockSubscriptionManager) GetSubscriptionsByExchange(exchange string) []SubscriptionInfo {
	ssm.mutex.RLock()
	defer ssm.mutex.RUnlock()

	var subscriptions []SubscriptionInfo
	for _, sub := range ssm.subscriptionMap {
		if strings.ToUpper(sub.Exchange) == strings.ToUpper(exchange) {
			subscriptions = append(subscriptions, sub)
		}
	}
	return subscriptions
}

// GetStats returns subscription statistics
func (ssm *StockSubscriptionManager) GetStats() map[string]interface{} {
	ssm.mutex.RLock()
	defer ssm.mutex.RUnlock()

	nseCount := 0
	bseCount := 0

	for _, sub := range ssm.subscriptionMap {
		switch strings.ToUpper(sub.Exchange) {
		case "NSE":
			nseCount++
		case "BSE":
			bseCount++
		}
	}

	return map[string]interface{}{
		"total_stocks":        len(ssm.allStocks),
		"active_stocks":       len(ssm.activeStocks),
		"total_subscriptions": len(ssm.subscriptionMap),
		"nse_subscriptions":   nseCount,
		"bse_subscriptions":   bseCount,
		"last_updated":        ssm.lastUpdated.Format(time.RFC3339),
	}
}

// RefreshSubscriptions refreshes the stock data and updates subscriptions
func (ssm *StockSubscriptionManager) RefreshSubscriptions() error {
	log.Printf("🔄 Refreshing stock subscriptions...")

	result, err := ssm.FetchAndFilterStocks()
	if err != nil {
		return fmt.Errorf("failed to refresh stocks: %w", err)
	}

	// Subscribe to new filtered tokens
	if err := ssm.SubscribeToFilteredStocks(); err != nil {
		return fmt.Errorf("failed to subscribe to refreshed tokens: %w", err)
	}

	log.Printf("✅ Subscription refresh completed: %d active subscriptions", len(result.SubscribedTokens))
	return nil
}

// GetSymbolForToken returns the symbol and exchange for a given token
func (ssm *StockSubscriptionManager) GetSymbolForToken(token string) (string, string, bool) {
	ssm.mutex.RLock()
	defer ssm.mutex.RUnlock()

	if sub, exists := ssm.subscriptionMap[token]; exists {
		return sub.Symbol, sub.Exchange, true
	}
	return "", "", false
}
