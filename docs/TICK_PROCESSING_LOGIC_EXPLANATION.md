# 🔄 Tick Processing Logic: Normalization & Candle Aggregation

## 📊 Complete Flow: Raw Tick → Normalized Tick → 1-Minute Candle

This document explains the detailed logic of how raw market data ticks are processed, normalized, and aggregated into 1-minute OHLCV candles.

---

## 1. **Tick Normalization & Validation Logic** (`internal/tick/normalizer.go`)

### **Purpose**: Clean, validate, and deduplicate incoming raw tick data

### **Step-by-Step Process:**

#### **Input: Raw Market Data from Python Bridge**
```json
{
  "symbol": "AERO",
  "token": "2475", 
  "ltp": 232.75,           // Last Traded Price
  "high": 233.00,
  "low": 232.00,
  "open": 232.50,
  "close": 232.75,
  "volume": 100,           // Tick volume
  "timestamp": 1693298145000  // Milliseconds
}
```

#### **Validation Steps:**

**1. Required Field Validation:**
```go
// Check essential fields
if data.Token == "" {
    return nil, fmt.Errorf("missing token in market data")
}
if data.Symbol == "" {
    return nil, fmt.Errorf("missing symbol in market data") 
}
if data.LTP <= 0 {
    return nil, fmt.Errorf("invalid LTP: %f", data.LTP)
}
```

**2. Timestamp Normalization:**
```go
// Convert millisecond timestamp to IST timezone
var tickTime time.Time
if data.Timestamp > 0 {
    // Convert milliseconds to time.Time in IST
    tickTime = time.Unix(data.Timestamp/1000, (data.Timestamp%1000)*1000000).In(istLocation)
} else {
    // Fallback to current time if missing
    tickTime = time.Now().In(istLocation)
}
```

**3. Exchange Determination:**
```go
// Determine exchange based on token patterns
func determineExchange(token, symbol string) string {
    // BSE tokens are typically longer numeric strings
    if len(token) >= 6 {
        return "BSE"
    }
    // NSE is default
    return "NSE"
}
```

**4. Deduplication Logic:**
```go
// Generate hash for duplicate detection
func generateTickHash(token string, price float64, volume int64, timestamp time.Time) string {
    // Hash based on token, price, volume, and minute boundary
    minuteTimestamp := timestamp.Truncate(time.Minute).Unix()
    return fmt.Sprintf("%s_%.4f_%d_%d", token, price, volume, minuteTimestamp)
}

// Check for duplicates
func isDuplicate(tick *NormalizedTick) bool {
    lastTick, exists := lastTicks[tick.ScripToken]
    if !exists {
        return false
    }
    // Same hash = duplicate within same minute
    return lastTick.Hash == tick.Hash
}
```

**5. Market Hours Validation:**
```go
// Validate trading hours (09:15 - 15:30 IST)
func ValidateMarketHours(timestamp time.Time) bool {
    istTime := timestamp.In(istLocation)
    
    marketOpen := 9*time.Hour + 15*time.Minute   // 09:15
    marketClose := 15*time.Hour + 30*time.Minute // 15:30
    
    timeOfDay := time.Duration(istTime.Hour())*time.Hour + 
                time.Duration(istTime.Minute())*time.Minute
    
    return timeOfDay >= marketOpen && timeOfDay <= marketClose
}
```

#### **Output: Normalized Tick**
```go
type NormalizedTick struct {
    Exchange   string    // "NSE", "BSE"
    ScripToken string    // "2475"
    Symbol     string    // "AERO"
    Price      float64   // 232.75
    Volume     int64     // 100
    Timestamp  time.Time // 2025-08-29T12:30:45+05:30
    Hash       string    // "2475_232.7500_100_1693298100"
}
```

#### **Statistics Tracking:**
```go
type TickNormalizer struct {
    totalTicks      int64  // All received ticks
    normalizedTicks int64  // Successfully processed
    duplicateTicks  int64  // Filtered duplicates
    errorTicks      int64  // Validation failures
}
```

---

## 2. **Candle Engine Aggregation Logic** (`internal/candle/engine.go`)

### **Purpose**: Aggregate normalized ticks into 1-minute OHLCV candles

### **Key Concepts:**

#### **Per-Token State Management:**
```go
type TokenCandleState struct {
    exchange       string     // "NSE"
    scripToken     string     // "2475"
    symbol         string     // "AERO"
    currentMinute  time.Time  // 2025-08-29T12:30:00+05:30
    currentCandle  *Candle    // Current minute's candle being built
    lastKnownPrice float64    // For synthetic candles
    lastTickTime   time.Time  // Last tick timestamp
}
```

### **Step-by-Step Aggregation Process:**

#### **1. Tick Processing Entry Point:**
```go
func ProcessTick(tick Tick) error {
    // Convert to IST and get minute boundary
    tickTimeIST := tick.Timestamp.In(istLocation)
    minuteStart := tickTimeIST.Truncate(time.Minute) // Floor to minute
    
    // Validate market hours
    if !isMarketHours(tickTimeIST) {
        return nil // Ignore outside market hours
    }
    
    // Get or create token state
    state := getOrCreateTokenState(tick.Exchange, tick.ScripToken, tick.Symbol)
}
```

#### **2. Minute Boundary Detection:**
```go
// Handle minute boundary crossing
if state.currentMinute.IsZero() || !minuteStart.Equal(state.currentMinute) {
    
    // Finalize previous minute if exists
    if state.currentCandle != nil {
        finalizeCandle(state)
    }
    
    // Fill any gap minutes with synthetic candles
    if !state.currentMinute.IsZero() {
        fillGapMinutes(state, state.currentMinute.Add(time.Minute), minuteStart)
    }
    
    // Start new minute
    startNewMinute(state, minuteStart, tick.Price)
}
```

#### **3. New Minute Initialization:**
```go
func startNewMinute(state *TokenCandleState, minuteStart time.Time, openPrice float64) {
    state.currentMinute = minuteStart
    state.currentCandle = &Candle{
        Exchange:   state.exchange,
        ScripToken: state.scripToken,
        MinuteTS:   minuteStart,
        Open:       openPrice,  // First tick price of the minute
        High:       openPrice,  // Initialize with first price
        Low:        openPrice,  // Initialize with first price
        Close:      openPrice,  // Initialize with first price
        Volume:     0,          // Will accumulate
        Source:     "realtime",
    }
}
```

#### **4. OHLCV Update Logic:**
```go
func updateCurrentCandle(state *TokenCandleState, tick Tick) {
    candle := state.currentCandle
    
    // Update High (maximum price in the minute)
    if tick.Price > candle.High {
        candle.High = tick.Price
    }
    
    // Update Low (minimum price in the minute)
    if tick.Price < candle.Low {
        candle.Low = tick.Price
    }
    
    // Update Close (always latest price)
    candle.Close = tick.Price
    
    // Accumulate Volume
    candle.Volume += tick.Volume
    
    // Update state tracking
    state.lastKnownPrice = tick.Price
    state.lastTickTime = tick.Timestamp
}
```

#### **5. Real-time Broadcasting:**
```go
// Publish live update after each tick
update := CandleUpdate{
    Type:   "update",
    Candle: *state.currentCandle,
}

// Send to WebSocket clients
updateChannel <- update

// Execute callback for Redis/WebSocket broadcasting
if publishCallback != nil {
    publishCallback(update)
}
```

#### **6. Candle Finalization:**
```go
func finalizeCandle(state *TokenCandleState) {
    finalCandle := *state.currentCandle
    
    // Persist to storage (TimescaleDB)
    if persistCallback != nil {
        persistCallback(finalCandle)
    }
    
    // Publish close event
    closeUpdate := CandleUpdate{
        Type:   "close",
        Candle: finalCandle,
    }
    
    closeChannel <- closeUpdate
    
    // Clear current candle
    state.currentCandle = nil
}
```

#### **7. Gap Filling with Synthetic Candles:**
```go
func fillGapMinutes(state *TokenCandleState, fromMinute, toMinute time.Time) {
    current := fromMinute
    
    for current.Before(toMinute) {
        // Skip if outside market hours
        if !isMarketHours(current) {
            current = current.Add(time.Minute)
            continue
        }
        
        // Create synthetic candle with last known price
        syntheticCandle := Candle{
            Exchange:   state.exchange,
            ScripToken: state.scripToken,
            MinuteTS:   current,
            Open:       state.lastKnownPrice,  // Same price for OHLC
            High:       state.lastKnownPrice,
            Low:        state.lastKnownPrice,
            Close:      state.lastKnownPrice,
            Volume:     0,                     // No volume
            Source:     "synthetic",
        }
        
        // Persist and publish synthetic candle
        persistCallback(syntheticCandle)
        publishCallback(closeUpdate)
        
        current = current.Add(time.Minute)
    }
}
```

#### **8. Late Tick Handling:**
```go
func ProcessLateTick(tick Tick) error {
    tickAge := time.Now().Sub(tick.Timestamp)
    
    // Check late tolerance (default: 2 minutes)
    if tickAge > lateTolerance {
        return fmt.Errorf("tick too old: %v", tickAge)
    }
    
    // Create patch candle for historical minute
    patchCandle := Candle{
        Exchange:   tick.Exchange,
        ScripToken: tick.ScripToken,
        MinuteTS:   tick.Timestamp.Truncate(time.Minute),
        // ... update with late tick data
        Source:     "realtime",
    }
    
    // Publish patch event
    patchUpdate := CandleUpdate{
        Type:   "patch",
        Candle: patchCandle,
    }
    
    patchChannel <- patchUpdate
}
```

---

## 3. **Complete Example: Multiple Ticks → Single Candle**

### **Input: Multiple Ticks in 12:30:00 - 12:30:59**

```json
// Tick 1 at 12:30:15
{
  "token": "2475",
  "symbol": "AERO", 
  "ltp": 232.50,
  "volume": 50,
  "timestamp": 1693298115000
}

// Tick 2 at 12:30:30
{
  "token": "2475",
  "symbol": "AERO",
  "ltp": 233.25,  // New high
  "volume": 75,
  "timestamp": 1693298130000
}

// Tick 3 at 12:30:45
{
  "token": "2475", 
  "symbol": "AERO",
  "ltp": 232.10,  // New low
  "volume": 100,
  "timestamp": 1693298145000
}

// Tick 4 at 12:30:58
{
  "token": "2475",
  "symbol": "AERO", 
  "ltp": 232.75,  // Final close
  "volume": 80,
  "timestamp": 1693298158000
}
```

### **Processing Steps:**

**1. First Tick (12:30:15):**
```go
// New minute detected: 12:30:00
startNewMinute(state, "12:30:00", 232.50)

currentCandle = {
    MinuteTS: "2025-08-29T12:30:00+05:30",
    Open:    232.50,  // First tick price
    High:    232.50,  // Initialize
    Low:     232.50,  // Initialize  
    Close:   232.50,  // Initialize
    Volume:  50,      // First tick volume
}

// Broadcast live update
publishUpdate(currentCandle)
```

**2. Second Tick (12:30:30):**
```go
// Same minute, update candle
updateCurrentCandle(state, tick)

currentCandle = {
    MinuteTS: "2025-08-29T12:30:00+05:30",
    Open:    232.50,  // Unchanged (first tick)
    High:    233.25,  // Updated (new high)
    Low:     232.50,  // Unchanged
    Close:   233.25,  // Updated (latest price)
    Volume:  125,     // Accumulated (50 + 75)
}

// Broadcast live update
publishUpdate(currentCandle)
```

**3. Third Tick (12:30:45):**
```go
// Same minute, update candle
updateCurrentCandle(state, tick)

currentCandle = {
    MinuteTS: "2025-08-29T12:30:00+05:30",
    Open:    232.50,  // Unchanged
    High:    233.25,  // Unchanged
    Low:     232.10,  // Updated (new low)
    Close:   232.10,  // Updated (latest price)
    Volume:  225,     // Accumulated (125 + 100)
}

// Broadcast live update
publishUpdate(currentCandle)
```

**4. Fourth Tick (12:30:58):**
```go
// Same minute, final update
updateCurrentCandle(state, tick)

currentCandle = {
    MinuteTS: "2025-08-29T12:30:00+05:30",
    Open:    232.50,  // Unchanged (first tick)
    High:    233.25,  // Unchanged (highest)
    Low:     232.10,  // Unchanged (lowest)
    Close:   232.75,  // Updated (final price)
    Volume:  305,     // Final total (225 + 80)
}

// Broadcast live update
publishUpdate(currentCandle)
```

**5. Minute Boundary (12:31:00):**
```go
// Finalize completed candle
finalizeCandle(state)

finalCandle = {
    Exchange:   "NSE",
    ScripToken: "2475",
    Symbol:     "AERO",
    MinuteTS:   "2025-08-29T12:30:00+05:30",
    Open:       232.50,  // First tick price in minute
    High:       233.25,  // Highest tick price in minute
    Low:        232.10,  // Lowest tick price in minute
    Close:      232.75,  // Last tick price in minute
    Volume:     305,     // Sum of all tick volumes
    Source:     "realtime"
}

// Store in TimescaleDB
persistCallback(finalCandle)

// Cache in Redis
publishCallback(closeUpdate)

// Broadcast to WebSocket clients
closeChannel <- closeUpdate
```

---

## 4. **Key Features & Benefits**

### **Real-time Processing:**
- ✅ **Live Updates**: Each tick immediately updates current candle
- ✅ **WebSocket Broadcasting**: Real-time candle updates to clients
- ✅ **Sub-second Latency**: Immediate processing and broadcasting

### **Data Integrity:**
- ✅ **Deduplication**: Prevents duplicate tick processing
- ✅ **Validation**: Ensures data quality and consistency
- ✅ **Market Hours**: Only processes ticks during trading hours

### **Gap Handling:**
- ✅ **Synthetic Candles**: Fills missing minutes with last known price
- ✅ **Late Tick Processing**: Handles delayed ticks within tolerance
- ✅ **Continuous Series**: Ensures complete minute-by-minute data

### **Performance Optimization:**
- ✅ **Per-Token State**: Concurrent processing for multiple stocks
- ✅ **Memory Efficient**: Minimal state tracking per token
- ✅ **Channel-based**: Non-blocking event publishing

### **Monitoring & Statistics:**
- ✅ **Processing Metrics**: Success rates, error tracking
- ✅ **Performance Stats**: Processing rates, latency monitoring
- ✅ **Health Checks**: System status and component health

This architecture ensures reliable, real-time conversion of raw market ticks into standardized 1-minute OHLCV candles with comprehensive error handling and monitoring.
