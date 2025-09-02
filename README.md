# Odin Streamer - Candlestick Extension

A production-ready Go service that extends the existing Odin Streamer with real-time candlestick generation, synthetic candle creation, and comprehensive intraday data APIs.

## 🚀 Features

- **Real-time Candle Generation**: Converts live ticks to 1-minute OHLCV candles with IST timezone support
- **Synthetic Candle Creation**: Fills missing minutes with synthetic candles using last known price
- **Historical Data Integration**: Fetches and fills gaps using IndiraTrade API with intelligent fill algorithm
- **Redis Caching**: High-performance caching with pub/sub for live updates
- **TimescaleDB Persistence**: Scalable time-series storage with hypertables
- **WebSocket Streaming**: Extended with `init_data` and live candle events
- **REST API**: Complete intraday data endpoints with metadata
- **Market Hours Aware**: Respects Indian market hours (09:15-15:30 IST)
- **Late Tick Handling**: Processes late ticks within 2-minute tolerance

## 📋 Prerequisites

- **Go 1.21+**
- **Redis 6.0+** (for caching and pub/sub)
- **TimescaleDB/PostgreSQL 12+** (for persistence)
- **Python 3.8+** (for existing B2C bridge)

## 🛠️ Quick Setup

### 1. Clone and Setup

```bash
# Navigate to your existing odin-streamer directory
cd odin-streamer

# Install Go dependencies
go mod tidy

# Copy environment configuration
cp .env.example .env

# Edit .env with your specific values
nano .env
```

### 2. Database Setup

#### Redis Setup
```bash
# Install Redis (Ubuntu/Debian)
sudo apt update
sudo apt install redis-server

# Start Redis
sudo systemctl start redis-server
sudo systemctl enable redis-server

# Test Redis connection
redis-cli ping
```

#### TimescaleDB Setup
```bash
# Install TimescaleDB (Ubuntu/Debian)
sudo apt install postgresql-12 postgresql-client-12
sudo apt install timescaledb-postgresql-12

# Create database
sudo -u postgres createdb odin_candles

# Enable TimescaleDB extension
sudo -u postgres psql odin_candles -c "CREATE EXTENSION IF NOT EXISTS timescaledb;"

# Create user (optional)
sudo -u postgres psql -c "CREATE USER odin_user WITH PASSWORD 'your_password';"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE odin_candles TO odin_user;"
```

### 3. Environment Configuration

Update your `.env` file with the provided values:

```bash
# Basic Configuration
FORCE_REFRESH=false
API_BASE_URL=https://uatdev.indiratrade.com/companies-details
PYTHON_SCRIPT=b2c_bridge.py
WS_PORT=8081
STOCK_DB=stocks.db
HISTORICAL_DB=historical.db
HISTORICAL_API_URL=https://trading.indiratrade.com:3000

# Database URLs
TIMESCALE_URL=postgres://postgres:password@localhost:5432/odin_candles?sslmode=disable
REDIS_URL=redis://localhost:6379/0
```

### 4. Run the Service

```bash
# Build and run
go build -o odin-streamer .
./odin-streamer

# Or run directly
go run main.go
```

## 📡 API Endpoints

### REST API

#### Intraday Candles
```bash
# Get full day candles for a token
GET /intraday/{exchange}/{scrip_token}

# Examples:
curl "http://localhost:8081/intraday/NSE/2475"
curl "http://localhost:8081/intraday/NSE/2475?date=2025-08-28"
curl "http://localhost:8081/intraday/NSE/2475?force_refresh=true"
```

#### Health & Stats
```bash
# Health check
GET /intraday/health

# API statistics
GET /intraday/stats

# WebSocket diagnostics
GET /api/websocket/diagnostics
```

### WebSocket API

#### Connection
```javascript
// Connect to WebSocket
const ws = new WebSocket('ws://localhost:8081/stream?stocks=RELIANCE,TCS');

// Handle messages
ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    
    switch(data.type) {
        case 'init_data':
            // Full day candles on connect
            console.log('Received init data:', data.candles);
            break;
            
        case 'candle:update':
            // Live candle update
            console.log('Candle update:', data.candle);
            break;
            
        case 'candle:close':
            // Finalized minute candle
            console.log('Candle closed:', data.candle);
            break;
            
        case 'candle:patch':
            // Late tick correction
            console.log('Candle patched:', data.candle);
            break;
    }
};
```

## 🏗️ Architecture

### Core Components

```
/cmd/streamer/           # Main server (extended)
/internal/tick/          # Tick normalization & deduplication
/internal/candle/        # Candle Engine (tick→candle aggregation)
/internal/historical/    # IndiraTrade client + fill algorithm
/internal/api/           # REST + WebSocket handlers
/internal/storage/       # Redis + TimescaleDB adapters
/migrations/             # TimescaleDB SQL migrations
```

### Data Flow

```
Raw Ticks → Tick Normalizer → Candle Engine → Storage Layer
                                     ↓
WebSocket Clients ← Redis Pub/Sub ← Live Updates
                                     ↓
REST API ← Redis Cache ← TimescaleDB ← Persistence
```

### Candle JSON Format

```json
{
  "exchange": "NSE_EQ",
  "scrip_token": "2475",
  "minute_ts": "2025-08-28T09:15:00+05:30",
  "open": 100.00,
  "high": 101.00,
  "low": 99.50,
  "close": 100.50,
  "volume": 1250,
  "source": "realtime|historical_api|synthetic"
}
```

## 🔧 Configuration

### Market Hours
- **Market Open**: 09:15 IST
- **Market Close**: 15:30 IST
- **Timezone**: Asia/Kolkata (IST)

### Redis Key Schema
- `candles:{exchange}:{scrip_token}:{YYYYMMDD}` → Full-day minute candles
- `intraday_latest:{exchange}:{scrip_token}` → Latest in-flight candle
- PubSub: `intraday_updates:{exchange}:{scrip_token}` → Live updates

### Performance Tuning

```bash
# Buffer Sizes (adjust based on load)
TICK_NORMALIZER_BUFFER=2000000
MARKET_DATA_BUFFER=5000000
WS_CLIENT_BUFFER_SIZE=500000

# Database Connections
TIMESCALE_MAX_CONNECTIONS=25
REDIS_POOL_SIZE=10
```

## 🧪 Testing

### Manual Testing

```bash
# Test with sparse tick data
curl "http://localhost:8081/intraday/NSE/2475?date=2025-08-28"

# Test WebSocket connection
wscat -c "ws://localhost:8081/stream?stocks=RELIANCE"

# Test health endpoints
curl "http://localhost:8081/intraday/health"
curl "http://localhost:8081/intraday/stats"
```

### Acceptance Criteria Verification

1. **Late Joiner Test**: Connect WebSocket at 13:31 IST
   - Should receive continuous 09:15→now candles
   - Missing minutes filled with synthetic candles

2. **No Trade Instrument**: Request data for low-volume stock
   - Should use previous-day close for synthetic candles
   - Volume=0 for synthetic minutes

3. **Live Updates**: Monitor WebSocket during market hours
   - Should receive `candle:update` for current minute
   - Should receive `candle:close` at minute boundaries

## 📊 Monitoring

### Health Checks
```bash
# Component health
curl http://localhost:8081/intraday/health

# Response:
{
  "status": "healthy",
  "components": {
    "redis": "healthy",
    "timescale": "healthy",
    "historical": "available"
  }
}
```

### Performance Metrics
```bash
# API statistics
curl http://localhost:8081/intraday/stats

# WebSocket diagnostics
curl http://localhost:8081/api/websocket/diagnostics
```

### Logs
```bash
# Monitor logs for candle generation
tail -f /var/log/odin-streamer.log | grep "Candle"

# Monitor performance
tail -f /var/log/odin-streamer.log | grep "Performance"
```

## 🚨 Troubleshooting

### Common Issues

#### 1. Redis Connection Failed
```bash
# Check Redis status
sudo systemctl status redis-server

# Test connection
redis-cli ping

# Check configuration
grep REDIS_URL .env
```

#### 2. TimescaleDB Connection Failed
```bash
# Check PostgreSQL status
sudo systemctl status postgresql

# Test connection
psql -h localhost -U postgres -d odin_candles -c "SELECT version();"

# Check TimescaleDB extension
psql -h localhost -U postgres -d odin_candles -c "SELECT * FROM pg_extension WHERE extname='timescaledb';"
```

#### 3. Missing Candles
```bash
# Check historical API connectivity
curl "https://trading.indiratrade.com:3000/v1/chart/data/NSE/2475/1/MIN/RELIANCE?from=2025-08-28%2009:15:00&to=2025-08-28%2015:30:00"

# Check fill algorithm logs
grep "Fill algorithm" /var/log/odin-streamer.log
```

#### 4. WebSocket Connection Issues
```bash
# Check WebSocket server
curl http://localhost:8081/health

# Test WebSocket connection
wscat -c "ws://localhost:8081/stream?stocks=RELIANCE"

# Check client diagnostics
curl http://localhost:8081/api/websocket/diagnostics
```

## 🔄 Deployment

### Production Checklist

- [ ] Update `.env` with production values
- [ ] Set up Redis cluster for high availability
- [ ] Configure TimescaleDB with proper retention policies
- [ ] Set up monitoring and alerting
- [ ] Configure log rotation
- [ ] Set up backup procedures
- [ ] Test failover scenarios

### Docker Deployment (Optional)

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o odin-streamer .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /app/odin-streamer .
COPY --from=builder /app/.env .
CMD ["./odin-streamer"]
```

## 📈 Performance Benchmarks

### Expected Performance
- **Tick Processing**: 100,000+ ticks/second
- **WebSocket Clients**: 1,000+ concurrent connections
- **API Response Time**: <50ms (cache hit), <500ms (cache miss)
- **Memory Usage**: ~200MB base + ~1MB per 1000 active tokens

### Scaling Recommendations
- **Horizontal**: Multiple instances with Redis pub/sub
- **Vertical**: Increase buffer sizes and connection pools
- **Database**: TimescaleDB sharding for >10M candles/day

## 🤝 Contributing

1. Follow existing code patterns and conventions
2. Add comprehensive tests for new features
3. Update documentation for API changes
4. Ensure IST timezone handling throughout
5. Test with sparse tick data scenarios

## 📄 License

This project extends the existing Odin Streamer codebase. Please refer to the original license terms.

---

**Built with ❤️ for Indian Stock Markets**

*Market Hours: 09:15 - 15:30 IST | Timezone: Asia/Kolkata*
