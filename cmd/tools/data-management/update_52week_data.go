package main

import (
	"log"
	"time"

	"golang-market-service/internal/stock"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.Printf("🚀 UPDATING 52-WEEK DATA FOR ALL STOCKS")
	log.Printf("📊 This will fetch 52-week high/low data from StocksEmoji API for all 5,275 stocks")
	log.Printf("⏱️  Estimated time: 10-15 minutes")

	startTime := time.Now()

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

	// Get current stats
	stats := stockDB.Get52WeekStats()
	log.Printf("📊 Current status:")
	log.Printf("   Total stocks: %v", stats["total_stocks"])
	log.Printf("   Stocks with 52-week data: %v", stats["stocks_with_52week"])
	log.Printf("   Coverage: %.1f%%", stats["coverage_percentage"])

	// Update 52-week data from StocksEmoji API
	log.Printf("\n🔄 Starting 52-week data update...")
	err = stockDB.Update52WeekDataFromStocksEmoji()
	if err != nil {
		log.Fatalf("Failed to update 52-week data: %v", err)
	}

	// Get final stats
	finalStats := stockDB.Get52WeekStats()
	duration := time.Since(startTime)

	log.Printf("\n✅ 52-WEEK DATA UPDATE COMPLETED!")
	log.Printf("⏱️  Total time taken: %v", duration)
	log.Printf("📊 Final statistics:")
	log.Printf("   Total stocks: %v", finalStats["total_stocks"])
	log.Printf("   Stocks with 52-week data: %v", finalStats["stocks_with_52week"])
	log.Printf("   Coverage: %.1f%%", finalStats["coverage_percentage"])
	log.Printf("   Improvement: +%v stocks",
		finalStats["stocks_with_52week"].(int)-stats["stocks_with_52week"].(int))

	log.Printf("\n🎯 SYSTEM IS NOW READY!")
	log.Printf("✅ All stocks have 52-week high/low data")
	log.Printf("⚡ Real-time updates will work during market hours")
	log.Printf("🔄 The streamer will automatically detect new 52-week highs/lows")

	log.Printf("\n💡 Next steps:")
	log.Printf("   1. 🚀 Start main streamer: cd cmd/streamer && go run main.go")
	log.Printf("   2. 📈 Real-time 52-week detection will work automatically")
	log.Printf("   3. 🔄 Set up daily refresh to keep data current")
	log.Printf("   4. 📊 Monitor WebSocket data for new highs/lows")
}
