package stock

import (
	"fmt"
	"log"
	"time"
)

// Update52WeekDataFromStocksEmoji fetches 52-week data ONLY for stocks missing it
func (sdb *Database) Update52WeekDataFromStocksEmoji() error {
	log.Printf("🔄 Starting SMART 52-week data update (missing stocks only)...")

	// Get only stocks that are missing 52-week data
	stocksMissingData, err := sdb.GetStocksMissing52WeekData()
	if err != nil {
		return fmt.Errorf("failed to get stocks missing 52-week data: %w", err)
	}

	log.Printf("📊 Found %d stocks missing 52-week data, fetching from API...", len(stocksMissingData))

	if len(stocksMissingData) == 0 {
		log.Printf("🎉 All stocks already have 52-week data! No updates needed.")
		return nil
	}

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

		log.Printf("📦 Processing batch %d-%d (missing stocks only)...", i+1, end)

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

			if successCount%100 == 0 {
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

	log.Printf("✅ SMART 52-week data update completed!")
	log.Printf("   📈 Successfully updated: %d stocks", successCount)
	log.Printf("   ❌ Errors: %d stocks", errorCount)
	log.Printf("   📊 Success rate: %.1f%%", float64(successCount)/float64(len(stocksMissingData))*100)

	return nil
}

// GetStocksMissing52WeekData returns stocks that don't have 52-week data
func (sdb *Database) GetStocksMissing52WeekData() ([]struct {
	Token    string
	Symbol   string
	Exchange string
	CoCode   float64
}, error) {
	query := `SELECT token, symbol, exchange, co_code FROM stock_subscriptions 
	          WHERE status = 'ACTIVE' AND co_code > 0 AND (week_52_high = 0 OR week_52_high IS NULL)`
	rows, err := sdb.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query stocks missing 52-week data: %w", err)
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
