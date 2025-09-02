# WebSocket Testing Commands for Odin Streamer

## 1. Test All Market Data (Live Stream)
```bash
# Connect to live stream for ALL stocks
wscat -c "ws://localhost:8080/live-stream"

# Connect with specific stocks filter
wscat -c "ws://localhost:8080/live-stream?stocks=RELIANCE,TCS,INFY"

# Connect with multiple stocks
wscat -c "ws://localhost:8080/live-stream?stocks=RELIANCE,DMART,HANMAN,WIPRO,HDFC"
```

## 2. Test Enhanced Stream (Advanced Features)
```bash
# Connect to enhanced stream
wscat -c "ws://localhost:8080/enhanced-stream"

# After connecting, send subscription messages:
# Subscribe to specific stocks
{"type": "subscribe", "stocks": ["RELIANCE", "TCS", "INFY"]}

# Subscribe to single stock
{"type": "subscribe", "symbol": "RELIANCE"}

# List current subscriptions
{"type": "list_subscriptions"}

# Unsubscribe from stocks
{"type": "unsubscribe", "stocks": ["TCS"]}
```

## 3. Test Original Candle Stream
```bash
# Connect to candle stream for specific stocks
wscat -c "ws://localhost:8080/stream?stocks=RELIANCE,TCS"
```

## 4. HTTP API Endpoints for Testing
```bash
# Check service health
curl http://localhost:8080/api/health

# Get stock statistics
curl http://localhost:8080/api/stocks/stats

# Get 52-week statistics
curl http://localhost:8080/api/52week/stats

# Get enhanced statistics
curl http://localhost:8080/api/enhanced/stats

# Root endpoint (service info)
curl http://localhost:8080/
```

## 5. Interactive WebSocket Testing

### Using wscat (install if needed):
```bash
# Install wscat if not available
npm install -g wscat

# Test live stream with all data
wscat -c "ws://localhost:8080/live-stream"

# Test with specific stocks
wscat -c "ws://localhost:8080/live-stream?stocks=RELIANCE,TCS,INFY,WIPRO,HDFC"
```

### Using websocat (alternative):
```bash
# Install websocat if needed
# wget https://github.com/vi/websocat/releases/download/v1.11.0/websocat.x86_64-unknown-linux-musl
# chmod +x websocat.x86_64-unknown-linux-musl
# sudo mv websocat.x86_64-unknown-linux-musl /usr/local/bin/websocat

# Test connection
websocat "ws://localhost:8080/live-stream"

# Test with stocks filter
websocat "ws://localhost:8080/live-stream?stocks=RELIANCE,TCS"
```

## 6. Expected WebSocket Message Types

### Live Stream Messages:
- `welcome` - Connection confirmation
- `live_market_data` - Real-time market data with 52-week info
- `new_52_week_high` - New 52-week high alert
- `new_52_week_low` - New 52-week low alert
- `new_52_week_high_low` - Both high and low records

### Enhanced Stream Messages:
- `subscribe_response` - Subscription confirmation
- `unsubscribe_response` - Unsubscription confirmation
- `subscription_list` - Current subscriptions
- `market_data` - Live market data
- `pong` - Response to ping

## 7. Sample JSON Messages for Enhanced Stream

### Subscribe to stocks:
```json
{"type": "subscribe", "stocks": ["RELIANCE", "TCS", "INFY"]}
```

### Subscribe to single stock:
```json
{"type": "subscribe", "symbol": "RELIANCE"}
```

### Ping server:
```json
{"type": "ping"}
```

### List subscriptions:
```json
{"type": "list_subscriptions"}
```

### Unsubscribe:
```json
{"type": "unsubscribe", "stocks": ["TCS"]}
```
