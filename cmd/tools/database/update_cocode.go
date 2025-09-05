package main

import (
	"log"

	"github.com/RohitIndira/odin-streamer/internal/stock"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.Printf("🔧 UPDATING CO_CODE FOR ALL STOCKS IN DATABASE")
	log.Printf("📊 This will fetch co_code from companies-details API and update database")

	// Initialize stock database
	stockDB, err := stock.NewDatabase("cmd/streamer/stocks.db", "")
	if err != nil {
		log.Fatalf("Failed to create stock database: %v", err)
	}
	defer stockDB.Close()

	// Load existing data into memory
	err = stockDB.InitializeFromDatabase()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	stats := stockDB.GetStats()
	log.Printf("📊 Database contains %v stocks", stats["total_stocks"])

	// Update co_code for all stocks
	err = stockDB.UpdateCoCodeForAllStocks()
	if err != nil {
		log.Fatalf("Failed to update co_code: %v", err)
	}

	// Check results
	stocksWithCoCode, err := stockDB.GetStocksWithCoCode()
	if err != nil {
		log.Printf("⚠️ Failed to get stocks with co_code: %v", err)
	} else {
		log.Printf("✅ Successfully updated co_code for %d stocks", len(stocksWithCoCode))

		// Show sample of updated stocks
		log.Printf("\n📋 Sample of stocks with co_code:")
		for i, stock := range stocksWithCoCode {
			if i >= 10 { // Show first 10
				break
			}
			log.Printf("   %s (%s): Token=%s, CoCode=%.0f",
				stock.Symbol, stock.Exchange, stock.Token, stock.CoCode)
		}
	}

	log.Printf("\n✅ CO_CODE UPDATE COMPLETED!")
	log.Printf("🎯 Next step: Use these co_code values with StocksEmoji API for 52-week data")
}
