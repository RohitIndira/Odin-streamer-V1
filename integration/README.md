# Odin Streamer WebSocket Integration Guide

This directory contains complete integration files for connecting to the Odin Streamer WebSocket API. External developers can use these files to integrate real-time market data streaming into their applications.

## 🚀 Quick Start

### 1. Basic WebSocket Connection
```bash
# Start the Odin Streamer server
cd /path/to/odin-streamer
go run cmd/streamer/main.go

# Server will be available at:
# - Basic WebSocket: ws://localhost:8080/stream
# - Enhanced WebSocket: ws://localhost:8080/enhanced-stream
```

### 2. Connection URLs

#### Basic WebSocket (Simple Stock List)
```
ws://localhost:8080/stream?stocks=RELIANCE,TCS,INFY
```

#### Enhanced WebSocket (Dynamic Subscriptions)
```
ws://localhost:8080/enhanced-stream?client_id=your_unique_client_id
```

## 📁 Integration Files

| File | Description | Language |
|------|-------------|----------|
| `websocket_api_docs.md` | Complete API documentation | Documentation |
| `javascript_client.html` | Browser-based JavaScript client | JavaScript |
| `python_client.py` | Python WebSocket client | Python |
| `nodejs_client.js` | Node.js WebSocket client | Node.js |
| `go_client.go` | Go WebSocket client | Go |
| `curl_examples.sh` | cURL examples for testing | Shell |
| `postman_collection.json` | Postman collection for API testing | JSON |

## 🔧 Features

### Basic WebSocket (`/stream`)
- ✅ Real-time candle data streaming
- ✅ Historical data on connection
- ✅ Market hours awareness
- ✅ Multiple stock subscriptions
- ✅ Ping/pong heartbeat

### Enhanced WebSocket (`/enhanced-stream`)
- ✅ All basic features
- ✅ Dynamic subscribe/unsubscribe
- ✅ Persistent client sessions
- ✅ Market status notifications
- ✅ Real-time 52-week high/low alerts
- ✅ Client reconnection support
- ✅ Advanced subscription management

## 📊 Market Data Types

### 1. Candle Data
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

### 2. 52-Week High/Low Alerts
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
    "date": "2025-01-09"
  },
  "timestamp": 1704794400000
}
```

## 🕐 Market Hours

- **Market Open**: 9:15 AM IST
- **Market Close**: 3:30 PM IST
- **Timezone**: Asia/Kolkata (UTC+5:30)
- **Data Streaming**: Only during market hours
- **Historical Data**: Available 24/7

## 🔐 Authentication

Currently, no authentication is required for WebSocket connections. This is suitable for development and testing environments.

For production deployments, consider implementing:
- API key authentication
- JWT token validation
- Rate limiting
- IP whitelisting

## 📈 Usage Examples

### Subscribe to Multiple Stocks
```javascript
// Enhanced WebSocket
const ws = new WebSocket('ws://localhost:8080/enhanced-stream?client_id=my_app');

ws.onopen = function() {
    // Subscribe to stocks
    ws.send(JSON.stringify({
        type: "request",
        action: "subscribe",
        stocks: ["RELIANCE", "TCS", "INFY", "HDFCBANK"]
    }));
};
```

### Handle Real-time Data
```javascript
ws.onmessage = function(event) {
    const message = JSON.parse(event.data);
    
    switch(message.type) {
        case 'market_data':
            console.log(`${message.symbol}: ${message.data.close}`);
            break;
        case '52week_alert':
            console.log(`🚨 ${message.symbol} hit new ${message.data.alert_type}!`);
            break;
        case 'market_opened':
            console.log('📈 Market is now open - streaming started');
            break;
    }
};
```

## 🛠️ Development Setup

### Prerequisites
- Go 1.21+
- Redis (optional, for caching)
- TimescaleDB (optional, for storage)

### Environment Variables
```bash
# Copy example environment file
cp .env.example .env

# Edit configuration
REDIS_URL=redis://localhost:6379
TIMESCALE_URL=postgres://user:pass@localhost:5432/market_data
HISTORICAL_API_URL=https://api.indira.trade
```

### Build and Run
```bash
# Build the server
go build -o odin-streamer cmd/streamer/main.go

# Run the server
./odin-streamer

# Or run directly
go run cmd/streamer/main.go
```

## 📞 Support

For integration support and questions:
- Check the API documentation: `websocket_api_docs.md`
- Review example clients in this directory
- Test with the provided HTML test client
- Use Postman collection for API testing

## 🔄 Version History

- **v1.0**: Basic WebSocket streaming
- **v2.0**: Enhanced WebSocket with dynamic subscriptions
- **v2.1**: Real-time 52-week high/low detection
- **v2.2**: Market hours control and client persistence

---

**Happy Trading! 📈**
