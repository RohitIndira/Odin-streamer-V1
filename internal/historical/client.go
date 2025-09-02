package historical

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang-market-service/internal/candle"
)

// IndiraTradeClient handles communication with IndiraTrade historical API
type IndiraTradeClient struct {
	baseURL     string
	httpClient  *http.Client
	istLocation *time.Location
}

// HistoricalResponse represents the API response structure
type HistoricalResponse struct {
	Status  string                `json:"status"`
	Message string                `json:"message"`
	Data    []HistoricalDataPoint `json:"data"`
}

// HistoricalDataPoint represents a single data point from the API
type HistoricalDataPoint struct {
	Symbol    string  `json:"symbol"`
	Open      float64 `json:"O"`
	High      float64 `json:"H"`
	Low       float64 `json:"L"`
	Close     float64 `json:"C"`
	Volume    int64   `json:"V"`
	DateTime  string  `json:"DT"` // "2025-08-25 09:15:00"
	Timestamp int64   `json:"timestamp"`
}

// FillAlgorithmResult represents the result of the fill algorithm
type FillAlgorithmResult struct {
	Candles          []candle.Candle `json:"candles"`
	TotalMinutes     int             `json:"total_minutes"`
	ProvidedMinutes  int             `json:"provided_minutes"`
	SyntheticMinutes int             `json:"synthetic_minutes"`
	FirstMinute      time.Time       `json:"first_minute"`
	LastMinute       time.Time       `json:"last_minute"`
	PreviousDayClose float64         `json:"previous_day_close"`
	HasGaps          bool            `json:"has_gaps"`
	GapPeriods       []GapPeriod     `json:"gap_periods"`
}

// GapPeriod represents a period where synthetic candles were created
type GapPeriod struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Minutes   int       `json:"minutes"`
	FillPrice float64   `json:"fill_price"`
}

// NewIndiraTradeClient creates a new IndiraTrade API client
func NewIndiraTradeClient(baseURL string) (*IndiraTradeClient, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	client := &IndiraTradeClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		istLocation: istLocation,
	}

	log.Printf("✅ IndiraTrade client initialized with base URL: %s", baseURL)
	return client, nil
}

// FetchHistoricalData fetches historical minute data from IndiraTrade API
func (itc *IndiraTradeClient) FetchHistoricalData(exchange, scripToken, symbol string, fromTime, toTime time.Time) ([]HistoricalDataPoint, error) {
	// Build API URL using official format: {exchange}/{scrip_token}/{interval}/{duration}/{symbol}
	// Format: /v1/chart/data/{exchange}/{scrip_token}/{interval}/{duration}/{symbol}
	apiPath := fmt.Sprintf("/v1/chart/data/%s/%s/1/MIN/%s", exchange, scripToken, symbol)
	apiURL := itc.baseURL + apiPath

	// Add query parameters as per official documentation
	params := url.Values{}
	params.Add("from", fromTime.In(itc.istLocation).Format("2006-01-02 15:04:05"))
	params.Add("to", toTime.In(itc.istLocation).Format("2006-01-02 15:04:05"))
	params.Add("countBack", "500") // Number of candles to retrieve

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	log.Printf("🔄 Fetching real market data: %s", fullURL)
	log.Printf("🔍 Debug - Exchange: %s, Token: %s, Symbol: %s", exchange, scripToken, symbol)
	log.Printf("🔍 Debug - From: %s, To: %s", fromTime.In(itc.istLocation).Format("2006-01-02 15:04:05"), toTime.In(itc.istLocation).Format("2006-01-02 15:04:05"))

	// Create HTTP request with authentication headers
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication headers from environment variables
	apiKey := os.Getenv("BROKER_API_KEY")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// Add other required headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OdinStreamer/1.0")

	// Make HTTP request
	resp, err := itc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	// Parse response
	var apiResponse HistoricalResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	if apiResponse.Status != "success" {
		return nil, fmt.Errorf("API returned error: %s", apiResponse.Message)
	}

	log.Printf("✅ Fetched %d real market data points for %s", len(apiResponse.Data), symbol)
	return apiResponse.Data, nil
}

// FetchPreviousDayClose fetches the previous day's closing price
func (itc *IndiraTradeClient) FetchPreviousDayClose(exchange, scripToken, symbol string, date time.Time) (float64, error) {
	// Get previous trading day (skip weekends)
	prevDay := date.AddDate(0, 0, -1)
	for prevDay.Weekday() == time.Saturday || prevDay.Weekday() == time.Sunday {
		prevDay = prevDay.AddDate(0, 0, -1)
	}

	// Fetch data for previous day's market close (15:30 IST)
	marketClose := time.Date(prevDay.Year(), prevDay.Month(), prevDay.Day(), 15, 30, 0, 0, itc.istLocation)
	fromTime := marketClose.Add(-5 * time.Minute) // Get last 5 minutes of previous day

	data, err := itc.FetchHistoricalData(exchange, scripToken, symbol, fromTime, marketClose)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch previous day close: %w", err)
	}

	if len(data) == 0 {
		return 0, fmt.Errorf("no data available for previous day close")
	}

	// Return the close price of the last available candle
	lastCandle := data[len(data)-1]
	log.Printf("📊 Previous day close for %s: %.2f", symbol, lastCandle.Close)
	return lastCandle.Close, nil
}

// ApplyFillAlgorithm converts raw historical data to candle format (NO SYNTHETIC DATA)
func (itc *IndiraTradeClient) ApplyFillAlgorithm(exchange, scripToken, symbol string, fromTime, toTime time.Time, providedData []HistoricalDataPoint) (*FillAlgorithmResult, error) {
	log.Printf("🔄 Processing real market data for %s from %s to %s", symbol,
		fromTime.In(itc.istLocation).Format("15:04"), toTime.In(itc.istLocation).Format("15:04"))

	// Convert only real data points to candles - NO SYNTHETIC DATA
	var resultCandles []candle.Candle

	for _, point := range providedData {
		// Parse datetime and convert to IST
		dt, err := time.ParseInLocation("2006-01-02 15:04:05", point.DateTime, itc.istLocation)
		if err != nil {
			log.Printf("⚠️ Failed to parse datetime %s: %v", point.DateTime, err)
			continue
		}

		// Convert to candle format
		candleData := candle.Candle{
			Exchange:   exchange,
			ScripToken: scripToken,
			MinuteTS:   dt.Truncate(time.Minute),
			Open:       point.Open,
			High:       point.High,
			Low:        point.Low,
			Close:      point.Close,
			Volume:     point.Volume,
			Source:     "historical_api",
		}

		resultCandles = append(resultCandles, candleData)
	}

	// Calculate statistics - only real data
	totalMinutes := len(resultCandles)
	providedMinutes := len(providedData)

	var firstMinute, lastMinute time.Time
	if len(resultCandles) > 0 {
		firstMinute = resultCandles[0].MinuteTS
		lastMinute = resultCandles[len(resultCandles)-1].MinuteTS
	}

	result := &FillAlgorithmResult{
		Candles:          resultCandles,
		TotalMinutes:     totalMinutes,
		ProvidedMinutes:  providedMinutes,
		SyntheticMinutes: 0, // No synthetic data
		FirstMinute:      firstMinute,
		LastMinute:       lastMinute,
		PreviousDayClose: 0,             // Not needed without synthetic data
		HasGaps:          false,         // No gap filling
		GapPeriods:       []GapPeriod{}, // No gaps filled
	}

	log.Printf("✅ Processed %d real market data points for %s", totalMinutes, symbol)
	return result, nil
}

// FetchAndFillIntradayData fetches historical data from market open to current time (or market close)
func (itc *IndiraTradeClient) FetchAndFillIntradayData(exchange, scripToken, symbol string, date time.Time) (*FillAlgorithmResult, error) {
	// Create market open time for the date
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, itc.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, itc.istLocation)

	// Determine end time: current time or market close (whichever is earlier)
	now := time.Now().In(itc.istLocation)
	endTime := marketClose

	// If requesting current date, use current time (but not beyond market close)
	if date.Format("2006-01-02") == now.Format("2006-01-02") {
		currentMinute := now.Truncate(time.Minute)
		if currentMinute.Before(marketClose) {
			endTime = currentMinute
		}
		log.Printf("📅 Current date request: fetching from %s to %s (live data)",
			marketOpen.Format("15:04"), endTime.Format("15:04"))
	} else {
		log.Printf("📅 Historical date request: fetching full day from %s to %s",
			marketOpen.Format("15:04"), endTime.Format("15:04"))
	}

	// Fetch historical data from API
	historicalData, err := itc.FetchHistoricalData(exchange, scripToken, symbol, marketOpen, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	// Apply fill algorithm
	return itc.ApplyFillAlgorithm(exchange, scripToken, symbol, marketOpen, endTime, historicalData)
}

// FetchFromTimeToNow fetches and fills data from a specific time to now
func (itc *IndiraTradeClient) FetchFromTimeToNow(exchange, scripToken, symbol string, fromTime time.Time) (*FillAlgorithmResult, error) {
	now := time.Now().In(itc.istLocation)

	// Adjust end time to current minute or market close, whichever is earlier
	marketClose := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, itc.istLocation)
	endTime := now.Truncate(time.Minute)
	if endTime.After(marketClose) {
		endTime = marketClose
	}

	// Fetch historical data
	historicalData, err := itc.FetchHistoricalData(exchange, scripToken, symbol, fromTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %w", err)
	}

	// Apply fill algorithm
	return itc.ApplyFillAlgorithm(exchange, scripToken, symbol, fromTime, endTime, historicalData)
}

// isMarketHours checks if the given time is within market hours (09:15-15:30 IST)
func (itc *IndiraTradeClient) isMarketHours(t time.Time) bool {
	istTime := t.In(itc.istLocation)

	// Market hours: 09:15 - 15:30 IST
	marketOpen := 9*time.Hour + 15*time.Minute
	marketClose := 15*time.Hour + 30*time.Minute

	timeOfDay := time.Duration(istTime.Hour())*time.Hour +
		time.Duration(istTime.Minute())*time.Minute +
		time.Duration(istTime.Second())*time.Second

	return timeOfDay >= marketOpen && timeOfDay <= marketClose
}

// GetMarketHours returns market open and close times for a given date
func (itc *IndiraTradeClient) GetMarketHours(date time.Time) (time.Time, time.Time) {
	marketOpen := time.Date(date.Year(), date.Month(), date.Day(), 9, 15, 0, 0, itc.istLocation)
	marketClose := time.Date(date.Year(), date.Month(), date.Day(), 15, 30, 0, 0, itc.istLocation)
	return marketOpen, marketClose
}

// ValidateAPIResponse validates the API response structure
func (itc *IndiraTradeClient) ValidateAPIResponse(response HistoricalResponse) error {
	if response.Status != "success" {
		return fmt.Errorf("API error: %s", response.Message)
	}

	if len(response.Data) == 0 {
		return fmt.Errorf("no data returned from API")
	}

	// Validate data points
	for i, point := range response.Data {
		if point.DateTime == "" {
			return fmt.Errorf("missing datetime in data point %d", i)
		}

		if point.Open <= 0 || point.High <= 0 || point.Low <= 0 || point.Close <= 0 {
			return fmt.Errorf("invalid OHLC values in data point %d", i)
		}

		if point.High < point.Low {
			return fmt.Errorf("high < low in data point %d", i)
		}
	}

	return nil
}

// GetStats returns client statistics
func (itc *IndiraTradeClient) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"base_url":     itc.baseURL,
		"timeout":      itc.httpClient.Timeout.String(),
		"timezone":     itc.istLocation.String(),
		"market_open":  "09:15 IST",
		"market_close": "15:30 IST",
	}
}

// SetTimeout sets the HTTP client timeout
func (itc *IndiraTradeClient) SetTimeout(timeout time.Duration) {
	itc.httpClient.Timeout = timeout
	log.Printf("🔧 IndiraTrade client timeout set to %v", timeout)
}
