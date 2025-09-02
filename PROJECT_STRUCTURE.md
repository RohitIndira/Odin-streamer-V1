# Odin Streamer - Project Structure

## 📁 Directory Organization

```
odin-streamer/
├── 📁 cmd/                          # Command-line applications (see cmd/README.md)
│   ├── 📁 streamer/                 # Main streaming application
│   │   ├── main.go                  # Enhanced main application with dynamic subscriptions
│   │   ├── .env                     # Environment configuration
│   │   ├── stocks.db                # SQLite database (auto-generated)
│   │   └── streamer                 # Compiled binary
│   ├── 📁 migrations/               # Database migration utilities
│   │   └── migrate_add_cocode.go    # Add co_code field migration
│   ├── 📁 tools/                    # Data management and utility tools
│   │   ├── 📁 data-management/      # Data processing tools
│   │   │   └── update_52week_data.go # 52-week data update utility
│   │   └── 📁 database/             # Database management tools
│   │       └── update_cocode.go     # Company code update utility
│   ├── 📁 tests/                    # Integration and API tests
│   │   └── test_stocksemoji_integration.go # StocksEmoji API integration test
│   ├── 📁 admin/                    # Administrative utilities (future use)
│   └── 📄 README.md                 # Detailed cmd directory documentation
│
├── 📁 internal/                     # Internal Go packages
│   ├── 📁 api/                      # API handlers and WebSocket management
│   │   ├── enhanced_websocket.go   # Enhanced WebSocket with market hours control
│   │   ├── intraday.go             # Intraday data API
│   │   └── websocket.go            # Legacy WebSocket handler
│   ├── 📁 bridge/                   # Data processing bridge
│   │   └── streamer_bridge.go      # StreamerBridge for candle processing
│   ├── 📁 candle/                   # Candlestick processing
│   │   └── engine.go               # Candle generation engine
│   ├── 📁 historical/               # Historical data client
│   │   └── client.go               # IndiraTrade historical API client
│   ├── 📁 stock/                    # Stock data management
│   │   ├── api_fetch.go            # Stock data fetching and filtering
│   │   ├── database.go             # SQLite database operations
│   │   ├── stocksemoji_api.go      # StocksEmoji API integration
│   │   └── week52_manager.go       # 52-week high/low management
│   ├── 📁 storage/                  # Storage adapters
│   │   ├── redis.go                # Redis adapter
│   │   └── timescaledb.go          # TimescaleDB adapter
│   └── 📁 tick/                     # Tick data processing
│       └── normalizer.go           # Tick data normalization
│
├── 📁 scripts/                      # Scripts and automation
│   ├── b2c_bridge.py               # Python B2C API bridge
│   └── ecosystem.config.js         # PM2 configuration
│
├── 📁 web/                          # Web assets and test clients
│   └── 📁 test/                     # Test HTML clients
│       ├── dynamic_subscription_test.html # Dynamic subscription test client
│       └── test_enhanced_websocket.html   # Enhanced WebSocket test client
│
├── 📁 docs/                         # Documentation
│   ├── CODE_EXPLANATION.md         # Code architecture explanation
│   ├── COMPLETE_ARCHITECTURE_GUIDE.md # Complete system architecture
│   ├── DATABASE_STORAGE_EXPLANATION.md # Database design
│   ├── IMPLEMENTATION_PLAN.md      # Implementation roadmap
│   ├── TICK_PROCESSING_LOGIC_EXPLANATION.md # Tick processing logic
│   └── WEBSOCKET_CACHING_STRATEGY.md # WebSocket caching strategy
│
├── 📁 config/                       # Configuration files
│   └── cloud_config.json          # Cloud deployment configuration
│
├── 📁 b2c-api-python/              # Python B2C API library
│   └── [Python library files]     # External Python library
│
├── 📁 migrations/                   # Database migrations
│   └── [Migration files]          # Database schema migrations
│
├── 📄 go.mod                        # Go module definition
├── 📄 go.sum                        # Go module checksums
├── 📄 .env                          # Environment variables
├── 📄 .env.example                  # Environment template
├── 📄 .gitignore                    # Git ignore rules
├── 📄 README.md                     # Project documentation
└── 📄 PROJECT_STRUCTURE.md          # This file
```

## 🚀 Key Features

### ✅ Enhanced WebSocket Streaming
- **Dynamic Subscription Management**: Subscribe/unsubscribe to stocks without reconnection
- **Market Hours Control**: Automatic streaming during market hours (9:15 AM - 3:30 PM IST)
- **Persistent Connections**: Client session management with heartbeat
- **Real-time 52-week High/Low Detection**: Live alerts for new records

### ✅ Intelligent Data Management
- **Smart 52-week Data Updates**: Only downloads missing data, preserves existing
- **SQLite Database**: Fast local storage with in-memory caching
- **Token-Symbol Mapping**: Instant lookups for efficient processing
- **Persistent Data**: Survives application restarts

### ✅ Complete Data Pipeline
- **Python B2C Bridge**: Live market data from B2C API
- **StreamerBridge**: Processes ticks into candles
- **Multi-storage Support**: Redis + TimescaleDB + SQLite
- **Historical Data API**: IndiraTrade integration

## 🔧 Usage

### Start the Application
```bash
cd cmd/streamer
./streamer
```

### Test Dynamic Subscriptions
1. Open `web/test/dynamic_subscription_test.html` in browser
2. Connect to `ws://localhost:8080/live-stream`
3. Subscribe to stocks dynamically: `AERO`, `RELIANCE`, `TCS`, etc.
4. Watch real-time market data with 52-week alerts

### WebSocket API Examples

#### Subscribe to Single Stock
```javascript
ws.send(JSON.stringify({
    type: "subscribe",
    symbol: "AERO"
}));
```

#### Subscribe to Multiple Stocks
```javascript
ws.send(JSON.stringify({
    type: "subscribe",
    stocks: ["RELIANCE", "TCS", "INFY"]
}));
```

#### Unsubscribe
```javascript
ws.send(JSON.stringify({
    type: "unsubscribe",
    symbol: "AERO"
}));
```

#### List Current Subscriptions
```javascript
ws.send(JSON.stringify({
    type: "list_subscriptions"
}));
```

## 📊 API Endpoints

- `ws://localhost:8080/live-stream` - Dynamic subscription WebSocket
- `ws://localhost:8080/enhanced-stream` - Enhanced WebSocket with market hours
- `ws://localhost:8080/stream` - Legacy candle WebSocket
- `GET /api/health` - Health check
- `GET /api/stocks/stats` - Stock database statistics
- `GET /api/52week/stats` - 52-week data coverage
- `GET /intraday/{exchange}/{token}` - Historical intraday data

## 🎯 Architecture Highlights

1. **Modular Design**: Clean separation of concerns with internal packages
2. **Scalable WebSocket**: Supports multiple concurrent clients with subscriptions
3. **Efficient Processing**: Only processes data for subscribed stocks
4. **Persistent Storage**: SQLite for reliability, Redis for caching
5. **Real-time Alerts**: Instant 52-week high/low notifications
6. **Market Hours Aware**: Automatic start/stop based on trading hours

## 🔄 Data Flow

```
B2C API → Python Bridge → Go Application → StreamerBridge → WebSocket Clients
                                      ↓
                              SQLite Database (52-week data)
                                      ↓
                              Redis Cache + TimescaleDB
```

## 🧪 Testing

- **Dynamic Subscription Test**: `web/test/dynamic_subscription_test.html`
- **Enhanced WebSocket Test**: `web/test/test_enhanced_websocket.html`
- **Command Line Tests**: Various utilities in `cmd/` directory

This structure provides a clean, maintainable, and scalable foundation for the Odin Streamer market data streaming system.
