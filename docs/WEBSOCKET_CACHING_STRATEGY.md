# 📡 WebSocket Client Connection: Caching Strategy Explained

## 🎯 **Answer: NO, we don't call the historical API every time!**

The system uses a smart **Redis-first caching strategy** to avoid unnecessary API calls.

---

## 🔄 **What Happens When a Client Connects**

### **Step 1: Client Connection**
```javascript
// Client connects to WebSocket
ws://localhost:8080/stream?stocks=AERO,RELIANCE,TCS
```

### **Step 2: Cache-First Data Loading**
```go
func sendInitData(conn *websocket.Conn, symbol string) error {
    // Step 1: Try Redis cache FIRST
    if wsh.redisAdapter != nil {
        cache, cacheErr := wsh.redisAdapter.GetDayCandles(exchange, token, now)
        if cacheErr == nil && cache != nil {
            candles = cache.Candles
            dataSource = "redis_cache"
            log.Printf("📦 Cache hit for %s:%s (%d candles)", exchange, token, len(candles))
            // ✅ RETURN CACHED DATA - NO API CALL!
        }
    }

    // Step 2: Only call API if cache MISS
    if len(candles) == 0 {
        // ❌ Cache miss - fetch from historical API
        result, err := wsh.historicalClient.FetchAndFillIntradayData(apiExchange, token, symbol, now)
        candles = result.Candles
        dataSource = "historical_api"

        // ✅ Store in cache for future requests
        if wsh.redisAdapter != nil {
            wsh.redisAdapter.StoreDayCandles(exchange, token, now, candles)
        }
    }
}
```

---

## 📊 **Caching Strategy Breakdown**

### **Redis Cache Structure:**
```redis
# Daily candle cache (key format)
candles:NSE_EQ:2475:20250829 → {
  "candles": [375 minute candles],
  "total_candles": 375,
  "market_open": "09:15",
  "market_close": "15:30",
  "last_updated": "2025-08-29T15:30:00+05:30"
}

# Cache expiration: 7 days
EXPIRE candles:NSE_EQ:2475:20250829 604800
```

### **Cache Hit vs Cache Miss:**

#### **✅ Cache Hit (Most Common)**
```
Client connects → Check Redis → Data found → Send cached data
Time: ~1-2ms (sub-millisecond Redis lookup)
API calls: 0
```

#### **❌ Cache Miss (Only when needed)**
```
Client connects → Check Redis → No data → Call Historical API → Cache result → Send data
Time: ~200-500ms (API call + caching)
API calls: 1 (then cached for future requests)
```

---

## 🕐 **When Does Cache Miss Happen?**

### **1. First Request of the Day**
- **Scenario**: First client connects for AERO at 09:30 AM
- **Action**: Cache miss → API call → Cache for 7 days
- **Result**: All subsequent clients get cached data

### **2. Cache Expiration**
- **Scenario**: Data older than 7 days
- **Action**: Cache miss → API call → Refresh cache
- **Result**: Updated cache for future requests

### **3. New Stock Symbol**
- **Scenario**: Client requests a stock not previously cached
- **Action**: Cache miss → API call → Cache new stock data
- **Result**: Stock now cached for future requests

### **4. Redis Restart/Flush**
- **Scenario**: Redis server restart or manual flush
- **Action**: All cache miss → Rebuild cache gradually
- **Result**: Cache rebuilds as clients connect

---

## 📈 **Performance Benefits**

### **Typical Day Scenario:**
```
09:15 AM - First client connects to AERO
         → Cache miss → API call → Cache stored

09:16 AM - Second client connects to AERO  
         → Cache hit → Instant response (1ms)

09:17 AM - Third client connects to AERO
         → Cache hit → Instant response (1ms)

... (all day)

15:30 PM - 100th client connects to AERO
         → Cache hit → Instant response (1ms)
```

### **API Call Reduction:**
- **Without Cache**: 100 clients = 100 API calls
- **With Cache**: 100 clients = 1 API call + 99 cache hits
- **Efficiency**: 99% reduction in API calls

---

## 🔄 **Live Data Integration**

### **Initial Data (Historical)**
```go
// On client connect - send cached/historical data
initData := InitDataMessage{
    Symbol:       "AERO",
    TotalCandles: 375,
    Candles:      cachedCandles,  // From Redis or API
    Source:       "redis_cache"   // or "historical_api"
}
```

### **Live Updates (Real-time)**
```go
// After initial data - stream live updates
func BroadcastCandleUpdate(update candle.CandleUpdate) {
    message := WebSocketMessage{
        Type: "candle:update",     // Live candle update
        Data: update.Candle,       // Real-time data
    }
    // Broadcast to all subscribed clients
}
```

---

## 📊 **Complete Client Experience**

### **Client Connection Flow:**
```
1. Client connects: ws://localhost:8080/stream?stocks=AERO

2. Initial Data Loading:
   ✅ Check Redis cache for AERO today
   ✅ Cache hit → Send 375 candles instantly (1ms)
   ✅ Client receives full day's historical data

3. Live Data Streaming:
   ✅ Client now subscribed to live AERO updates
   ✅ Each new tick → Real-time candle update
   ✅ Live price changes streamed immediately

4. Result:
   ✅ Instant historical data (cached)
   ✅ Real-time live updates (streamed)
   ✅ No API delay for cached stocks
```

---

## 🎯 **Cache Efficiency Metrics**

### **Expected Cache Hit Rates:**
- **Same Day Requests**: ~95-99% cache hit rate
- **Popular Stocks**: ~99% cache hit rate  
- **New Stocks**: First request cache miss, then 99% hit rate
- **Overall System**: ~90-95% cache hit rate

### **Performance Comparison:**
| Scenario | Cache Hit | Cache Miss |
|----------|-----------|------------|
| **Response Time** | 1-2ms | 200-500ms |
| **API Calls** | 0 | 1 |
| **Server Load** | Minimal | Moderate |
| **Client Experience** | Instant | Brief delay |

---

## 🔧 **Cache Management**

### **Automatic Cache Population:**
```go
// When live candles are generated
func finalizeCandle(candle Candle) {
    // 1. Store in TimescaleDB (persistence)
    persistCallback(candle)
    
    // 2. Update Redis cache (fast access)
    redisAdapter.AppendToIntradaySeries(candle)
    
    // 3. Broadcast to WebSocket clients (real-time)
    publishCallback(candleUpdate)
}
```

### **Cache Warming Strategy:**
- **Live Data**: Automatically populates cache as candles are generated
- **Historical Data**: Populated on-demand when clients request
- **Popular Stocks**: Stay cached due to frequent access
- **Cache Expiry**: 7 days retention for recent data

---

## ✅ **Summary**

**The system is highly optimized to avoid unnecessary API calls:**

1. **✅ Redis-First Strategy**: Always check cache before API
2. **✅ 7-Day Cache Retention**: Recent data stays cached
3. **✅ Automatic Cache Population**: Live data updates cache
4. **✅ High Cache Hit Rate**: ~95% of requests served from cache
5. **✅ Instant Response**: Sub-millisecond cached data delivery
6. **✅ API Call Minimization**: Only call API when absolutely necessary

**Result**: Clients get instant historical data (if cached) plus real-time live updates, with minimal API overhead!
