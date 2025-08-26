# Historical Price Manager API - How It Works

## What the Historical Price Manager Does

The Historical Price Manager solves a key problem: **merging historical price data with live streaming data** to create continuous price series for charting applications.

### The Problem You Had:
- Historical data comes from your trading API: `https://trading.indiratrade.com:3000/v1/chart/data/...`
- Live data comes from WebSocket stream (Odin → b2c_bridge.py → Go service)
- Charts need continuous data without gaps between historical and live

### The Solution:
The Historical Price Manager creates **seamless continuous series** by:
1. Fetching historical data from your API
2. Caching it in SQLite for performance
3. Merging it with live WebSocket data
4. Providing unified API endpoints for clients

## API Endpoints for Clients

### 1. **Historical Data Only** 
```
GET /api/historical/{SYMBOL}?from={timestamp}&to={timestamp}&interval={1MIN|5MIN}
```

**Example:**
```bash
curl "http://localhost:8081/api/historical/RELIANCE?from=1724505600000&to=1724592000000&interval=1MIN"
```

**Response:**
```json
{
  "symbol": "RELIANCE",
  "interval": "1MIN",
  "from": 1724505600000,
  "to": 1724592000000,
  "count": 375,
  "data": [
    {
      "symbol": "RELIANCE",
      "O": 2850.5,
      "H": 2855.0,
      "L": 2848.0,
      "C": 2852.5,
      "V": 125000,
      "DT": "2025-08-25 09:15:00",
      "timestamp": 1724505900000,
      "source": "HISTORICAL"
    }
  ],
  "data_source": "HISTORICAL"
}
```

### 2. **Continuous Series (Historical + Live)**
```
GET /api/continuous/{SYMBOL}?period={1D|1W|1M}&interval={1MIN}
```

**Example:**
```bash
curl "http://localhost:8081/api/continuous/RELIANCE?period=1D&interval=1MIN"
```

**Response:**
```json
{
  "symbol": "RELIANCE",
  "interval": "1MIN",
  "period": "1D",
  "total_candles": 378,
  "historical_count": 375,
  "live_count": 3,
  "has_gaps": false,
  "is_complete": true,
  "data": [
    {
      "symbol": "RELIANCE",
      "O": 2850.5,
      "H": 2855.0,
      "L": 2848.0,
      "C": 2852.5,
      "V": 125000,
      "source": "HISTORICAL"
    },
    {
      "symbol": "RELIANCE", 
      "O": 2852.5,
      "H": 2854.0,
      "L": 2851.0,
      "C": 2853.2,
      "V": 45000,
      "source": "LIVE"
    }
  ],
  "historical_end": 1724591700000,
  "live_start": 1724591760000,
  "data_source": "CONTINUOUS"
}
```

### 3. **Chart Data (Optimized for TradingView/Charts)**
```
GET /api/chart/{SYMBOL}?period={1D|1W|1M}&interval={1MIN}
```

**Example:**
```bash
curl "http://localhost:8081/api/chart/RELIANCE?period=1D&interval=1MIN"
```

**Response (TradingView Format):**
```json
{
  "s": "ok",
  "symbol": "RELIANCE",
  "meta": {
    "interval": "1MIN",
    "period": "1D",
    "total_candles": 378,
    "historical_count": 375,
    "live_count": 3,
    "exchange": "NSE",
    "token": "1594"
  },
  "data": [
    {
      "time": 1724505900000,
      "open": 2850.5,
      "high": 2855.0,
      "low": 2848.0,
      "close": 2852.5,
      "volume": 125000,
      "source": "HISTORICAL"
    }
  ]
}
```

## How Clients Connect and Get Data

### Step 1: Client Makes HTTP Request
```javascript
// JavaScript example
const response = await fetch('http://localhost:8081/api/continuous/RELIANCE?period=1D&interval=1MIN');
const data = await response.json();

// Now you have continuous price series
console.log(`Total candles: ${data.total_candles}`);
console.log(`Historical: ${data.historical_count}, Live: ${data.live_count}`);
```

### Step 2: System Processes Request
1. **Historical Manager** checks if symbol exists in database
2. Gets token mapping: `RELIANCE → token 1594`
3. Fetches historical data from your API: `https://trading.indiratrade.com:3000/v1/chart/data/NSE_EQ/1594/1/MIN/RELIANCE`
4. Merges with live data from WebSocket stream
5. Returns unified continuous series

### Step 3: Client Gets Seamless Data
- No gaps between historical and live data
- Consistent format across all data points
- Clear indication of data source (HISTORICAL vs LIVE)
- Ready for charting libraries

## Data Flow Architecture

```
Your Chart Application
        ↓ HTTP Request
Historical API Endpoints (/api/continuous/RELIANCE)
        ↓
Historical Price Manager
        ↓                    ↓
Historical API          Live WebSocket
(Your Trading API)      (Odin Stream)
        ↓                    ↓
SQLite Cache ←→ Memory Cache ←→ Live Data Buffer
        ↓
Merged Continuous Series
        ↓
JSON Response to Client
```

## Key Benefits for Your Chart Application

1. **No Data Gaps**: Seamless transition from historical to live data
2. **High Performance**: SQLite caching + memory optimization
3. **Indian Market Timezone**: Proper IST handling
4. **Multiple Formats**: Historical, Continuous, Chart-optimized
5. **Real-time Updates**: Live data automatically merged
6. **Backfill Support**: Fill missing data gaps
7. **Cache Management**: Clear cache, get stats

## Example Integration in Your Chart App

```javascript
class StockChart {
  async loadChart(symbol, period = '1D', interval = '1MIN') {
    // Get continuous series (historical + live)
    const response = await fetch(
      `http://localhost:8081/api/continuous/${symbol}?period=${period}&interval=${interval}`
    );
    const data = await response.json();
    
    // Load into your charting library
    this.chart.setData(data.data);
    
    // Connect to WebSocket for live updates
    this.connectWebSocket(symbol);
  }
  
  connectWebSocket(symbol) {
    const ws = new WebSocket(`ws://localhost:8081/stream?stocks=${symbol}`);
    ws.onmessage = (event) => {
      const liveData = JSON.parse(event.data);
      // Add live data to existing chart
      this.chart.addLiveData(liveData);
    };
  }
}
```

This gives you **continuous price lines** for your charts with no gaps between historical and live data!
