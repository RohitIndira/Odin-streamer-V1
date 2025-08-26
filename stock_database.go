package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// StockDatabase handles SQLite-based stock data management
type StockDatabase struct {
	db          *sql.DB
	apiURL      string
	mutex       sync.RWMutex
	lastUpdated time.Time
	// In-memory maps for instant lookups
	tokenToSymbol   map[string]string // token -> symbol
	tokenToExchange map[string]string // token -> exchange
	symbolToToken   map[string]string // symbol -> token
}

// DatabaseStockEntry represents a stock entry in the database
type DatabaseStockEntry struct {
	Token         string    `json:"token"`
	Symbol        string    `json:"symbol"`
	Exchange      string    `json:"exchange"`
	MarketSegment int       `json:"market_segment"`
	CompanyName   string    `json:"company_name"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewStockDatabase creates a new stock database instance
func NewStockDatabase(dbPath string, apiURL string) (*StockDatabase, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	stockDB := &StockDatabase{
		db:              db,
		apiURL:          apiURL,
		tokenToSymbol:   make(map[string]string),
		tokenToExchange: make(map[string]string),
		symbolToToken:   make(map[string]string),
	}

	// Create tables
	if err := stockDB.createTables(); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	log.Printf("✅ Stock database initialized: %s", dbPath)
	return stockDB, nil
}

// createTables creates the necessary database tables
func (sdb *StockDatabase) createTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS stock_subscriptions (
		token TEXT PRIMARY KEY,
		symbol TEXT NOT NULL,
		exchange TEXT NOT NULL,
		market_segment INTEGER NOT NULL,
		company_name TEXT,
		status TEXT DEFAULT 'ACTIVE',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_symbol ON stock_subscriptions(symbol);
	CREATE INDEX IF NOT EXISTS idx_exchange ON stock_subscriptions(exchange);
	CREATE INDEX IF NOT EXISTS idx_market_segment ON stock_subscriptions(market_segment);
	CREATE INDEX IF NOT EXISTS idx_status ON stock_subscriptions(status);
	
	CREATE TABLE IF NOT EXISTS database_metadata (
		key TEXT PRIMARY KEY,
		value TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := sdb.db.Exec(query)
	return err
}

// FetchAndStoreStocks fetches stocks from API and stores them with intelligent filtering
func (sdb *StockDatabase) FetchAndStoreStocks() (*StockFilterResult, error) {
	log.Printf("🔄 Fetching stocks from API: %s", sdb.apiURL)

	// Fetch data from API
	resp, err := http.Get(sdb.apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stocks: %w", err)
	}
	defer resp.Body.Close()

	var stocksArray []StockInfo
	if err := json.NewDecoder(resp.Body).Decode(&stocksArray); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	log.Printf("✅ Fetched %d stocks from API", len(stocksArray))

	// Apply intelligent filtering and store in database
	return sdb.applyIntelligentFilteringAndStore(stocksArray)
}

// applyIntelligentFilteringAndStore applies intelligent filtering logic and stores in SQLite
func (sdb *StockDatabase) applyIntelligentFilteringAndStore(stocks []StockInfo) (*StockFilterResult, error) {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	log.Printf("🎯 Applying CORRECTED intelligent filtering with proper B2C token mapping...")

	// Begin transaction for bulk insert
	tx, err := sdb.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear existing data
	if _, err := tx.Exec("DELETE FROM stock_subscriptions"); err != nil {
		return nil, fmt.Errorf("failed to clear existing data: %w", err)
	}

	// Prepare insert statement
	stmt, err := tx.Prepare(`
		INSERT INTO stock_subscriptions 
		(token, symbol, exchange, market_segment, company_name, status) 
		VALUES (?, ?, ?, ?, ?, 'ACTIVE')
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	var (
		activeStocks     []DatabaseStockEntry
		subscribedTokens []string
		nseOnlyCount     int
		bseOnlyCount     int
		bothExchanges    int
	)

	for _, stock := range stocks {
		// Check if stock has active status on any exchange
		nseActive := strings.ToUpper(stock.NSEStatus) == "ACTIVE"
		bseActive := strings.ToUpper(stock.BSEStatus) == "ACTIVE"

		// Skip stocks that are not active on any exchange
		if !nseActive && !bseActive {
			continue
		}

		// CORRECTED TOKEN MAPPING LOGIC:
		// For NSE: Use 'code' field as token (B2C API requirement)
		// For BSE: Use 'bsecode' field as token, but use BSESymbol as symbol

		// Apply intelligent subscription priority logic:
		// 1. If active on both NSE and BSE → prefer NSE (market_segment = 1)
		// 2. If active only on BSE → use BSE (market_segment = 3)
		// 3. If active only on NSE → use NSE (market_segment = 1)
		var selectedExchange, selectedSymbol string
		var marketSegment int
		var token string

		if nseActive && bseActive {
			// Both exchanges active - prefer NSE
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			marketSegment = 1                         // NSE market segment
			token = fmt.Sprintf("%.0f", stock.CoCode) // Use 'co_code' field for NSE (B2C API requirement)
			bothExchanges++
			log.Printf("📊 CORRECTED: Stock %s (%s): Both exchanges active, selecting NSE (segment 1, token: %s from 'co_code' field)", stock.CompanyShortName, stock.NSESymbol, token)
		} else if nseActive {
			// NSE only
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			marketSegment = 1                         // NSE market segment
			token = fmt.Sprintf("%.0f", stock.CoCode) // Use 'co_code' field for NSE (B2C API requirement)
			nseOnlyCount++
			log.Printf("📈 CORRECTED: Stock %s (%s): NSE only (segment 1, token: %s from 'co_code' field)", stock.CompanyShortName, stock.NSESymbol, token)
		} else if bseActive {
			// BSE only
			selectedExchange = "BSE"
			selectedSymbol = stock.BSESymbol // Use BSESymbol, not BSECode
			marketSegment = 3                // BSE market segment
			token = stock.BSECode            // Use 'bsecode' field for BSE token (e.g., 500325)
			bseOnlyCount++
			log.Printf("📉 CORRECTED: Stock %s (%s): BSE only (segment 3, token: %s from 'bsecode' field)", stock.CompanyShortName, stock.BSESymbol, token)
		}

		// Skip if no valid symbol or token found
		if selectedSymbol == "" || token == "" || token == "0" {
			log.Printf("⚠️ Skipping stock %s: no valid symbol/token (symbol: %s, token: %s)", stock.CompanyShortName, selectedSymbol, token)
			continue
		}

		// Insert into database
		if _, err := stmt.Exec(token, selectedSymbol, selectedExchange, marketSegment, stock.CompanyName); err != nil {
			log.Printf("⚠️ Error inserting stock %s: %v", selectedSymbol, err)
			continue
		}

		// Create database entry for result
		entry := DatabaseStockEntry{
			Token:         token,
			Symbol:        selectedSymbol,
			Exchange:      selectedExchange,
			MarketSegment: marketSegment,
			CompanyName:   stock.CompanyName,
			Status:        "ACTIVE",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		activeStocks = append(activeStocks, entry)
		subscribedTokens = append(subscribedTokens, token)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Load all data into memory for instant lookups
	if err := sdb.loadIntoMemory(); err != nil {
		return nil, fmt.Errorf("failed to load data into memory: %w", err)
	}

	// Update metadata
	sdb.updateMetadata("last_api_fetch", time.Now().Format(time.RFC3339))
	sdb.updateMetadata("total_stocks", fmt.Sprintf("%d", len(stocks)))
	sdb.updateMetadata("active_stocks", fmt.Sprintf("%d", len(activeStocks)))

	sdb.lastUpdated = time.Now()

	result := &StockFilterResult{
		TotalStocks:      len(stocks),
		ActiveStocks:     len(activeStocks),
		NSEOnlyStocks:    nseOnlyCount,
		BSEOnlyStocks:    bseOnlyCount,
		BothExchanges:    bothExchanges,
		SubscribedTokens: subscribedTokens,
		FilteredAt:       time.Now(),
	}

	log.Printf("✅ CORRECTED SQLite storage completed:")
	log.Printf("   📊 Total stocks from API: %d", result.TotalStocks)
	log.Printf("   ✅ Active stocks stored: %d", result.ActiveStocks)
	log.Printf("   📈 NSE only: %d (segment 1, using 'code' field as token)", result.NSEOnlyStocks)
	log.Printf("   📉 BSE only: %d (segment 3, using 'bsecode' field as token)", result.BSEOnlyStocks)
	log.Printf("   🔄 Both exchanges (NSE preferred): %d (segment 1, using 'code' field)", result.BothExchanges)
	log.Printf("   🎯 Total subscriptions ready: %d", len(subscribedTokens))
	log.Printf("   🔧 TOKEN MAPPING CORRECTED: NSE='code' field, BSE='bsecode' field")

	return result, nil
}

// GetSymbolForToken returns symbol and exchange for a token (instant in-memory lookup)
func (sdb *StockDatabase) GetSymbolForToken(token string) (string, string, bool) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	symbol, symbolExists := sdb.tokenToSymbol[token]
	exchange, exchangeExists := sdb.tokenToExchange[token]

	if symbolExists && exchangeExists {
		return symbol, exchange, true
	}

	return "", "", false
}

// GetTokenForSymbol returns token for a symbol (instant in-memory lookup)
func (sdb *StockDatabase) GetTokenForSymbol(symbol string) (string, bool) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	token, exists := sdb.symbolToToken[symbol]
	return token, exists
}

// GetAllTokenExchangePairs returns all token:exchange pairs for subscription
func (sdb *StockDatabase) GetAllTokenExchangePairs() ([]string, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	query := `SELECT token, exchange FROM stock_subscriptions WHERE status = 'ACTIVE'`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query token pairs: %w", err)
	}
	defer rows.Close()

	var pairs []string
	for rows.Next() {
		var token, exchange string
		if err := rows.Scan(&token, &exchange); err != nil {
			log.Printf("⚠️ Error scanning token pair: %v", err)
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s:%s", token, exchange))
	}

	return pairs, nil
}

// GetStats returns database statistics
func (sdb *StockDatabase) GetStats() map[string]interface{} {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	// Count total stocks
	var totalCount int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE'").Scan(&totalCount)

	// Count by exchange
	var nseCount, bseCount int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE exchange = 'NSE' AND status = 'ACTIVE'").Scan(&nseCount)
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE exchange = 'BSE' AND status = 'ACTIVE'").Scan(&bseCount)

	// Count by market segment
	var segment1Count, segment3Count int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE market_segment = 1 AND status = 'ACTIVE'").Scan(&segment1Count)
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE market_segment = 3 AND status = 'ACTIVE'").Scan(&segment3Count)

	return map[string]interface{}{
		"total_stocks":     totalCount,
		"nse_stocks":       nseCount,
		"bse_stocks":       bseCount,
		"market_segment_1": segment1Count,
		"market_segment_3": segment3Count,
		"last_updated":     sdb.lastUpdated.Format(time.RFC3339),
		"database_ready":   totalCount > 0,
	}
}

// updateMetadata updates metadata in the database
func (sdb *StockDatabase) updateMetadata(key, value string) {
	query := `INSERT OR REPLACE INTO database_metadata (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`
	sdb.db.Exec(query, key, value)
}

// Close closes the database connection
func (sdb *StockDatabase) Close() error {
	if sdb.db != nil {
		return sdb.db.Close()
	}
	return nil
}

// GetAllSubscriptions returns all active subscriptions from database
func (sdb *StockDatabase) GetAllSubscriptions() ([]DatabaseStockEntry, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	query := `SELECT token, symbol, exchange, market_segment, company_name, status, created_at, updated_at 
			  FROM stock_subscriptions WHERE status = 'ACTIVE'`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscriptions: %w", err)
	}
	defer rows.Close()

	var subscriptions []DatabaseStockEntry
	for rows.Next() {
		var entry DatabaseStockEntry
		if err := rows.Scan(&entry.Token, &entry.Symbol, &entry.Exchange, &entry.MarketSegment,
			&entry.CompanyName, &entry.Status, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			log.Printf("⚠️ Error scanning subscription: %v", err)
			continue
		}
		subscriptions = append(subscriptions, entry)
	}

	return subscriptions, nil
}

// loadIntoMemory loads all stock data from SQLite into memory for instant lookups
func (sdb *StockDatabase) loadIntoMemory() error {
	log.Printf("🚀 Loading stock data into memory for instant lookups...")

	query := `SELECT token, symbol, exchange FROM stock_subscriptions WHERE status = 'ACTIVE'`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query stock data: %w", err)
	}
	defer rows.Close()

	// Clear existing maps
	sdb.tokenToSymbol = make(map[string]string)
	sdb.tokenToExchange = make(map[string]string)
	sdb.symbolToToken = make(map[string]string)

	count := 0
	for rows.Next() {
		var token, symbol, exchange string
		if err := rows.Scan(&token, &symbol, &exchange); err != nil {
			log.Printf("⚠️ Error scanning row: %v", err)
			continue
		}

		// Store in memory maps
		sdb.tokenToSymbol[token] = symbol
		sdb.tokenToExchange[token] = exchange
		sdb.symbolToToken[symbol] = token
		count++
	}

	log.Printf("⚡ Loaded %d stock mappings into memory for instant lookups", count)
	log.Printf("🎯 Token mapping will now be INSTANT (no database queries during runtime)")

	return nil
}

// InitializeFromDatabase loads existing data from database into memory (for startup)
func (sdb *StockDatabase) InitializeFromDatabase() error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	// Check if database has data
	var count int
	err := sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check database: %w", err)
	}

	if count > 0 {
		log.Printf("📊 Found %d existing stocks in database, loading into memory...", count)
		return sdb.loadIntoMemory()
	}

	log.Printf("📊 No existing data found in database")
	return nil
}
