# Odin Streamer WebSocket API Documentation

Complete API reference for integrating with the Odin Streamer WebSocket service.

## 📡 WebSocket Endpoints

### 1. Basic WebSocket - `/stream`
Simple WebSocket endpoint for real-time market data streaming.

**Connection URL:**
```
ws://localhost:8080/stream?stocks=SYMBOL1,SYMBOL2,SYMBOL3
```

**Parameters:**
- `stocks` (required): Comma-separated list of stock symbols (e.g., "RELIANCE,TCS,INFY")

### 2. Enhanced WebSocket - `/enhanced-stream`
Advanced WebSocket endpoint with dynamic subscription management.

**Connection URL:**
```
ws://localhost:8080/enhanced-stream?client_id=YOUR_CLIENT_ID
```

**Parameters:**
- `client_id` (optional): Unique client identifier for session persistence

## 📨 Message Types

### Incoming Messages (Client → Server)

#### 1. Subscribe Request
```json
{
  "type": "request",
  "action": "subscribe",
  "stocks": ["RELIANCE", "TCS", "INFY"],
  "timestamp": 1704794400000
}
```

#### 2. Unsubscribe Request
```json
{
  "type": "request",
  "action": "unsubscribe",
  "stocks": ["RELIANCE"],
  "timestamp": 1704794400000
}
```

#### 3. Ping Request
```json
{
  "type": "request",
  "action": "ping",
  "timestamp": 1704794400000
}
```

#### 4. Get Subscriptions
```json
{
  "type": "request",
  "action": "get_subscriptions",
  "timestamp": 1704794400000
}
```

#### 5. Market Status Request
```json
{
  "type": "request",
  "action": "market_status",
  "timestamp": 1704794400000
}
```

### Outgoing Messages (Server → Client)

#### 1. Welcome Message
Sent immediately after connection establishment.

```json
{
  "type": "welcome",
  "status": "connected",
  "client_id": "client_1704794400123",
  "data": {
    "message": "Connected to Odin Streamer Enhanced WebSocket",
    "features": ["dynamic_subscriptions", "market_hours_control", "persistent_connection", "real_time_data"],
    "market_hours": "09:15 - 15:30 IST",
    "market_open": true,
    "current_time": "14:30:45",
    "subscribed_stocks": 0,
    "instructions": {
      "subscribe": "{\"type\": \"request\", \"action\": \"subscribe\", \"stocks\": [\"RELIANCE\", \"TCS\"]}",
      "unsubscribe": "{\"type\": \"request\", \"action\": \"unsubscribe\", \"stocks\": [\"RELIANCE\"]}",
      "ping": "{\"type\": \"request\", \"action\": \"ping\"}"
    }
  },
  "timestamp": 1704794400000
}
```

#### 2. Initial Data (Basic WebSocket)
Historical candle data sent on connection.

```json
{
  "type": "init_data",
  "symbol": "RELIANCE",
  "token": "738561",
  "exchange": "NSE",
  "data": {
    "symbol": "RELIANCE",
    "token": "738561",
    "exchange": "NSE",
    "date": "2025-01-09",
    "market_open": "2025-01-09T09:15:00+05:30",
    "market_close": "2025-01-09T15:30:00+05:30",
    "total_candles": 375,
    "candles": [
      {
        "scrip_token": "738561",
        "minute_ts": "2025-01-09T09:15:00+05:30",
        "open": 1245.50,
        "high": 1248.75,
        "low": 1244.20,
        "close": 1247.30,
        "volume": 125000
      }
    ]
  },
  "timestamp": 1704794400000
}
```

#### 3. Real-time Candle Updates
Live market data during trading hours.

```json
{
  "type": "candle:update",
  "symbol": "RELIANCE",
  "token": "738561",
  "exchange": "NSE",
  "data": {
    "scrip_token": "738561",
    "minute_ts": "2025-01-09T14:30:00+05:30",
    "open": 1245.50,
    "high": 1248.75,
    "low": 1244.20,
    "close": 1247.30,
    "volume": 125000
  },
  "timestamp": 1704794400000
}
```

#### 4. Candle Close Events
```json
{
  "type": "candle:close",
  "symbol": "RELIANCE",
  "token": "738561",
  "exchange": "NSE",
  "data": {
    "scrip_token": "738561",
    "minute_ts": "2025-01-09T14:29:00+05:30",
    "open": 1244.20,
    "high": 1247.80,
    "low": 1243.50,
    "close": 1246.90,
    "volume": 98500
  },
  "timestamp": 1704794400000
}
```

#### 5. 52-Week High/Low Alerts
```json
{
  "type": "52week_alert",
  "symbol": "RELIANCE",
  "token": "738561",
  "exchange": "NSE",
  "data": {
    "alert_type": "new_high",
    "current_price": 1250.00,
    "previous_high": 1248.75,
    "previous_low": 980.50,
    "date": "2025-01-09",
    "time": "14:30:15"
  },
  "timestamp": 1704794400000
}
```

#### 6. Subscription Response
```json
{
  "type": "subscription_response",
  "status": "success",
  "client_id": "client_1704794400123",
  "data": {
    "action": "subscribe",
    "subscribed_stocks": ["RELIANCE", "TCS"],
    "failed_stocks": [],
    "total_subscribed": 2
  },
  "timestamp": 1704794400000
}
```

#### 7. Market Status Response
```json
{
  "type": "market_status",
  "status": "success",
  "client_id": "client_1704794400123",
  "data": {
    "market_open": true,
    "market_hours": "09:15 - 15:30 IST",
    "current_time": "14:30:45",
    "next_open": "2025-01-10 09:15:00",
    "streaming_data": true
  },
  "timestamp": 1704794400000
}
```

#### 8. Market Status Notifications
```json
{
  "type": "market_opened",
  "status": "info",
  "message": "Market is now open - data streaming started",
  "data": {
    "market_open": true,
    "current_time": "09:15:00"
  },
  "timestamp": 1704794400000
}
```

```json
{
  "type": "market_closed",
  "status": "info",
  "message": "Market is now closed - data streaming stopped",
  "data": {
    "market_open": false,
    "current_time": "15:30:00"
  },
  "timestamp": 1704794400000
}
```

#### 9. Pong Response
```json
{
  "type": "pong",
  "status": "alive",
  "client_id": "client_1704794400123",
  "timestamp": 1704794400000
}
```

#### 10. Error Messages
```json
{
  "type": "error",
  "status": "error",
  "message": "Symbol INVALID not found in stock database",
  "client_id": "client_1704794400123",
  "timestamp": 1704794400000
}
```

## 📊 Data Structures

### Candle Object
```json
{
  "scrip_token": "738561",
  "minute_ts": "2025-01-09T14:30:00+05:30",
  "open": 1245.50,
  "high": 1248.75,
  "low": 1244.20,
  "close": 1247.30,
  "volume": 125000
}
```

**Fields:**
- `scrip_token`: Unique token identifier for the stock
- `minute_ts`: Timestamp in ISO 8601 format (IST timezone)
- `open`: Opening price for the minute
- `high`: Highest price during the minute
- `low`: Lowest price during the minute
- `close`: Closing price for the minute
- `volume`: Trading volume for the minute

### 52-Week Alert Object
```json
{
  "alert_type": "new_high",
  "current_price": 1250.00,
  "previous_high": 1248.75,
  "previous_low": 980.50,
  "date": "2025-01-09",
  "time": "14:30:15"
}
```

**Alert Types:**
- `new_high`: Stock hit a new 52-week high
- `new_low`: Stock hit a new 52-week low

## 🕐 Market Hours & Behavior

### Trading Hours
- **Market Open**: 9:15 AM IST
- **Market Close**: 3:30 PM IST
- **Timezone**: Asia/Kolkata (UTC+5:30)

### Data Streaming Behavior
- **During Market Hours**: Real-time data streaming active
- **Outside Market Hours**: No real-time data, historical data available
- **Market Status**: Automatic notifications when market opens/closes

### Weekend & Holidays
- No data streaming on weekends
- Market holidays are automatically detected
- Historical data remains available 24/7

## 🔄 Connection Management

### Connection States
1. **Connecting**: WebSocket handshake in progress
2. **Connected**: Welcome message received
3. **Subscribed**: Actively receiving market data
4. **Disconnected**: Connection lost or closed

### Reconnection Strategy
```javascript
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
const reconnectDelay = 1000; // 1 second

function connect() {
    const ws = new WebSocket('ws://localhost:8080/enhanced-stream?client_id=my_app');
    
    ws.onopen = function() {
        console.log('Connected to Odin Streamer');
        reconnectAttempts = 0;
    };
    
    ws.onclose = function() {
        if (reconnectAttempts < maxReconnectAttempts) {
            setTimeout(() => {
                reconnectAttempts++;
                console.log(`Reconnecting... Attempt ${reconnectAttempts}`);
                connect();
            }, reconnectDelay * reconnectAttempts);
        }
    };
}
```

### Heartbeat (Ping/Pong)
```javascript
// Send ping every 30 seconds
setInterval(() => {
    if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
            type: "request",
            action: "ping",
            timestamp: Date.now()
        }));
    }
}, 30000);
```

## 🚨 Error Handling

### Common Error Messages
- `"Missing 'stocks' parameter"`: Basic WebSocket requires stocks parameter
- `"Symbol INVALID not found in stock database"`: Unknown stock symbol
- `"No stocks provided for subscription"`: Empty stocks array
- `"Unknown action: invalid_action"`: Invalid request action

### Error Response Format
```json
{
  "type": "error",
  "status": "error",
  "message": "Detailed error description",
  "client_id": "client_1704794400123",
  "timestamp": 1704794400000
}
```

## 📈 Rate Limits & Performance

### Connection Limits
- **Max Connections per IP**: 10 (configurable)
- **Max Subscriptions per Client**: 100 stocks
- **Message Rate Limit**: 10 messages/second per client

### Performance Optimization
- Use Enhanced WebSocket for better performance
- Subscribe only to needed stocks
- Implement proper reconnection logic
- Handle market hours appropriately

## 🔧 Configuration

### Server Configuration
```bash
# Environment variables
WEBSOCKET_PORT=8080
WEBSOCKET_MAX_CONNECTIONS=1000
WEBSOCKET_RATE_LIMIT=10
WEBSOCKET_HEARTBEAT_INTERVAL=30
```

### Client Configuration
```javascript
const config = {
    serverUrl: 'ws://localhost:8080/enhanced-stream',
    clientId: 'my_trading_app_v1',
    reconnectAttempts: 5,
    heartbeatInterval: 30000,
    subscriptions: ['RELIANCE', 'TCS', 'INFY']
};
```

## 📝 Best Practices

### 1. Connection Management
- Use unique client IDs for session persistence
- Implement exponential backoff for reconnections
- Handle connection state changes gracefully

### 2. Subscription Management
- Subscribe only to actively needed stocks
- Unsubscribe from unused stocks to save bandwidth
- Use batch subscribe/unsubscribe operations

### 3. Data Processing
- Process messages asynchronously
- Implement proper error handling
- Cache historical data locally when possible

### 4. Market Hours Awareness
- Check market status before expecting real-time data
- Handle market open/close notifications
- Adjust UI/behavior based on market hours

## 🧪 Testing

### Test WebSocket Connection
```bash
# Using wscat (install: npm install -g wscat)
wscat -c "ws://localhost:8080/enhanced-stream?client_id=test_client"

# Send subscribe message
{"type": "request", "action": "subscribe", "stocks": ["RELIANCE"]}

# Send ping
{"type": "request", "action": "ping"}
```

### Test with cURL (HTTP Upgrade)
```bash
curl -i -N -H "Connection: Upgrade" \
     -H "Upgrade: websocket" \
     -H "Sec-WebSocket-Version: 13" \
     -H "Sec-WebSocket-Key: x3JJHMbDL1EzLkh9GBhXDw==" \
     http://localhost:8080/enhanced-stream?client_id=curl_test
```

## 📚 Integration Examples

See the following files for complete integration examples:
- `javascript_client.html` - Browser JavaScript client
- `python_client.py` - Python WebSocket client
- `nodejs_client.js` - Node.js client
- `go_client.go` - Go client implementation

---

**API Version**: 2.2  
**Last Updated**: January 2025  
**Support**: Check integration examples and test clients
