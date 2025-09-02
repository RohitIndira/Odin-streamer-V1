package stock

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Week52Data represents 52-week high/low data for a stock
type Week52Data struct {
	Symbol         string    `json:"symbol"`
	Token          string    `json:"token"`
	Exchange       string    `json:"exchange"`
	Week52High     float64   `json:"week_52_high"`
	Week52Low      float64   `json:"week_52_low"`
	Week52HighDate string    `json:"week_52_high_date"`
	Week52LowDate  string    `json:"week_52_low_date"`
	LastClose      float64   `json:"last_close"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// BhavCopyRecord represents a single record from NSE/BSE BhavCopy
type BhavCopyRecord struct {
	Symbol    string  `json:"symbol"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	TradeDate string  `json:"trade_date"`
}

// Week52Manager manages 52-week high/low data
type Week52Manager struct {
	db          *sql.DB
	stockDB     *Database
	istLocation *time.Location
}

// NewWeek52Manager creates a new 52-week data manager
func NewWeek52Manager(stockDB *Database) (*Week52Manager, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	manager := &Week52Manager{
		db:          stockDB.db,
		stockDB:     stockDB,
		istLocation: istLocation,
	}

	log.Printf("✅ Week52Manager initialized with IST timezone")
	return manager, nil
}

// DownloadNSEBhavCopy downloads NSE BhavCopy data for a specific date
func (w52 *Week52Manager) DownloadNSEBhavCopy(date time.Time) ([]BhavCopyRecord, error) {
	// Format date for NSE BhavCopy URL (YYYYMMDD format)
	dateStr := date.Format("20060102")

	// Build URL for NSE BhavCopy zip file
	zipURL := fmt.Sprintf("https://nsearchives.nseindia.com/content/cm/BhavCopy_NSE_CM_0_0_0_%s_F_0000.csv.zip", dateStr)

	log.Printf("🔄 Downloading NSE BhavCopy for %s: %s", date.Format("2006-01-02"), zipURL)

	// Create HTTP request with proper headers
	req, err := http.NewRequest("GET", zipURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download NSE BhavCopy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NSE BhavCopy download failed with status %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Open zip file
	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}

	if len(zipReader.File) == 0 {
		return nil, fmt.Errorf("empty zip file")
	}

	// Open CSV file inside zip
	csvFile := zipReader.File[0]
	csvReader, err := csvFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer csvReader.Close()

	// Parse CSV
	reader := csv.NewReader(csvReader)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("empty CSV file")
	}

	// Find column indices
	header := records[0]
	symbolIdx := findColumnIndex(header, "TckrSymb")
	highIdx := findColumnIndex(header, "HghPric")
	lowIdx := findColumnIndex(header, "LwPric")
	closeIdx := findColumnIndex(header, "ClsPric")
	dateIdx := findColumnIndex(header, "TradDt")

	if symbolIdx == -1 || highIdx == -1 || lowIdx == -1 || closeIdx == -1 || dateIdx == -1 {
		return nil, fmt.Errorf("required columns not found in NSE BhavCopy")
	}

	var bhavData []BhavCopyRecord

	// Process data rows (skip header)
	for i := 1; i < len(records); i++ {
		record := records[i]
		maxIdx := max(max(max(max(symbolIdx, highIdx), lowIdx), closeIdx), dateIdx)
		if len(record) <= maxIdx {
			continue
		}

		high, err := strconv.ParseFloat(record[highIdx], 64)
		if err != nil {
			continue
		}

		low, err := strconv.ParseFloat(record[lowIdx], 64)
		if err != nil {
			continue
		}

		close, err := strconv.ParseFloat(record[closeIdx], 64)
		if err != nil {
			continue
		}

		bhavRecord := BhavCopyRecord{
			Symbol:    strings.TrimSpace(record[symbolIdx]),
			High:      high,
			Low:       low,
			Close:     close,
			TradeDate: strings.TrimSpace(record[dateIdx]),
		}

		bhavData = append(bhavData, bhavRecord)
	}

	log.Printf("✅ Downloaded %d NSE records for %s", len(bhavData), date.Format("2006-01-02"))
	return bhavData, nil
}

// DownloadBSEBhavCopy downloads BSE BhavCopy data for a specific date
func (w52 *Week52Manager) DownloadBSEBhavCopy(date time.Time) ([]BhavCopyRecord, error) {
	// Format date for BSE BhavCopy URL (DDMMYY format)
	dateStr := date.Format("020106")

	// Build URL for BSE BhavCopy zip file
	zipURL := fmt.Sprintf("https://www.bseindia.com/download/BhavCopy/Equity/EQ%s_CSV.ZIP", dateStr)

	log.Printf("🔄 Downloading BSE BhavCopy for %s: %s", date.Format("2006-01-02"), zipURL)

	// Create HTTP request with proper headers
	req, err := http.NewRequest("GET", zipURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download BSE BhavCopy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BSE BhavCopy download failed with status %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Open zip file
	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}

	if len(zipReader.File) == 0 {
		return nil, fmt.Errorf("empty zip file")
	}

	// Open CSV file inside zip
	csvFile := zipReader.File[0]
	csvReader, err := csvFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer csvReader.Close()

	// Parse CSV
	reader := csv.NewReader(csvReader)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("empty CSV file")
	}

	// Find column indices for BSE format
	header := records[0]
	symbolIdx := findColumnIndex(header, "SC_NAME")
	highIdx := findColumnIndex(header, "HIGH")
	lowIdx := findColumnIndex(header, "LOW")
	closeIdx := findColumnIndex(header, "CLOSE")
	dateIdx := findColumnIndex(header, "TRADING_DATE")

	if symbolIdx == -1 || highIdx == -1 || lowIdx == -1 || closeIdx == -1 {
		// Try alternative column names
		symbolIdx = findColumnIndex(header, "SYMBOL")
		if dateIdx == -1 {
			dateIdx = findColumnIndex(header, "DATE")
		}
	}

	if symbolIdx == -1 || highIdx == -1 || lowIdx == -1 || closeIdx == -1 {
		return nil, fmt.Errorf("required columns not found in BSE BhavCopy")
	}

	var bhavData []BhavCopyRecord

	// Process data rows (skip header)
	for i := 1; i < len(records); i++ {
		record := records[i]
		maxIdx := max(max(max(symbolIdx, highIdx), lowIdx), closeIdx)
		if len(record) <= maxIdx {
			continue
		}

		high, err := strconv.ParseFloat(record[highIdx], 64)
		if err != nil {
			continue
		}

		low, err := strconv.ParseFloat(record[lowIdx], 64)
		if err != nil {
			continue
		}

		close, err := strconv.ParseFloat(record[closeIdx], 64)
		if err != nil {
			continue
		}

		tradeDate := date.Format("2006-01-02")
		if dateIdx != -1 && dateIdx < len(record) {
			tradeDate = strings.TrimSpace(record[dateIdx])
		}

		bhavRecord := BhavCopyRecord{
			Symbol:    strings.TrimSpace(record[symbolIdx]),
			High:      high,
			Low:       low,
			Close:     close,
			TradeDate: tradeDate,
		}

		bhavData = append(bhavData, bhavRecord)
	}

	log.Printf("✅ Downloaded %d BSE records for %s", len(bhavData), date.Format("2006-01-02"))
	return bhavData, nil
}

// Calculate52WeekHighLow calculates 52-week high/low for all stocks in database
func (w52 *Week52Manager) Calculate52WeekHighLow() error {
	log.Printf("🔄 Starting 52-week high/low calculation for database stocks...")

	// Get all stocks from database first
	dbStocks, err := w52.getDatabaseStocks()
	if err != nil {
		return fmt.Errorf("failed to get database stocks: %w", err)
	}

	log.Printf("📊 Found %d stocks in database to calculate 52-week data for", len(dbStocks))

	// Get current date in IST
	now := time.Now().In(w52.istLocation)

	// Collect BhavCopy data for stocks in our database only
	nseStocksInDB := make(map[string]bool)
	bseStocksInDB := make(map[string]bool)

	for _, stock := range dbStocks {
		if stock.Exchange == "NSE" {
			nseStocksInDB[stock.Symbol] = true
		} else if stock.Exchange == "BSE" {
			bseStocksInDB[stock.Symbol] = true
		}
	}

	nseData := make(map[string][]BhavCopyRecord) // symbol -> records
	bseData := make(map[string][]BhavCopyRecord) // symbol -> records

	log.Printf("📈 NSE stocks to process: %d", len(nseStocksInDB))
	log.Printf("📈 BSE stocks to process: %d", len(bseStocksInDB))

	// Download data for last 52 weeks (364 days) but only keep data for our stocks
	successfulDays := 0
	for i := 0; i < 364; i++ {
		date := now.AddDate(0, 0, -i)

		// Skip weekends
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}

		daySuccess := false

		// Download NSE data if we have NSE stocks
		if len(nseStocksInDB) > 0 {
			nseRecords, err := w52.DownloadNSEBhavCopy(date)
			if err != nil {
				log.Printf("⚠️ Failed to download NSE data for %s: %v", date.Format("2006-01-02"), err)
			} else {
				// Only keep records for stocks in our database
				for _, record := range nseRecords {
					if nseStocksInDB[record.Symbol] {
						nseData[record.Symbol] = append(nseData[record.Symbol], record)
					}
				}
				daySuccess = true
			}
		}

		// Download BSE data if we have BSE stocks
		if len(bseStocksInDB) > 0 {
			bseRecords, err := w52.DownloadBSEBhavCopy(date)
			if err != nil {
				log.Printf("⚠️ Failed to download BSE data for %s: %v", date.Format("2006-01-02"), err)
			} else {
				// Only keep records for stocks in our database
				for _, record := range bseRecords {
					if bseStocksInDB[record.Symbol] {
						bseData[record.Symbol] = append(bseData[record.Symbol], record)
					}
				}
				daySuccess = true
			}
		}

		if daySuccess {
			successfulDays++
		}

		// Progress logging every 10 days
		if (i+1)%10 == 0 {
			log.Printf("📅 Processed %d days, successful downloads: %d", i+1, successfulDays)
		}

		// Add small delay to avoid overwhelming servers
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("✅ Completed downloading historical data for %d days", successfulDays)
	log.Printf("📊 NSE stocks with data: %d", len(nseData))
	log.Printf("📊 BSE stocks with data: %d", len(bseData))

	// Update database with calculated 52-week data
	return w52.updateDatabase(nseData, bseData)
}

// getDatabaseStocks returns all stocks from the database
func (w52 *Week52Manager) getDatabaseStocks() ([]struct {
	Symbol   string
	Exchange string
}, error) {
	rows, err := w52.db.Query("SELECT symbol, exchange FROM stock_subscriptions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stocks []struct {
		Symbol   string
		Exchange string
	}

	for rows.Next() {
		var stock struct {
			Symbol   string
			Exchange string
		}
		if err := rows.Scan(&stock.Symbol, &stock.Exchange); err != nil {
			continue
		}
		stocks = append(stocks, stock)
	}

	return stocks, nil
}

// updateDatabase updates the database with 52-week high/low data
func (w52 *Week52Manager) updateDatabase(nseData, bseData map[string][]BhavCopyRecord) error {
	log.Printf("🔄 Updating database with 52-week high/low data...")

	tx, err := w52.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	updateStmt, err := tx.Prepare(`
		UPDATE stock_subscriptions 
		SET week_52_high = ?, week_52_low = ?, week_52_high_date = ?, week_52_low_date = ?, 
		    last_close = ?, updated_at = CURRENT_TIMESTAMP
		WHERE symbol = ? AND exchange = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare update statement: %w", err)
	}
	defer updateStmt.Close()

	updatedCount := 0

	// Process NSE data
	for symbol, records := range nseData {
		if len(records) == 0 {
			continue
		}

		// Calculate 52-week high/low
		week52High, week52Low, highDate, lowDate, lastClose := calculate52WeekStats(records)

		// Update database
		result, err := updateStmt.Exec(week52High, week52Low, highDate, lowDate, lastClose, symbol, "NSE")
		if err != nil {
			log.Printf("⚠️ Failed to update NSE stock %s: %v", symbol, err)
			continue
		}

		if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
			updatedCount++
		}
	}

	// Process BSE data
	for symbol, records := range bseData {
		if len(records) == 0 {
			continue
		}

		// Calculate 52-week high/low
		week52High, week52Low, highDate, lowDate, lastClose := calculate52WeekStats(records)

		// Update database
		result, err := updateStmt.Exec(week52High, week52Low, highDate, lowDate, lastClose, symbol, "BSE")
		if err != nil {
			log.Printf("⚠️ Failed to update BSE stock %s: %v", symbol, err)
			continue
		}

		if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
			updatedCount++
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ Updated 52-week data for %d stocks", updatedCount)
	return nil
}

// UpdateDayHighLow updates day high/low for a stock (called from WebSocket)
func (w52 *Week52Manager) UpdateDayHighLow(symbol, exchange string, dayHigh, dayLow float64) (bool, error) {
	// Get current 52-week high and low
	var currentWeek52High, currentWeek52Low float64
	err := w52.db.QueryRow("SELECT week_52_high, week_52_low FROM stock_subscriptions WHERE symbol = ? AND exchange = ?",
		symbol, exchange).Scan(&currentWeek52High, &currentWeek52Low)
	if err != nil {
		return false, fmt.Errorf("failed to get current 52-week high/low: %w", err)
	}

	// Check if we have new records
	newHighRecord := dayHigh > currentWeek52High
	newLowRecord := false

	// Only check for new low if we have an existing 52-week low (not 0)
	if currentWeek52Low > 0 {
		newLowRecord = dayLow < currentWeek52Low
	} else {
		// If no 52-week low exists, set it to current day low
		newLowRecord = true
		currentWeek52Low = dayLow
	}

	// Update day high/low and potentially 52-week high/low
	var updateQuery string
	var args []interface{}
	today := time.Now().In(w52.istLocation).Format("2006-01-02")

	if newHighRecord && newLowRecord {
		// Update both 52-week high and low
		updateQuery = `
			UPDATE stock_subscriptions 
			SET day_high = ?, day_low = ?, week_52_high = ?, week_52_high_date = ?, 
			    week_52_low = ?, week_52_low_date = ?, updated_at = CURRENT_TIMESTAMP
			WHERE symbol = ? AND exchange = ?
		`
		args = []interface{}{dayHigh, dayLow, dayHigh, today, dayLow, today, symbol, exchange}
		log.Printf("🚀 NEW 52-WEEK HIGH & LOW! %s (%s): High %.2f (prev: %.2f), Low %.2f (prev: %.2f)",
			symbol, exchange, dayHigh, currentWeek52High, dayLow, currentWeek52Low)
	} else if newHighRecord {
		// Update only 52-week high
		updateQuery = `
			UPDATE stock_subscriptions 
			SET day_high = ?, day_low = ?, week_52_high = ?, week_52_high_date = ?, updated_at = CURRENT_TIMESTAMP
			WHERE symbol = ? AND exchange = ?
		`
		args = []interface{}{dayHigh, dayLow, dayHigh, today, symbol, exchange}
		log.Printf("🚀 NEW 52-WEEK HIGH! %s (%s): %.2f (previous: %.2f)", symbol, exchange, dayHigh, currentWeek52High)
	} else if newLowRecord {
		// Update only 52-week low
		updateQuery = `
			UPDATE stock_subscriptions 
			SET day_high = ?, day_low = ?, week_52_low = ?, week_52_low_date = ?, updated_at = CURRENT_TIMESTAMP
			WHERE symbol = ? AND exchange = ?
		`
		args = []interface{}{dayHigh, dayLow, dayLow, today, symbol, exchange}
		log.Printf("🔻 NEW 52-WEEK LOW! %s (%s): %.2f (previous: %.2f)", symbol, exchange, dayLow, currentWeek52Low)
	} else {
		// Update only day high/low
		updateQuery = `
			UPDATE stock_subscriptions 
			SET day_high = ?, day_low = ?, updated_at = CURRENT_TIMESTAMP
			WHERE symbol = ? AND exchange = ?
		`
		args = []interface{}{dayHigh, dayLow, symbol, exchange}
	}

	_, err = w52.db.Exec(updateQuery, args...)
	if err != nil {
		return false, fmt.Errorf("failed to update day high/low: %w", err)
	}

	return newHighRecord || newLowRecord, nil
}

// GetWeek52Data returns 52-week data for a stock
func (w52 *Week52Manager) GetWeek52Data(symbol, exchange string) (*Week52Data, error) {
	var data Week52Data
	var updatedAt string

	query := `
		SELECT symbol, token, exchange, week_52_high, week_52_low, week_52_high_date, 
		       week_52_low_date, last_close, updated_at
		FROM stock_subscriptions 
		WHERE symbol = ? AND exchange = ?
	`

	err := w52.db.QueryRow(query, symbol, exchange).Scan(
		&data.Symbol, &data.Token, &data.Exchange, &data.Week52High, &data.Week52Low,
		&data.Week52HighDate, &data.Week52LowDate, &data.LastClose, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get 52-week data: %w", err)
	}

	// Parse updated_at timestamp
	if parsedTime, err := time.Parse("2006-01-02 15:04:05", updatedAt); err == nil {
		data.UpdatedAt = parsedTime
	}

	return &data, nil
}

// Helper functions

func findColumnIndex(header []string, columnName string) int {
	for i, col := range header {
		if strings.EqualFold(strings.TrimSpace(col), columnName) {
			return i
		}
	}
	return -1
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func calculate52WeekStats(records []BhavCopyRecord) (high, low float64, highDate, lowDate string, lastClose float64) {
	if len(records) == 0 {
		return 0, 0, "", "", 0
	}

	high = records[0].High
	low = records[0].Low
	highDate = records[0].TradeDate
	lowDate = records[0].TradeDate
	lastClose = records[0].Close

	// Find latest date for last close
	latestDate := records[0].TradeDate

	for _, record := range records {
		if record.High > high {
			high = record.High
			highDate = record.TradeDate
		}
		if record.Low < low {
			low = record.Low
			lowDate = record.TradeDate
		}

		// Update last close with most recent data
		if record.TradeDate >= latestDate {
			latestDate = record.TradeDate
			lastClose = record.Close
		}
	}

	return high, low, highDate, lowDate, lastClose
}
