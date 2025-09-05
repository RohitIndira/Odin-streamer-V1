package main

import (
	"log"

	"github.com/RohitIndira/odin-streamer/internal/stock"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.Printf("🧪 TESTING STOCKSEMOJI API INTEGRATION")
	log.Printf("📊 This will test the StocksEmoji API with real co_code values from database")

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

	// Get current 52-week stats
	stats := stockDB.Get52WeekStats()
	log.Printf("📊 Current 52-week data status:")
	log.Printf("   Total stocks: %v", stats["total_stocks"])
	log.Printf("   Stocks with 52-week data: %v", stats["stocks_with_52week"])
	log.Printf("   Coverage: %.1f%%", stats["coverage_percentage"])
	log.Printf("   Missing 52-week data: %v", stats["missing_52week"])

	// Test StocksEmoji API with sample stocks
	log.Printf("\n🧪 Testing StocksEmoji API...")
	err = stockDB.TestStocksEmojiAPI()
	if err != nil {
		log.Printf("❌ API test failed: %v", err)
		log.Printf("\n💡 Possible solutions:")
		log.Printf("   1. Check if StocksEmoji API is accessible")
		log.Printf("   2. Verify co_code values are correct")
		log.Printf("   3. Check network connectivity")
		log.Printf("   4. Try during market hours")
		return
	}

	log.Printf("\n🎯 API test successful! Ready to update all stocks.")
	log.Printf("❓ Would you like to update 52-week data for all stocks?")
	log.Printf("   This will take approximately 10-15 minutes for 5,275 stocks")
	log.Printf("   Run: go run cmd/update_52week_data.go")
}
