# 📚 Odin Streamer - Complete Code Explanation

This document explains every code file, function, and component step by step.

---

## 🗂️ File Structure Overview

```
odin-streamer/
├── internal/candle/engine.go      # ⚡ Core candle generation engine
├── internal/tick/normalizer.go    # 🔄 Tick data processing
├── internal/historical/client.go  # 📈 Historical data fetching
├── internal/api/intraday.go      # 🌐 REST API endpoints
├── internal/storage/redis.go     # 📦 Redis caching
├── internal/storage/timescaledb.go # 🗄️ Database persistence
├── go.mod                        # 📋 Dependencies
├── .env                         # ⚙️ Configuration
└── README.md                    # 📖 Documentation
```

---

# 1️⃣ **internal/candle/engine.go** - Core Candle Generation Engine

## 🎯 **What This File Does**
This is the heart of the candlestick system. It converts raw market ticks into 1-minute OHLCV candles and creates synthetic candles for missing data.

## 📊 **Data Structures**

### `Candle` Struct
```go
type Candle struct {
    Exchange   string    `json:"exchange"`     // NSE, BSE
    ScripToken string    `json:"scrip_token"`  // Stock token (e.g., "2475")
    MinuteTS   time.Time `json:"minute_ts"`    // Minute start time in IST
    Open       float64   `json:"open"`         // Opening price
    High       float64   `json:"high"`         // Highest price
    Low        float64   `json:"low"`          // Lowest price
    Close      float64   `json:"close"`        // Closing price
    Volume     int64     `json:"volume"`       // Total volume
    Source     string    `json:"source"`       // "realtime", "synthetic", "historical_api"
}
```
**Purpose**: Represents a single minute candle with all OHLCV data

### `Tick` Struct
```go
type Tick struct {
    Exchange   string    `json:"exchange"`     // NSE, BSE
    ScripToken string    `json:"scrip_token"`  // Stock token
    Symbol     string    `json:"symbol"`       // Stock symbol (RELIANCE)
    Price      float64   `json:"price"`        // Current price
    Volume     int64     `json:"volume"`       // Volume for this tick
    Timestamp  time.Time `json:"timestamp"`    // When tick occurred
}
```
**Purpose**: Represents incoming market data tick

### `CandleEngine` Struct
```go
type CandleEngine struct {
    istLocation   *time.Location                    // IST timezone
    tokenStates   map[string]*TokenCandleState      // Per-token state
    mutex         sync.RWMutex                      // Thread safety
    marketOpen    time.Duration                     // 09:15 IST
    marketClose   time.Duration                     // 15:30 IST
    updateChannel chan CandleUpdate                 // Live updates
    closeChannel  chan CandleUpdate                 // Finalized candles
    patchChannel  chan CandleUpdate                 // Late corrections
    lateTolerance time.Duration                     // 2 minutes
    persistCallback func(candle Candle) error       // Save to database
    publishCallback func(update CandleUpdate) error // Send to clients
}
```
**Purpose**: Main engine that manages all candle generation

## 🔧 **Key Functions Explained**

### `NewCandleEngine()` - Initialize Engine
```go
func NewCandleEngine() (*CandleEngine, error)
```
**What it does**:
1. Loads IST timezone (Asia/Kolkata)
2. Sets market hours (09:15-15:30)
3. Creates channels for updates
4. Starts minute boundary timer
5. Returns ready-to-use engine

**Step by step**:
- Load timezone → Set market hours → Create channels → Start timer → Return engine

### `ProcessTick()` - Convert Tick to Candle
```go
func (ce *CandleEngine) ProcessTick(tick Tick) error
```
**What it does**:
1. Converts tick timestamp to IST
2. Checks if tick is within market hours
3. Gets or creates state for this token
4. Handles minute boundary crossing
5. Updates current candle with tick data
6. Publishes live update

**Step by step**:
- Convert to IST → Check market hours → Get token state → Handle minute boundary → Update candle → Publish update

### `startNewMinute()` - Begin New Candle
```go
func (ce *CandleEngine) startNewMinute(state *TokenCandleState, minuteStart time.Time, openPrice float64)
```
**What it does**:
1. Sets current minute timestamp
2. Creates new candle with opening price
3. Initializes OHLC values to opening price
4. Sets volume to 0
5. Marks source as "realtime"

### `updateCurrentCandle()` - Update Candle with Tick
```go
func (ce *CandleEngine) updateCurrentCandle(state *TokenCandleState, tick Tick)
```
**What it does**:
1. Updates high if tick price > current high
2. Updates low if tick price < current low
3. Sets close to tick price
4. Adds tick volume to total volume
5. Updates last known price

### `finalizeCandle()` - Complete Minute Candle
```go
func (ce *CandleEngine) finalizeCandle(state *TokenCandleState)
```
**What it does**:
1. Creates final candle copy
2. Logs candle completion
3. Calls persist callback (saves to database)
4. Publishes close event
5. Clears current candle state

### `fillGapMinutes()` - Create Synthetic Candles
```go
func (ce *CandleEngine) fillGapMinutes(state *TokenCandleState, fromMinute, toMinute time.Time)
```
**What it does**:
1. Checks if we have last known price
2. Iterates through missing minutes
3. Skips non-market hours
4. Creates synthetic candles with:
   - Open = last known price
   - High = last known price
   - Low = last known price
   - Close = last known price
   - Volume = 0
   - Source = "synthetic"
5. Persists and publishes each synthetic candle

### `minuteBoundaryTimer()` - Handle Minute Changes
```go
func (ce *CandleEngine) minuteBoundaryTimer()
```
**What it does**:
1. Runs every minute
2. Checks if in market hours
3. For each token without current candle:
   - Creates synthetic candle if has last price
   - Uses previous minute timestamp
   - Persists and publishes synthetic candle

---

# 2️⃣ **internal/tick/normalizer.go** - Tick Data Processing

## 🎯 **What This File Does**
Processes raw market data, validates it, removes duplicates, and converts it to standardized tick format.

## 📊 **Data Structures**

### `TickNormalizer` Struct
```go
type TickNormalizer struct {
    istLocation     *time.Location                    // IST timezone
    lastTicks       map[string]*NormalizedTick        // For deduplication
    totalTicks      int64                             // Statistics
    normalizedTicks int64                             // Successfully processed
    duplicateTicks  int64                             // Duplicates found
    errorTicks      int64                             // Errors encountered
}
```

### `MarketData` Struct (Input)
```go
type MarketData struct {
    Symbol        string  `json:"symbol"`         // Stock symbol
    Token         string  `json:"token"`          // Stock token
    LTP           float64 `json:"ltp"`           // Last traded price
    High          float64 `json:"high"`          // Day high
    Low           float64 `json:"low"`           // Day low
    Open          float64 `json:"open"`          // Day open
    Close         float64 `json:"close"`         // Previous close
    Volume        int64   `json:"volume"`        // Volume
    Timestamp     int64   `json:"timestamp"`     // Unix timestamp
}
```

## 🔧 **Key Functions Explained**

### `NewTickNormalizer()` - Initialize Normalizer
```go
func NewTickNormalizer() (*TickNormalizer, error)
```
**What it does**:
1. Loads IST timezone
2. Creates deduplication map
3. Initializes statistics counters
4. Returns ready normalizer

### `NormalizeMarketData()` - Process Raw Data
```go
func (tn *TickNormalizer) NormalizeMarketData(data MarketData) (*candle.Tick, error)
```
**What it does**:
1. **Validation**:
   - Checks if token exists
   - Checks if symbol exists
   - Validates LTP > 0
2. **Timestamp Conversion**:
   - Converts Unix timestamp to IST time
   - Uses current time if timestamp missing
3. **Exchange Detection**:
   - Determines NSE vs BSE based on token length
4. **Deduplication**:
   - Creates hash from token + price + volume + minute
   - Checks against last processed tick
   - Returns nil if duplicate
5. **Conversion**:
   - Converts to candle.Tick format
   - Returns normalized tick

**Step by step**:
- Validate data → Convert timestamp → Detect exchange → Check duplicates → Convert format → Return tick

### `generateTickHash()` - Create Deduplication Hash
```go
func (tn *TickNormalizer) generateTickHash(token string, price float64, volume int64, timestamp time.Time) string
```
**What it does**:
1. Truncates timestamp to minute
2. Creates hash: "token_price_volume_minute"
3. Returns unique identifier for deduplication

---

# 3️⃣ **internal/historical/client.go** - Historical Data Fetching

## 🎯 **What This File Does**
Fetches historical data from IndiraTrade API and applies fill algorithm to create continuous minute series.

## 📊 **Data Structures**

### `IndiraTradeClient` Struct
```go
type IndiraTradeClient struct {
    baseURL     string        // API base URL
    httpClient  *http.Client  // HTTP client with timeout
    istLocation *time.Location // IST timezone
}
```

### `FillAlgorithmResult` Struct
```go
type FillAlgorithmResult struct {
    Candles          []candle.Candle // Final continuous series
    TotalMinutes     int             // Total minutes generated
    ProvidedMinutes  int             // Minutes from API
    SyntheticMinutes int             // Synthetic minutes created
    FirstMinute      time.Time       // Series start time
    LastMinute       time.Time       // Series end time
    PreviousDayClose float64         // Previous day closing price
    HasGaps          bool            // Whether gaps were found
    GapPeriods       []GapPeriod     // Details of gap periods
}
```

## 🔧 **Key Functions Explained**

### `NewIndiraTradeClient()` - Initialize Client
```go
func NewIndiraTradeClient(baseURL string) (*IndiraTradeClient, error)
```
**What it does**:
1. Loads IST timezone
2. Creates HTTP client with 30-second timeout
3. Sets base URL for API calls
4. Returns configured client

### `FetchHistoricalData()` - Get Data from API
```go
func (itc *IndiraTradeClient) FetchHistoricalData(exchange, scripToken, symbol string, fromTime, toTime time.Time) ([]HistoricalDataPoint, error)
```
**What it does**:
1. **Build API URL**:
   - Format: `/v1/chart/data/{exchange}/{token}/1/MIN/{symbol}`
   - Example: `/v1/chart/data/NSE/2475/1/MIN/RELIANCE`
2. **Add Parameters**:
   - from: Start time in IST
   - to: End time in IST
   - source: "LIVE"
3. **Make HTTP Request**:
   - GET request to full URL
   - Parse JSON response
   - Validate status = "success"
4. **Return Data**:
   - Array of historical data points

### `FetchPreviousDayClose()` - Get Previous Close
```go
func (itc *IndiraTradeClient) FetchPreviousDayClose(exchange, scripToken, symbol string, date time.Time) (float64, error)
```
**What it does**:
1. **Calculate Previous Day**:
   - Go back 1 day
   - Skip weekends (Saturday/Sunday)
2. **Fetch Last 5 Minutes**:
   - From 15:25 to 15:30 of previous day
   - Get closing data
3. **Return Close Price**:
   - Last available close price
   - Used for synthetic candles

### `ApplyFillAlgorithm()` - Create Continuous Series
```go
func (itc *IndiraTradeClient) ApplyFillAlgorithm(exchange, scripToken, symbol string, fromTime, toTime time.Time, providedData []HistoricalDataPoint) (*FillAlgorithmResult, error)
```
**What it does**:
1. **Convert to Map**:
   - Create map[time.Time]HistoricalDataPoint
   - Key = minute start time
   - Value = data point
2. **Get Fill Price**:
   - Use first data point's open price
   - Or fetch previous day close
   - Or use default 100.0
3. **Generate Series**:
   - Iterate minute by minute from start to end
   - Skip non-market hours (before 09:15, after 15:30)
   - For each minute:
     - If data exists: create real candle
     - If no data: create synthetic candle
4. **Track Gaps**:
   - Record gap periods
   - Count synthetic vs real candles
5. **Return Result**:
   - Complete continuous series
   - Statistics and metadata

**Step by step**:
- Convert data to map → Get fill price → Iterate minutes → Create candles → Track gaps → Return result

### `FetchAndFillIntradayData()` - Get Full Day Data
```go
func (itc *IndiraTradeClient) FetchAndFillIntradayData(exchange, scripToken, symbol string, date time.Time) (*FillAlgorithmResult, error)
```
**What it does**:
1. Creates market hours (09:15-15:30) for date
2. Fetches historical data for full day
3. Applies fill algorithm
4. Returns continuous series

---

# 4️⃣ **internal/api/intraday.go** - REST API Endpoints

## 🎯 **What This File Does**
Provides REST API endpoints for accessing intraday candlestick data with caching and metadata.

## 📊 **Data Structures**

### `IntradayAPI` Struct
```go
type IntradayAPI struct {
    redisAdapter     *storage.RedisAdapter        // Redis cache
    timescaleAdapter *storage.TimescaleDBAdapter  // Database
    historicalClient *historical.IndiraTradeClient // Historical data
    istLocation      *time.Location               // IST timezone
}
```

### `IntradayResponse` Struct
```go
type IntradayResponse struct {
    Success      bool            `json:"success"`       // Request success
    Exchange     string          `json:"exchange"`      // NSE, BSE
    ScripToken   string          `json:"scrip_token"`   // Token
    Symbol       string          `json:"symbol"`        // Symbol
    Date         string          `json:"date"`          // YYYY-MM-DD
    MarketOpen   string          `json:"market_open"`   // 09:15 IST
    MarketClose  string          `json:"market_close"`  // 15:30 IST
    TotalCandles int             `json:"total_candles"` // Number of candles
    DataSource   string          `json:"data_source"`   // Cache/API
    Candles      []candle.Candle `json:"candles"`       // Candle data
    Metadata     Metadata        `json:"metadata"`      // Additional info
    Timestamp    int64           `json:"timestamp"`     // Response time
}
```

## 🔧 **Key Functions Explained**

### `NewIntradayAPI()` - Initialize API
```go
func NewIntradayAPI(redisAdapter, timescaleAdapter, historicalClient) (*IntradayAPI, error)
```
**What it does**:
1. Loads IST timezone
2. Stores adapter references
3. Returns configured API handler

### `HandleIntradayRequest()` - Main API Handler
```go
func (api *IntradayAPI) HandleIntradayRequest(w http.ResponseWriter, r *http.Request)
```
**What it does**:
1. **Parse URL**:
   - Extract exchange and scrip_token from path
   - Example: `/intraday/NSE/2475`
2. **Parse Parameters**:
   - date: Target date (default: today)
   - force_refresh: Skip cache (default: false)
   - include_metadata: Add metadata (default: true)
3. **Get Symbol**:
   - Lookup symbol from token
   - Use token as fallback
4. **Try Cache First**:
   - Check Redis cache if not force_refresh
   - Return cached data if available
5. **Fetch from API**:
   - Call historical client if cache miss
   - Apply fill algorithm
   - Store in cache for future requests
6. **Return Response**:
   - JSON response with candles and metadata

**Step by step**:
- Parse URL → Parse parameters → Try cache → Fetch from API → Store in cache → Return response

### `getFromCache()` - Retrieve from Redis
```go
func (api *IntradayAPI) getFromCache(exchange, scripToken, symbol string, date time.Time) (*IntradayResponse, error)
```
**What it does**:
1. Calls Redis adapter to get day candles
2. Returns nil if cache miss
3. Converts cache format to API response format
4. Calculates metadata statistics
5. Returns response with cache_hit = true

### `fetchFromHistoricalAPI()` - Get from Historical API
```go
func (api *IntradayAPI) fetchFromHistoricalAPI(exchange, scripToken, symbol string, date time.Time) (*IntradayResponse, error)
```
**What it does**:
1. Calls historical client to fetch and fill data
2. Converts result to API response format
3. Sets cache_hit = false
4. Includes gap and synthetic candle statistics
5. Returns response

---

# 5️⃣ **internal/storage/redis.go** - Redis Caching Layer

## 🎯 **What This File Does**
Handles Redis caching for candle data and pub/sub for live updates.

## 📊 **Data Structures**

### `RedisAdapter` Struct
```go
type RedisAdapter struct {
    client      *redis.Client     // Redis client
    ctx         context.Context   // Context for operations
    istLocation *time.Location    // IST timezone
}
```

### `CandleCache` Struct
```go
type CandleCache struct {
    Exchange     string          `json:"exchange"`      // NSE, BSE
    ScripToken   string          `json:"scrip_token"`   // Token
    Date         string          `json:"date"`          // YYYYMMDD
    Candles      []candle.Candle `json:"candles"`       // Full day candles
    LastUpdated  time.Time       `json:"last_updated"`  // Cache time
    TotalCandles int             `json:"total_candles"` // Count
    MarketOpen   time.Time       `json:"market_open"`   // 09:15 IST
    MarketClose  time.Time       `json:"market_close"`  // 15:30 IST
}
```

## 🔧 **Key Functions Explained**

### `NewRedisAdapter()` - Initialize Redis
```go
func NewRedisAdapter(redisURL string) (*RedisAdapter, error)
```
**What it does**:
1. **Parse URL**: Redis connection string or default localhost:6379
2. **Create Client**: Redis client with connection options
3. **Load Timezone**: IST timezone for timestamps
4. **Test Connection**: Ping Redis to verify connectivity
5. **Return Adapter**: Ready-to-use Redis adapter

### `StoreDayCandles()` - Cache Full Day Data
```go
func (ra *RedisAdapter) StoreDayCandles(exchange, scripToken string, date time.Time, candles []candle.Candle) error
```
**What it does**:
1. **Create Key**: `candles:NSE:2475:20250828`
2. **Build Cache Object**:
   - Exchange, token, date
   - All candles for the day
   - Market open/close times
   - Total count and timestamp
3. **Serialize**: Convert to JSON
4. **Store**: Save in Redis with 7-day expiration
5. **Log**: Record successful storage

### `GetDayCandles()` - Retrieve Cached Data
```go
func (ra *RedisAdapter) GetDayCandles(exchange, scripToken string, date time.Time) (*CandleCache, error)
```
**What it does**:
1. **Build Key**: Same format as storage
2. **Get from Redis**: Retrieve JSON data
3. **Handle Miss**: Return nil if key doesn't exist
4. **Deserialize**: Convert JSON back to struct
5. **Return Cache**: Cached candle data

### `PublishCandleUpdate()` - Send Live Updates
```go
func (ra *RedisAdapter) PublishCandleUpdate(update candle.CandleUpdate) error
```
**What it does**:
1. **Create Channel**: `intraday_updates:NSE:2475`
2. **Serialize Update**: Convert to JSON
3. **Publish**: Send to Redis pub/sub channel
4. **Return**: Success/error status

---

# 6️⃣ **internal/storage/timescaledb.go** - Database Persistence

## 🎯 **What This File Does**
Handles TimescaleDB persistence for long-term candle storage with hypertables.

## 📊 **Data Structures**

### `TimescaleDBAdapter` Struct
```go
type TimescaleDBAdapter struct {
    db          *sql.DB           // Database connection
    istLocation *time.Location    // IST timezone
}
```

## 🔧 **Key Functions Explained**

### `NewTimescaleDBAdapter()` - Initialize Database
```go
func NewTimescaleDBAdapter(connectionString string) (*TimescaleDBAdapter, error)
```
**What it does**:
1. **Open Connection**: Connect to PostgreSQL/TimescaleDB
2. **Test Connection**: Ping database
3. **Load Timezone**: IST timezone
4. **Run Migrations**: Create tables and hypertables
5. **Return Adapter**: Ready database adapter

### `runMigrations()` - Create Database Schema
```go
func (tdb *TimescaleDBAdapter) runMigrations() error
```
**What it does**:
1. **Create Candles Table**:
   ```sql
   CREATE TABLE candles (
       exchange VARCHAR(10),
       scrip_token VARCHAR(20),
       minute_ts TIMESTAMPTZ,
       open_price DECIMAL(12,4),
       high_price DECIMAL(12,4),
       low_price DECIMAL(12,4),
       close_price DECIMAL(12,4),
       volume BIGINT,
       source VARCHAR(20),
       created_at TIMESTAMPTZ,
       updated_at TIMESTAMPTZ
   );
   ```
2. **Create Hypertable**: Partition by minute_ts with 1-day chunks
3. **Create Indexes**: For efficient queries
4. **Create Metadata Table**: Track candle statistics

### `StoreCandle()` - Save Single Candle
```go
func (tdb *TimescaleDBAdapter) StoreCandle(candleData candle.Candle) error
```
**What it does**:
1. **Insert/Update**: UPSERT candle data
2. **Handle Conflicts**: Update if candle already exists
3. **Update Metadata**: Track statistics
4. **Return Status**: Success/error

### `GetCandles()` - Retrieve Candles
```go
func (tdb *TimescaleDBAdapter) GetCandles(exchange, scripToken string, fromTime, toTime time.Time) ([]candle.Candle, error)
```
**What it does**:
1. **Query Database**: SELECT with time range filter
2. **Scan Results**: Convert rows to candle structs
3. **Convert Timezone**: Ensure IST timestamps
4. **Return Candles**: Array of candle data

---

# 🔗 **How All Files Work Together**

## 📊 **Data Flow Diagram**

```
1. Raw Market Data (MarketData)
   ↓
2. Tick Normalizer (normalizer.go)
   ↓ (validates, deduplicates)
3. Candle Engine (engine.go)
   ↓ (aggregates to candles)
4. Storage Layer
   ├── Redis (redis.go) - Fast cache
   └── TimescaleDB (timescaledb.go) - Persistent storage
   ↓
5. API Layer (intraday.go)
   ↓ (serves HTTP requests)
6. Client Applications
```

## 🔄 **Complete Process Flow**

### **Real-time Processing**:
1. **Raw tick arrives** → `MarketData` struct
2. **Tick Normalizer** validates and converts to `Tick`
3. **Candle Engine** processes tick:
   - Updates current minute candle
   - Creates synthetic candles for gaps
   - Finalizes completed candles
4. **Storage Layer** saves candles:
   - Redis for fast access
   - TimescaleDB for persistence
5. **Pub/Sub** notifies connected clients

### **API Request Processing**:
1. **Client requests** `/intraday/NSE/2475`
2. **API Handler** parses request
3. **Cache Check** tries Redis first
4. **Historical Fetch** if cache miss:
   - Calls IndiraTrade API
   - Applies fill algorithm
   - Creates continuous series
5. **Response** returns JSON with candles

### **WebSocket Integration** (Ready for implementation):
1. **Client connects** with stock list
2. **Init Data** sends full day candles
3. **Live Updates** stream real-time changes
4. **Redis Pub/Sub** distributes updates

---

## 🎯 **Key Design Patterns Used**

1. **Single Responsibility**: Each file has one clear purpose
2. **Dependency Injection**: Components receive dependencies
3. **Interface Segregation**: Clean adapter interfaces
4. **Error Handling**: Comprehensive error checking
5. **Concurrency Safety**: Mutexes for thread safety
6. **Caching Strategy**: Redis for speed, DB for persistence
7. **Timezone Consistency**: IST throughout the system

This architecture ensures high performance, reliability, and maintainability for Indian stock market data processing.
