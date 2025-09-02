package stock

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// StockInfo represents stock information from API
type StockInfo struct {
	CoCode           float64 `json:"co_code"`
	CompanyName      string  `json:"companyname"`
	CompanyShortName string  `json:"companyshortname"`
	NSESymbol        string  `json:"nsesymbol"`
	BSESymbol        string  `json:"BSESymbol"`
	BSECode          string  `json:"bsecode"`
	NSEStatus        string  `json:"NSEStatus"`
	BSEStatus        string  `json:"BSEStatus"`
	Code             int     `json:"code"` // Main token field
	ISIN             string  `json:"isin"`
	BSEListedFlag    string  `json:"bselistedflag"`
	NSEListedFlag    string  `json:"nselistedflag"`
}

// StocksEmojiResponse represents the StocksEmoji API response
type StocksEmojiResponse struct {
	CoCode      float64 `json:"Co_COde"`
	CompanyName string  `json:"CompanyName"`
	Symbol      string  `json:"nsesymbol"`
	LastPrice   float64 `json:"PriceBSE"`
	High52Week  float64 `json:"52WeekHigh"`
	Low52Week   float64 `json:"52WeekLow"`
	DayHigh     float64 `json:"dayhigh"`
	DayLow      float64 `json:"daylow"`
	Volume      int64   `json:"volume"`
	LastUpdated string  `json:"lastupdated"`
}

// StocksEmojiAPIWrapper represents the API wrapper response
type StocksEmojiAPIWrapper struct {
	Data    []StocksEmojiResponse `json:"data"`
	Success bool                  `json:"success"`
	Message string                `json:"message"`
}

// StockFilterResult represents the result of stock filtering
type StockFilterResult struct {
	TotalStocks      int       `json:"total_stocks"`
	ActiveStocks     int       `json:"active_stocks"`
	NSEOnlyStocks    int       `json:"nse_only_stocks"`
	BSEOnlyStocks    int       `json:"bse_only_stocks"`
	BothExchanges    int       `json:"both_exchanges"`
	SubscribedTokens []string  `json:"subscribed_tokens"`
	FilteredAt       time.Time `json:"filtered_at"`
}

// Database handles SQLite-based stock data management
type Database struct {
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

// NewDatabase creates a new stock database instance
func NewDatabase(dbPath string, apiURL string) (*Database, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	stockDB := &Database{
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
func (sdb *Database) createTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS stock_subscriptions (
		token TEXT PRIMARY KEY,
		symbol TEXT NOT NULL,
		exchange TEXT NOT NULL,
		market_segment INTEGER NOT NULL,
		company_name TEXT,
		co_code REAL DEFAULT 0,
		status TEXT DEFAULT 'ACTIVE',
		week_52_high REAL DEFAULT 0,
		week_52_low REAL DEFAULT 0,
		week_52_high_date TEXT DEFAULT '',
		week_52_low_date TEXT DEFAULT '',
		last_close REAL DEFAULT 0,
		day_high REAL DEFAULT 0,
		day_low REAL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_symbol ON stock_subscriptions(symbol);
	CREATE INDEX IF NOT EXISTS idx_exchange ON stock_subscriptions(exchange);
	CREATE INDEX IF NOT EXISTS idx_market_segment ON stock_subscriptions(market_segment);
	CREATE INDEX IF NOT EXISTS idx_status ON stock_subscriptions(status);
	CREATE INDEX IF NOT EXISTS idx_week_52_high ON stock_subscriptions(week_52_high);
	CREATE INDEX IF NOT EXISTS idx_week_52_low ON stock_subscriptions(week_52_low);
	CREATE INDEX IF NOT EXISTS idx_co_code ON stock_subscriptions(co_code);
	
	CREATE TABLE IF NOT EXISTS database_metadata (
		key TEXT PRIMARY KEY,
		value TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	// Add co_code column if it doesn't exist (for existing databases)
	sdb.db.Exec("ALTER TABLE stock_subscriptions ADD COLUMN co_code REAL DEFAULT 0")

	_, err := sdb.db.Exec(query)
	return err
}

// GetSymbolForToken returns symbol and exchange for a token (instant in-memory lookup)
func (sdb *Database) GetSymbolForToken(token string) (string, string, bool) {
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
func (sdb *Database) GetTokenForSymbol(symbol string) (string, bool) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	token, exists := sdb.symbolToToken[symbol]
	return token, exists
}

// GetAllTokenExchangePairs returns all token:exchange pairs for Python bridge
func (sdb *Database) GetAllTokenExchangePairs() ([]string, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	pairs := make([]string, 0, len(sdb.tokenToSymbol))

	for token, _ := range sdb.tokenToSymbol {
		if exchange, exists := sdb.tokenToExchange[token]; exists {
			// Format: token:exchange (e.g., "476:NSE" or "500410:BSE")
			pair := fmt.Sprintf("%s:%s", token, exchange)
			pairs = append(pairs, pair)
		}
	}

	log.Printf("📊 Generated %d token:exchange pairs for Python bridge", len(pairs))
	return pairs, nil
}

// InitializeFromDatabase loads existing data from database into memory (for startup)
func (sdb *Database) InitializeFromDatabase() error {
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

// loadIntoMemory loads all stock data from SQLite into memory for instant lookups
func (sdb *Database) loadIntoMemory() error {
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

// GetStats returns database statistics
func (sdb *Database) GetStats() map[string]interface{} {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	// Count total stocks
	var totalCount int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE'").Scan(&totalCount)

	// Count by exchange
	var nseCount, bseCount int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE exchange = 'NSE' AND status = 'ACTIVE'").Scan(&nseCount)
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE exchange = 'BSE' AND status = 'ACTIVE'").Scan(&bseCount)

	return map[string]interface{}{
		"total_stocks":   totalCount,
		"nse_stocks":     nseCount,
		"bse_stocks":     bseCount,
		"last_updated":   sdb.lastUpdated.Format(time.RFC3339),
		"database_ready": totalCount > 0,
	}
}

// UpdateMissing52WeekDataFromStocksEmoji intelligently updates only stocks missing 52-week data
func (sdb *Database) UpdateMissing52WeekDataFromStocksEmoji() error {
	log.Printf("🧠 Starting INTELLIGENT 52-week data update (only missing data)...")

	// Get stocks that are missing 52-week data (week_52_high = 0 or NULL)
	query := `SELECT token, symbol, exchange, co_code FROM stock_subscriptions 
	          WHERE status = 'ACTIVE' AND co_code > 0 AND (week_52_high = 0 OR week_52_high IS NULL)`

	rows, err := sdb.db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query stocks missing 52-week data: %w", err)
	}
	defer rows.Close()

	var stocksMissingData []struct {
		Token    string
		Symbol   string
		Exchange string
		CoCode   float64
	}

	for rows.Next() {
		var token, symbol, exchange string
		var coCode float64
		if err := rows.Scan(&token, &symbol, &exchange, &coCode); err != nil {
			continue
		}
		stocksMissingData = append(stocksMissingData, struct {
			Token    string
			Symbol   string
			Exchange string
			CoCode   float64
		}{token, symbol, exchange, coCode})
	}

	// Get total count for comparison
	var totalStocks, stocksWithData int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0").Scan(&totalStocks)
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0 AND week_52_high > 0").Scan(&stocksWithData)

	log.Printf("📊 52-week data analysis:")
	log.Printf("   📈 Total stocks with co_code: %d", totalStocks)
	log.Printf("   ✅ Already have 52-week data: %d", stocksWithData)
	log.Printf("   ❌ Missing 52-week data: %d", len(stocksMissingData))
	log.Printf("   🎯 Coverage: %.1f%%", float64(stocksWithData)/float64(totalStocks)*100)

	if len(stocksMissingData) == 0 {
		log.Printf("🎉 All stocks already have 52-week data! No updates needed.")
		return nil
	}

	log.Printf("🔄 Fetching 52-week data for %d missing stocks...", len(stocksMissingData))

	// Prepare update statement
	updateStmt, err := sdb.db.Prepare(`
		UPDATE stock_subscriptions 
		SET week_52_high = ?, week_52_low = ?, last_close = ?, 
		    week_52_high_date = ?, week_52_low_date = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE token = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare update statement: %w", err)
	}
	defer updateStmt.Close()

	successCount := 0
	errorCount := 0
	today := time.Now().Format("2006-01-02")

	// Process stocks in batches to be respectful to the API
	batchSize := 10
	for i := 0; i < len(stocksMissingData); i += batchSize {
		end := i + batchSize
		if end > len(stocksMissingData) {
			end = len(stocksMissingData)
		}

		log.Printf("📦 Processing batch %d-%d of missing data...", i+1, end)

		for j := i; j < end; j++ {
			stock := stocksMissingData[j]

			// Fetch 52-week data from StocksEmoji API
			data, err := sdb.fetchStocksEmoji52WeekData(stock.CoCode)
			if err != nil {
				log.Printf("⚠️ Failed to fetch data for %s (co_code: %.0f): %v", stock.Symbol, stock.CoCode, err)
				errorCount++
				continue
			}

			// Update database with 52-week data
			_, err = updateStmt.Exec(
				data.High52Week, data.Low52Week, data.LastPrice,
				today, today, // Use today's date for both high and low dates
				stock.Token,
			)
			if err != nil {
				log.Printf("⚠️ Failed to update database for %s: %v", stock.Symbol, err)
				errorCount++
				continue
			}

			successCount++

			if successCount%50 == 0 {
				log.Printf("📊 Updated 52-week data for %d missing stocks...", successCount)
			}

			// Be respectful to the API
			time.Sleep(100 * time.Millisecond)
		}

		// Longer pause between batches
		if end < len(stocksMissingData) {
			time.Sleep(1 * time.Second)
		}
	}

	// Final statistics
	finalStocksWithData := stocksWithData + successCount
	finalCoverage := float64(finalStocksWithData) / float64(totalStocks) * 100

	log.Printf("✅ INTELLIGENT 52-week data update completed!")
	log.Printf("   📊 Preserved existing data: %d stocks", stocksWithData)
	log.Printf("   📈 Successfully added: %d stocks", successCount)
	log.Printf("   ❌ Errors: %d stocks", errorCount)
	log.Printf("   🎯 Final coverage: %d/%d (%.1f%%)", finalStocksWithData, totalStocks, finalCoverage)
	log.Printf("   💡 Smart update: Only fetched missing data, preserved existing!")

	return nil
}

// fetchStocksEmoji52WeekData fetches 52-week data for a specific co_code (shared method)
func (sdb *Database) fetchStocksEmoji52WeekData(coCode float64) (*StocksEmojiResponse, error) {
	url := fmt.Sprintf("https://admin.stocksemoji.com/api/cmot/EquityScoreboard/%.0f", coCode)

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var wrapper StocksEmojiAPIWrapper
	err = json.NewDecoder(resp.Body).Decode(&wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	if !wrapper.Success {
		return nil, fmt.Errorf("API returned error: %s", wrapper.Message)
	}

	if len(wrapper.Data) == 0 {
		return nil, fmt.Errorf("API returned no data")
	}

	return &wrapper.Data[0], nil
}

// Get52WeekStats returns 52-week data statistics
func (sdb *Database) Get52WeekStats() map[string]interface{} {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	// Count total stocks with co_code
	var totalStocks int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0").Scan(&totalStocks)

	// Count stocks with 52-week data
	var stocksWith52Week int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0 AND week_52_high > 0").Scan(&stocksWith52Week)

	// Count stocks missing 52-week data
	var missing52Week int
	sdb.db.QueryRow("SELECT COUNT(*) FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0 AND (week_52_high = 0 OR week_52_high IS NULL)").Scan(&missing52Week)

	// Calculate coverage percentage
	coveragePercentage := float64(0)
	if totalStocks > 0 {
		coveragePercentage = float64(stocksWith52Week) / float64(totalStocks) * 100
	}

	return map[string]interface{}{
		"total_stocks":        totalStocks,
		"stocks_with_52week":  stocksWith52Week,
		"missing_52week":      missing52Week,
		"coverage_percentage": coveragePercentage,
		"last_updated":        sdb.lastUpdated.Format(time.RFC3339),
	}
}

// TestStocksEmojiAPI tests the StocksEmoji API connectivity
func (sdb *Database) TestStocksEmojiAPI() error {
	log.Printf("🧪 Testing StocksEmoji API with sample stocks...")

	// Get a few sample stocks with co_code for testing
	query := `SELECT symbol, co_code FROM stock_subscriptions 
	          WHERE status = 'ACTIVE' AND co_code > 0 
	          ORDER BY RANDOM() LIMIT 5`

	rows, err := sdb.db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to get sample stocks: %w", err)
	}
	defer rows.Close()

	successCount := 0
	totalCount := 0

	for rows.Next() {
		var symbol string
		var coCode float64
		if err := rows.Scan(&symbol, &coCode); err != nil {
			continue
		}

		totalCount++
		log.Printf("📊 Testing %s (co_code: %.0f)...", symbol, coCode)

		// Test API call
		data, err := sdb.fetchStocksEmoji52WeekData(coCode)
		if err != nil {
			log.Printf("   ❌ FAILED: %v", err)
			continue
		}

		log.Printf("   ✅ SUCCESS! : High=%.2f, Low=%.2f, Last=%.2f",
			data.High52Week, data.Low52Week, data.LastPrice)
		log.Printf("   📊 Volume: %d, Updated: %s", data.Volume, data.LastUpdated)
		successCount++

		// Small delay between tests
		time.Sleep(1 * time.Second)
	}

	log.Printf("\n📊 API TEST RESULTS:")
	log.Printf("   ✅ Successfully tested: %d/%d stocks", successCount, totalCount)
	log.Printf("   📈 Success rate: %.1f%%", float64(successCount)/float64(totalCount)*100)

	if successCount == 0 {
		return fmt.Errorf("API test failed - no successful responses")
	}

	log.Printf("\n🎯 API is working! Ready to update all %d stocks", totalCount)
	return nil
}

// Close closes the database connection
func (sdb *Database) Close() error {
	if sdb.db != nil {
		return sdb.db.Close()
	}
	return nil
}
