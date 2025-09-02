package stock

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// FetchAndStoreStocks fetches stocks from API and stores them with intelligent filtering
func (sdb *Database) FetchAndStoreStocks() (*StockFilterResult, error) {
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
	return sdb.applyIntelligentFilteringAndStoreComplete(stocksArray)
}

// applyIntelligentFilteringAndStoreComplete applies the complete intelligent filtering logic from the original system
func (sdb *Database) applyIntelligentFilteringAndStoreComplete(stocks []StockInfo) (*StockFilterResult, error) {
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
		// Check if stock is listed and active on exchanges using proper flags
		nseActive := strings.ToUpper(stock.NSEListedFlag) == "Y" && strings.ToUpper(stock.NSEStatus) == "ACTIVE"
		bseActive := strings.ToUpper(stock.BSEListedFlag) == "Y" && strings.ToUpper(stock.BSEStatus) == "ACTIVE"

		// Skip stocks that are not active on any exchange
		if !nseActive && !bseActive {
			continue
		}

		// CORRECTED TOKEN MAPPING LOGIC BASED ON YOUR REQUIREMENTS:
		// 1. Both exchanges active → prefer NSE, use 'code' field as token
		// 2. NSE only → use NSE, use 'code' field as token
		// 3. BSE only → use BSE, use 'code' field as token
		var selectedExchange, selectedSymbol string
		var marketSegment int
		var token string

		if nseActive && bseActive {
			// Case 1: Both exchanges active - prefer NSE, use 'code' field
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			marketSegment = 1                     // NSE market segment
			token = fmt.Sprintf("%d", stock.Code) // Use 'code' field (not co_code)
			bothExchanges++
			log.Printf("📊 FIXED: Stock %s (%s): Both exchanges active, selecting NSE (segment 1, token: %s from 'code' field)", stock.CompanyShortName, stock.NSESymbol, token)
		} else if nseActive {
			// Case 3: NSE only - use 'code' field
			selectedExchange = "NSE"
			selectedSymbol = stock.NSESymbol
			marketSegment = 1                     // NSE market segment
			token = fmt.Sprintf("%d", stock.Code) // Use 'code' field (not co_code)
			nseOnlyCount++
			log.Printf("📈 FIXED: Stock %s (%s): NSE only (segment 1, token: %s from 'code' field)", stock.CompanyShortName, stock.NSESymbol, token)
		} else if bseActive {
			// Case 2: BSE only - use 'code' field
			selectedExchange = "BSE"
			selectedSymbol = stock.BSESymbol      // Use BSESymbol
			marketSegment = 3                     // BSE market segment
			token = fmt.Sprintf("%d", stock.Code) // Use 'code' field (not bsecode)
			bseOnlyCount++
			log.Printf("📉 FIXED: Stock %s (%s): BSE only (segment 3, token: %s from 'code' field)", stock.CompanyShortName, stock.BSESymbol, token)
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
			CreatedAt:     sdb.lastUpdated,
			UpdatedAt:     sdb.lastUpdated,
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
	sdb.updateMetadata("last_api_fetch", sdb.lastUpdated.Format("2006-01-02T15:04:05Z07:00"))
	sdb.updateMetadata("total_stocks", fmt.Sprintf("%d", len(stocks)))
	sdb.updateMetadata("active_stocks", fmt.Sprintf("%d", len(activeStocks)))

	result := &StockFilterResult{
		TotalStocks:      len(stocks),
		ActiveStocks:     len(activeStocks),
		NSEOnlyStocks:    nseOnlyCount,
		BSEOnlyStocks:    bseOnlyCount,
		BothExchanges:    bothExchanges,
		SubscribedTokens: subscribedTokens,
		FilteredAt:       sdb.lastUpdated,
	}

	log.Printf("✅ FIXED SQLite storage completed:")
	log.Printf("   📊 Total stocks from API: %d", result.TotalStocks)
	log.Printf("   ✅ Active stocks stored: %d", result.ActiveStocks)
	log.Printf("   📈 NSE only: %d (segment 1, using 'code' field as token)", result.NSEOnlyStocks)
	log.Printf("   📉 BSE only: %d (segment 3, using 'code' field as token)", result.BSEOnlyStocks)
	log.Printf("   🔄 Both exchanges (NSE preferred): %d (segment 1, using 'code' field)", result.BothExchanges)
	log.Printf("   🎯 Total subscriptions ready: %d", len(subscribedTokens))
	log.Printf("   🔧 TOKEN MAPPING FIXED: All cases use 'code' field as token")

	return result, nil
}

// UpdateCoCodeForAllStocks fetches co_code from companies-details API and updates database
func (sdb *Database) UpdateCoCodeForAllStocks() error {
	log.Printf("🔄 Starting co_code update for all stocks...")

	// Get all stocks from database that don't have co_code yet
	query := `SELECT token, symbol, exchange FROM stock_subscriptions WHERE status = 'ACTIVE' AND (co_code = 0 OR co_code IS NULL)`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query stocks: %w", err)
	}
	defer rows.Close()

	var stocksToUpdate []struct {
		Token    string
		Symbol   string
		Exchange string
	}

	for rows.Next() {
		var token, symbol, exchange string
		if err := rows.Scan(&token, &symbol, &exchange); err != nil {
			continue
		}
		stocksToUpdate = append(stocksToUpdate, struct {
			Token    string
			Symbol   string
			Exchange string
		}{token, symbol, exchange})
	}

	log.Printf("📊 Found %d stocks without co_code, fetching from companies-details API...", len(stocksToUpdate))

	// Fetch all company details from API
	companyDetails, err := sdb.fetchAllCompanyDetails()
	if err != nil {
		return fmt.Errorf("failed to fetch company details: %w", err)
	}

	log.Printf("📊 Fetched %d company records from API", len(companyDetails))

	// Create lookup map: token -> co_code
	tokenToCoCode := make(map[string]float64)
	for _, company := range companyDetails {
		tokenStr := fmt.Sprintf("%d", company.Code)
		tokenToCoCode[tokenStr] = company.CoCode
	}

	// Update database with co_code values
	successCount := 0
	updateStmt, err := sdb.db.Prepare("UPDATE stock_subscriptions SET co_code = ?, updated_at = CURRENT_TIMESTAMP WHERE token = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare update statement: %w", err)
	}
	defer updateStmt.Close()

	for _, stock := range stocksToUpdate {
		if coCode, exists := tokenToCoCode[stock.Token]; exists {
			_, err := updateStmt.Exec(coCode, stock.Token)
			if err != nil {
				log.Printf("⚠️ Failed to update co_code for %s (%s): %v", stock.Symbol, stock.Token, err)
				continue
			}
			successCount++

			if successCount%100 == 0 {
				log.Printf("📊 Updated co_code for %d stocks...", successCount)
			}
		} else {
			log.Printf("⚠️ No co_code found for %s (%s)", stock.Symbol, stock.Token)
		}
	}

	log.Printf("✅ Successfully updated co_code for %d/%d stocks", successCount, len(stocksToUpdate))
	return nil
}

// fetchAllCompanyDetails fetches all company details from the API
func (sdb *Database) fetchAllCompanyDetails() ([]StockInfo, error) {
	url := "https://uatdev.indiratrade.com/companies-details"

	log.Printf("📡 Fetching company details from: %s", url)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch company details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var companies []StockInfo
	err = json.NewDecoder(resp.Body).Decode(&companies)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return companies, nil
}

// GetStocksWithCoCode returns all stocks that have co_code values
func (sdb *Database) GetStocksWithCoCode() ([]struct {
	Token    string
	Symbol   string
	Exchange string
	CoCode   float64
}, error) {
	query := `SELECT token, symbol, exchange, co_code FROM stock_subscriptions WHERE status = 'ACTIVE' AND co_code > 0`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query stocks with co_code: %w", err)
	}
	defer rows.Close()

	var stocks []struct {
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
		stocks = append(stocks, struct {
			Token    string
			Symbol   string
			Exchange string
			CoCode   float64
		}{token, symbol, exchange, coCode})
	}

	return stocks, nil
}

// updateMetadata updates metadata in the database
func (sdb *Database) updateMetadata(key, value string) {
	query := `INSERT OR REPLACE INTO database_metadata (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`
	sdb.db.Exec(query, key, value)
}
