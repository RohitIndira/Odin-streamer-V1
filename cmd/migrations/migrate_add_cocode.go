package main

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.Printf("🔧 MIGRATING DATABASE TO ADD CO_CODE COLUMN")
	log.Printf("📊 This will add co_code column to existing stock_subscriptions table")

	// Open database connection
	db, err := sql.Open("sqlite3", "cmd/streamer/stocks.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check if co_code column already exists
	rows, err := db.Query("PRAGMA table_info(stock_subscriptions)")
	if err != nil {
		log.Fatalf("Failed to get table info: %v", err)
	}
	defer rows.Close()

	coCodeExists := false
	log.Printf("📋 Current database columns:")
	for rows.Next() {
		var cid int
		var name, dataType, dfltValue sql.NullString
		var notNull, pk int

		err := rows.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk)
		if err != nil {
			continue
		}
		log.Printf("   %d: %s (%s)", cid, name.String, dataType.String)

		if name.String == "co_code" {
			coCodeExists = true
		}
	}

	if coCodeExists {
		log.Printf("✅ co_code column already exists, no migration needed")
		return
	}

	// Add co_code column
	log.Printf("🔄 Adding co_code column to stock_subscriptions table...")

	_, err = db.Exec("ALTER TABLE stock_subscriptions ADD COLUMN co_code REAL DEFAULT 0")
	if err != nil {
		log.Fatalf("Failed to add co_code column: %v", err)
	}

	log.Printf("✅ Successfully added co_code column")

	// Verify the column was added
	rows2, err := db.Query("PRAGMA table_info(stock_subscriptions)")
	if err != nil {
		log.Fatalf("Failed to verify table info: %v", err)
	}
	defer rows2.Close()

	log.Printf("📋 Updated database columns:")
	for rows2.Next() {
		var cid int
		var name, dataType, dfltValue sql.NullString
		var notNull, pk int

		err := rows2.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk)
		if err != nil {
			continue
		}
		log.Printf("   %d: %s (%s)", cid, name.String, dataType.String)
	}

	log.Printf("\n✅ DATABASE MIGRATION COMPLETED!")
	log.Printf("🎯 Now you can run: go run cmd/update_cocode.go")
}
