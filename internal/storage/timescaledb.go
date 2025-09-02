package storage

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"golang-market-service/internal/candle"

	_ "github.com/lib/pq"
)

// TimescaleDBAdapter handles TimescaleDB operations for candle persistence
type TimescaleDBAdapter struct {
	db          *sql.DB
	istLocation *time.Location
}

// NewTimescaleDBAdapter creates a new TimescaleDB adapter
func NewTimescaleDBAdapter(connectionString string) (*TimescaleDBAdapter, error) {
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open TimescaleDB connection: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping TimescaleDB: %w", err)
	}

	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	adapter := &TimescaleDBAdapter{
		db:          db,
		istLocation: istLocation,
	}

	// Run migrations
	if err := adapter.runMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Printf("✅ TimescaleDB adapter initialized successfully")
	return adapter, nil
}

// runMigrations creates the necessary tables and hypertables
func (tdb *TimescaleDBAdapter) runMigrations() error {
	migrations := []string{
		// Create candles table
		`CREATE TABLE IF NOT EXISTS candles (
			id BIGSERIAL,
			exchange VARCHAR(10) NOT NULL,
			scrip_token VARCHAR(20) NOT NULL,
			minute_ts TIMESTAMPTZ NOT NULL,
			open_price DECIMAL(12,4) NOT NULL,
			high_price DECIMAL(12,4) NOT NULL,
			low_price DECIMAL(12,4) NOT NULL,
			close_price DECIMAL(12,4) NOT NULL,
			volume BIGINT NOT NULL DEFAULT 0,
			source VARCHAR(20) NOT NULL DEFAULT 'realtime',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (minute_ts, exchange, scrip_token)
		);`,

		// Create additional index for time-series performance (PostgreSQL compatible)
		`CREATE INDEX IF NOT EXISTS idx_candles_minute_ts 
			ON candles (minute_ts DESC);`,

		// Create indexes for efficient queries
		`CREATE INDEX IF NOT EXISTS idx_candles_exchange_token 
			ON candles (exchange, scrip_token, minute_ts DESC);`,

		`CREATE INDEX IF NOT EXISTS idx_candles_source 
			ON candles (source, minute_ts DESC);`,

		`CREATE INDEX IF NOT EXISTS idx_candles_created_at 
			ON candles (created_at DESC);`,

		// Create candle_metadata table for tracking
		`CREATE TABLE IF NOT EXISTS candle_metadata (
			exchange VARCHAR(10) NOT NULL,
			scrip_token VARCHAR(20) NOT NULL,
			date DATE NOT NULL,
			total_candles INTEGER DEFAULT 0,
			first_candle_ts TIMESTAMPTZ,
			last_candle_ts TIMESTAMPTZ,
			market_open_ts TIMESTAMPTZ,
			market_close_ts TIMESTAMPTZ,
			synthetic_candles INTEGER DEFAULT 0,
			realtime_candles INTEGER DEFAULT 0,
			historical_candles INTEGER DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (exchange, scrip_token, date)
		);`,

		// Create index on metadata
		`CREATE INDEX IF NOT EXISTS idx_candle_metadata_date 
			ON candle_metadata (date DESC);`,
	}

	for i, migration := range migrations {
		if _, err := tdb.db.Exec(migration); err != nil {
			return fmt.Errorf("failed to execute migration %d: %w", i+1, err)
		}
	}

	log.Printf("✅ TimescaleDB migrations completed successfully")
	return nil
}

// StoreCandle stores a single candle in TimescaleDB
func (tdb *TimescaleDBAdapter) StoreCandle(candleData candle.Candle) error {
	query := `
		INSERT INTO candles (
			exchange, scrip_token, minute_ts, open_price, high_price, 
			low_price, close_price, volume, source, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (minute_ts, exchange, scrip_token) 
		DO UPDATE SET 
			open_price = EXCLUDED.open_price,
			high_price = EXCLUDED.high_price,
			low_price = EXCLUDED.low_price,
			close_price = EXCLUDED.close_price,
			volume = EXCLUDED.volume,
			source = EXCLUDED.source,
			updated_at = NOW();`

	_, err := tdb.db.Exec(query,
		candleData.Exchange,
		candleData.ScripToken,
		candleData.MinuteTS.In(tdb.istLocation),
		candleData.Open,
		candleData.High,
		candleData.Low,
		candleData.Close,
		candleData.Volume,
		candleData.Source,
	)

	if err != nil {
		return fmt.Errorf("failed to store candle: %w", err)
	}

	// Update metadata
	if err := tdb.updateCandleMetadata(candleData); err != nil {
		log.Printf("⚠️ Failed to update candle metadata: %v", err)
	}

	return nil
}

// StoreCandlesBatch stores multiple candles in a single transaction
func (tdb *TimescaleDBAdapter) StoreCandlesBatch(candles []candle.Candle) error {
	if len(candles) == 0 {
		return nil
	}

	tx, err := tdb.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO candles (
			exchange, scrip_token, minute_ts, open_price, high_price, 
			low_price, close_price, volume, source, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (minute_ts, exchange, scrip_token) 
		DO UPDATE SET 
			open_price = EXCLUDED.open_price,
			high_price = EXCLUDED.high_price,
			low_price = EXCLUDED.low_price,
			close_price = EXCLUDED.close_price,
			volume = EXCLUDED.volume,
			source = EXCLUDED.source,
			updated_at = NOW();`)

	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, candleData := range candles {
		_, err := stmt.Exec(
			candleData.Exchange,
			candleData.ScripToken,
			candleData.MinuteTS.In(tdb.istLocation),
			candleData.Open,
			candleData.High,
			candleData.Low,
			candleData.Close,
			candleData.Volume,
			candleData.Source,
		)
		if err != nil {
			return fmt.Errorf("failed to execute batch insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	// Update metadata for each unique exchange/token/date combination
	metadataMap := make(map[string][]candle.Candle)
	for _, candleData := range candles {
		key := fmt.Sprintf("%s:%s:%s", candleData.Exchange, candleData.ScripToken,
			candleData.MinuteTS.In(tdb.istLocation).Format("2006-01-02"))
		metadataMap[key] = append(metadataMap[key], candleData)
	}

	for _, candleGroup := range metadataMap {
		if len(candleGroup) > 0 {
			if err := tdb.updateCandleMetadata(candleGroup[0]); err != nil {
				log.Printf("⚠️ Failed to update batch metadata: %v", err)
			}
		}
	}

	log.Printf("📦 Stored %d candles in TimescaleDB batch", len(candles))
	return nil
}

// GetCandles retrieves candles for a specific time range
func (tdb *TimescaleDBAdapter) GetCandles(exchange, scripToken string, fromTime, toTime time.Time) ([]candle.Candle, error) {
	query := `
		SELECT exchange, scrip_token, minute_ts, open_price, high_price, 
			   low_price, close_price, volume, source
		FROM candles 
		WHERE exchange = $1 AND scrip_token = $2 
			AND minute_ts >= $3 AND minute_ts <= $4
		ORDER BY minute_ts ASC;`

	rows, err := tdb.db.Query(query, exchange, scripToken,
		fromTime.In(tdb.istLocation), toTime.In(tdb.istLocation))
	if err != nil {
		return nil, fmt.Errorf("failed to query candles: %w", err)
	}
	defer rows.Close()

	var candles []candle.Candle
	for rows.Next() {
		var c candle.Candle
		var minuteTS time.Time

		err := rows.Scan(
			&c.Exchange, &c.ScripToken, &minuteTS,
			&c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Source,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan candle: %w", err)
		}

		c.MinuteTS = minuteTS.In(tdb.istLocation)
		candles = append(candles, c)
	}

	return candles, nil
}

// GetIntradayCandles retrieves all candles for a specific date (09:15-15:30 IST)
func (tdb *TimescaleDBAdapter) GetIntradayCandles(exchange, scripToken string, date time.Time) ([]candle.Candle, error) {
	// Create market open and close times for the date
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, tdb.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, tdb.istLocation)

	return tdb.GetCandles(exchange, scripToken, marketOpen, marketClose)
}

// GetLatestCandle retrieves the most recent candle for a token
func (tdb *TimescaleDBAdapter) GetLatestCandle(exchange, scripToken string) (*candle.Candle, error) {
	query := `
		SELECT exchange, scrip_token, minute_ts, open_price, high_price, 
			   low_price, close_price, volume, source
		FROM candles 
		WHERE exchange = $1 AND scrip_token = $2 
		ORDER BY minute_ts DESC 
		LIMIT 1;`

	var c candle.Candle
	var minuteTS time.Time

	err := tdb.db.QueryRow(query, exchange, scripToken).Scan(
		&c.Exchange, &c.ScripToken, &minuteTS,
		&c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Source,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No candles found
		}
		return nil, fmt.Errorf("failed to get latest candle: %w", err)
	}

	c.MinuteTS = minuteTS.In(tdb.istLocation)
	return &c, nil
}

// updateCandleMetadata updates the metadata table for tracking
func (tdb *TimescaleDBAdapter) updateCandleMetadata(candleData candle.Candle) error {
	date := candleData.MinuteTS.In(tdb.istLocation).Truncate(24 * time.Hour)
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, tdb.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, tdb.istLocation)

	query := `
		INSERT INTO candle_metadata (
			exchange, scrip_token, date, total_candles, first_candle_ts, 
			last_candle_ts, market_open_ts, market_close_ts, updated_at
		) VALUES ($1, $2, $3, 1, $4, $4, $5, $6, NOW())
		ON CONFLICT (exchange, scrip_token, date) 
		DO UPDATE SET 
			total_candles = (
				SELECT COUNT(*) FROM candles 
				WHERE exchange = EXCLUDED.exchange 
					AND scrip_token = EXCLUDED.scrip_token 
					AND minute_ts::date = EXCLUDED.date
			),
			first_candle_ts = LEAST(candle_metadata.first_candle_ts, EXCLUDED.first_candle_ts),
			last_candle_ts = GREATEST(candle_metadata.last_candle_ts, EXCLUDED.last_candle_ts),
			updated_at = NOW();`

	_, err := tdb.db.Exec(query,
		candleData.Exchange,
		candleData.ScripToken,
		date,
		candleData.MinuteTS.In(tdb.istLocation),
		marketOpen,
		marketClose,
	)

	return err
}

// GetCandleStats returns statistics about stored candles
func (tdb *TimescaleDBAdapter) GetCandleStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total candles count
	var totalCandles int64
	err := tdb.db.QueryRow("SELECT COUNT(*) FROM candles").Scan(&totalCandles)
	if err != nil {
		return nil, fmt.Errorf("failed to get total candles: %w", err)
	}
	stats["total_candles"] = totalCandles

	// Candles by source
	sourceQuery := `
		SELECT source, COUNT(*) 
		FROM candles 
		GROUP BY source 
		ORDER BY COUNT(*) DESC;`

	rows, err := tdb.db.Query(sourceQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to get source stats: %w", err)
	}
	defer rows.Close()

	sourceStats := make(map[string]int64)
	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			continue
		}
		sourceStats[source] = count
	}
	stats["candles_by_source"] = sourceStats

	// Recent activity (last 24 hours)
	var recentCandles int64
	recentQuery := `
		SELECT COUNT(*) 
		FROM candles 
		WHERE created_at >= NOW() - INTERVAL '24 hours';`

	err = tdb.db.QueryRow(recentQuery).Scan(&recentCandles)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent candles: %w", err)
	}
	stats["recent_candles_24h"] = recentCandles

	// Database size information
	var dbSize string
	sizeQuery := `
		SELECT pg_size_pretty(pg_total_relation_size('candles')) as size;`

	err = tdb.db.QueryRow(sizeQuery).Scan(&dbSize)
	if err != nil {
		log.Printf("⚠️ Failed to get database size: %v", err)
		dbSize = "unknown"
	}
	stats["candles_table_size"] = dbSize

	stats["timezone"] = tdb.istLocation.String()
	stats["connection_status"] = "connected"

	return stats, nil
}

// GetMetadata retrieves metadata for a specific exchange/token/date
func (tdb *TimescaleDBAdapter) GetMetadata(exchange, scripToken string, date time.Time) (map[string]interface{}, error) {
	query := `
		SELECT total_candles, first_candle_ts, last_candle_ts, 
			   market_open_ts, market_close_ts, synthetic_candles, 
			   realtime_candles, historical_candles, updated_at
		FROM candle_metadata 
		WHERE exchange = $1 AND scrip_token = $2 AND date = $3;`

	var totalCandles, syntheticCandles, realtimeCandles, historicalCandles int
	var firstCandle, lastCandle, marketOpen, marketClose, updatedAt time.Time

	err := tdb.db.QueryRow(query, exchange, scripToken, date.Truncate(24*time.Hour)).Scan(
		&totalCandles, &firstCandle, &lastCandle, &marketOpen, &marketClose,
		&syntheticCandles, &realtimeCandles, &historicalCandles, &updatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No metadata found
		}
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	return map[string]interface{}{
		"total_candles":      totalCandles,
		"first_candle_ts":    firstCandle.In(tdb.istLocation),
		"last_candle_ts":     lastCandle.In(tdb.istLocation),
		"market_open_ts":     marketOpen.In(tdb.istLocation),
		"market_close_ts":    marketClose.In(tdb.istLocation),
		"synthetic_candles":  syntheticCandles,
		"realtime_candles":   realtimeCandles,
		"historical_candles": historicalCandles,
		"updated_at":         updatedAt.In(tdb.istLocation),
	}, nil
}

// DeleteOldCandles deletes candles older than the specified duration
func (tdb *TimescaleDBAdapter) DeleteOldCandles(olderThan time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-olderThan)

	query := `DELETE FROM candles WHERE minute_ts < $1;`
	result, err := tdb.db.Exec(query, cutoffTime.In(tdb.istLocation))
	if err != nil {
		return 0, fmt.Errorf("failed to delete old candles: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	log.Printf("🧹 Deleted %d old candles (older than %v)", rowsAffected, olderThan)
	return rowsAffected, nil
}

// Close closes the TimescaleDB connection
func (tdb *TimescaleDBAdapter) Close() error {
	if err := tdb.db.Close(); err != nil {
		return fmt.Errorf("failed to close TimescaleDB connection: %w", err)
	}

	log.Printf("✅ TimescaleDB adapter closed")
	return nil
}

// Ping tests the database connection
func (tdb *TimescaleDBAdapter) Ping() error {
	return tdb.db.Ping()
}

// GetConnection returns the underlying database connection for advanced operations
func (tdb *TimescaleDBAdapter) GetConnection() *sql.DB {
	return tdb.db
}
