# 📊 Database Storage Breakdown: What's Stored Where

## 🎯 Key Point: We DON'T Store Raw Ticks - Only Processed Candles

The system processes raw tick data in real-time and stores only the processed 1-minute OHLCV candlestick data. Here's exactly what each database stores:

---

## 1. **Redis** - High-Speed Cache & Real-time Data

### What Redis Stores:
- ✅ **Processed 1-minute Candlestick Data** (NOT raw ticks)
- ✅ **Daily Candle Cache** - Full day's processed candles (375 candles: 09:15-15:30)
- ✅ **Latest In-flight Candles** - Current minute's candle being built from ticks
- ✅ **Pub/Sub Messages** - Real-time candle updates for WebSocket broadcasting

### Redis Data Structures:
```redis
# Daily candle cache (375 candles per trading day)
candles:NSE_EQ:2475:20250829 → {
  "exchange": "NSE_EQ",
  "scrip_token": "2475", 
  "date": "20250829",
  "candles": [
    {
      "minute_ts": "2025-08-29T09:15:00+05:30",
      "open": 230.00,
      "high": 235.00,
      "low": 229.50,
      "close": 234.25,
      "volume": 25000,
      "source": "live_tick"
    },
    // ... 374 more 1-minute candles
  ],
  "total_candles": 375,
  "market_open": "09:15",
  "market_close": "15:30"
}

# Latest live candle (current minute being built)
intraday_latest:NSE_EQ:2475 → {
  "exchange": "NSE_EQ",
  "scrip_token": "2475",
  "candle": {
    "minute_ts": "2025-08-29T12:30:00+05:30",
    "open": 232.50,
    "high": 233.00,
    "low": 232.00,
    "close": 232.75,  // Updates with each tick
    "volume": 15000,  // Accumulates with each tick
    "source": "live_tick"
  },
  "is_complete": false,  // true when minute ends
  "last_updated": "2025-08-29T12:30:45+05:30"
}

# Real-time pub/sub for WebSocket broadcasting
PUBLISH intraday_updates:NSE_EQ:2475 → {
  "type": "candle_update",
  "candle": {processed_candle_data},
  "timestamp": "2025-08-29T12:30:45+05:30"
}
```

### Redis Expiration:
- **Daily candles**: 7 days (for recent fast access)
- **Latest candles**: 5 minutes (short-lived current data)
- **Pub/sub**: Immediate (real-time messaging)

### Redis Usage:
- Fast WebSocket responses (sub-millisecond)
- Real-time candle caching
- Live data broadcasting to clients

---

## 2. **TimescaleDB** - Long-term Candlestick Persistence

### What TimescaleDB Stores:
- ✅ **Finalized 1-minute Candlestick Data** (NOT raw ticks)
- ✅ **Historical OHLCV candles** for long-term storage
- ✅ **Candle Metadata** - Daily statistics and tracking information

### TimescaleDB Schema:
```sql
-- Main candles table (stores ONLY processed 1-minute candles)
CREATE TABLE candles (
    id BIGSERIAL,
    exchange VARCHAR(10) NOT NULL,        -- 'NSE_EQ', 'BSE_EQ'
    scrip_token VARCHAR(20) NOT NULL,     -- '2475', '2885', '11536'
    minute_ts TIMESTAMPTZ NOT NULL,       -- '2025-08-29 09:15:00+05:30'
    open_price DECIMAL(12,4) NOT NULL,    -- 232.5000
    high_price DECIMAL(12,4) NOT NULL,    -- 233.0000
    low_price DECIMAL(12,4) NOT NULL,     -- 232.0000
    close_price DECIMAL(12,4) NOT NULL,   -- 232.7500
    volume BIGINT NOT NULL DEFAULT 0,     -- 15000
    source VARCHAR(20) NOT NULL,          -- 'live_tick', 'historical_api'
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (minute_ts, exchange, scrip_token)
);

-- Metadata tracking table
CREATE TABLE candle_metadata (
    exchange VARCHAR(10) NOT NULL,
    scrip_token VARCHAR(20) NOT NULL,
    date DATE NOT NULL,                   -- '2025-08-29'
    total_candles INTEGER DEFAULT 0,      -- 375 (full trading day)
    first_candle_ts TIMESTAMPTZ,          -- '09:15:00+05:30'
    last_candle_ts TIMESTAMPTZ,           -- '15:30:00+05:30'
    market_open_ts TIMESTAMPTZ,           -- '09:15:00+05:30'
    market_close_ts TIMESTAMPTZ,          -- '15:30:00+05:30'
    realtime_candles INTEGER DEFAULT 0,   -- Candles from live ticks
    historical_candles INTEGER DEFAULT 0, -- Candles from API backfill
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (exchange, scrip_token, date)
);
```

### Sample TimescaleDB Data:
```sql
-- Example candle records
INSERT INTO candles VALUES 
('NSE_EQ', '2475', '2025-08-29 09:15:00+05:30', 230.00, 235.00, 229.50, 234.25, 25000, 'live_tick'),
('NSE_EQ', '2475', '2025-08-29 09:16:00+05:30', 234.25, 236.00, 233.00, 235.50, 18000, 'live_tick'),
('NSE_EQ', '2475', '2025-08-29 09:17:00+05:30', 235.50, 237.00, 234.00, 236.75, 22000, 'live_tick');
-- ... continues for all 375 minutes of trading day
```

### TimescaleDB Usage:
- Long-term storage (months/years)
- Historical API queries (`GET /intraday/{exchange}/{token}`)
- Analytics and backtesting
- Regulatory compliance and audit trails
- Time-series analysis and reporting

---

## 3. **SQLite** - Stock Metadata & Symbol Mapping

### What SQLite Stores:
- ✅ **Stock Symbol/Token Mapping** (for instant lookups)
- ✅ **Company Information** and metadata
- ✅ **Exchange Details** and trading information

### SQLite Schema:
```sql
CREATE TABLE stocks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol TEXT UNIQUE NOT NULL,          -- 'AERO', 'RELIANCE', 'TCS'
    token TEXT NOT NULL,                  -- '2475', '2885', '11536'
    exchange TEXT NOT NULL,               -- 'NSE_EQ', 'BSE_EQ'
    company_name TEXT,                    -- 'Aerotech Industries Ltd'
    sector TEXT,                          -- 'Industrial Manufacturing'
    market_cap DECIMAL(15,2),             -- 5000000000.00
    is_active BOOLEAN DEFAULT 1,          -- true/false
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for fast lookups
CREATE INDEX idx_stocks_token ON stocks(token);
CREATE INDEX idx_stocks_symbol ON stocks(symbol);
CREATE INDEX idx_stocks_exchange ON stocks(exchange);
```

### In-Memory Mapping (Loaded at Startup):
```go
// Fast O(1) token → symbol resolution
tokenToSymbol := map[string]string{
    "2475":  "AERO",
    "2885":  "RELIANCE", 
    "11536": "TCS",
    // ... all other stocks
}

// Fast O(1) symbol → token resolution  
symbolToToken := map[string]string{
    "AERO":     "2475",
    "RELIANCE": "2885",
    "TCS":      "11536",
    // ... all other stocks
}
```

### SQLite Usage:
- Instant token → symbol resolution during tick processing
- Stock metadata for API responses
- Exchange and company information
- Trading status and market data

---

## 🔄 Complete Data Flow & Processing Pipeline

### 1. **Raw Tick Data Processing:**
```
B2C WebSocket API
    ↓ (Raw tick: price=232.75, volume=100, timestamp=now)
Python Bridge (b2c_bridge.py)
    ↓ (JSON stdout: {"token":"2475","ltp":232.75,"volume":100,...})
Go Application (main.go)
    ↓ (Parse JSON, validate data)
Token Mapping (SQLite lookup)
    ↓ (token "2475" → symbol "AERO")
Tick Normalizer
    ↓ (Validate, sanitize, normalize tick data)
Candle Engine
    ↓ (Aggregate ticks into 1-minute OHLCV candles)
Storage Layer
    ↓ (Store processed candles, NOT raw ticks)
Redis Cache + TimescaleDB Persistence
    ↓ (Fast access + long-term storage)
WebSocket Broadcasting
    ↓ (Real-time candle updates to clients)
```

### 2. **Key Processing Points:**

**❌ Raw Ticks Are NOT Stored:**
- Raw tick data is processed in real-time and discarded
- Only aggregated 1-minute candles are persisted
- This saves massive storage space and improves query performance

**✅ Only Processed Candles Are Stored:**
- Each 1-minute candle represents aggregated tick data
- OHLCV (Open, High, Low, Close, Volume) format
- Efficient storage and fast historical queries

### 3. **Example Data Transformation:**

**Input (Multiple Raw Ticks in 1 Minute):**
```json
// 12:30:00 - 12:30:59 (multiple ticks)
{"token":"2475", "ltp":232.50, "volume":50, "timestamp":1693298100000}
{"token":"2475", "ltp":233.00, "volume":75, "timestamp":1693298115000}  
{"token":"2475", "ltp":232.25, "volume":100, "timestamp":1693298130000}
{"token":"2475", "ltp":232.75, "volume":80, "timestamp":1693298145000}
// ... more ticks in the same minute
```

**Output (Single 1-Minute Candle):**
```json
{
  "exchange": "NSE_EQ",
  "scrip_token": "2475",
  "symbol": "AERO", 
  "minute_ts": "2025-08-29T12:30:00+05:30",
  "open": 232.50,    // First tick price in the minute
  "high": 233.00,    // Highest tick price in the minute  
  "low": 232.25,     // Lowest tick price in the minute
  "close": 232.75,   // Last tick price in the minute
  "volume": 305,     // Sum of all tick volumes (50+75+100+80)
  "source": "live_tick"
}
```

---

## 📊 Storage Comparison Table

| Database | Data Type | Storage Duration | Access Speed | Use Case |
|----------|-----------|------------------|--------------|----------|
| **Redis** | Processed Candles | 7 days | Sub-millisecond | Real-time WebSocket, Caching |
| **TimescaleDB** | Processed Candles | Long-term (years) | Fast (indexed) | Historical API, Analytics |
| **SQLite** | Stock Metadata | Permanent | Instant (in-memory) | Token/Symbol mapping |

## 🎯 Key Benefits of This Architecture:

1. **Efficient Storage**: Only store processed candles, not raw ticks
2. **Fast Queries**: Pre-aggregated data enables quick responses  
3. **Real-time Performance**: Redis caching for instant WebSocket updates
4. **Scalability**: Time-series database optimized for financial data
5. **Data Integrity**: Dual storage (cache + persistence) ensures reliability

This architecture provides optimal performance for real-time market data streaming while maintaining efficient storage and fast historical data access.
