# Command Line Tools - Odin Streamer

This directory contains all command-line applications and utilities for the Odin Streamer project.

## 📁 Directory Structure

```
cmd/
├── 📁 streamer/                     # Main streaming application
│   ├── main.go                      # Enhanced main application with dynamic subscriptions
│   ├── .env                         # Environment configuration
│   ├── stocks.db                    # SQLite database (auto-generated)
│   └── streamer                     # Compiled binary
│
├── 📁 migrations/                   # Database migration utilities
│   └── migrate_add_cocode.go        # Add co_code field migration
│
├── 📁 tools/                        # Data management and utility tools
│   ├── 📁 data-management/          # Data processing tools
│   │   └── update_52week_data.go    # 52-week data update utility
│   └── 📁 database/                 # Database management tools
│       └── update_cocode.go         # Company code update utility
│
├── 📁 tests/                        # Integration and API tests
│   └── test_stocksemoji_integration.go # StocksEmoji API integration test
│
└── 📁 admin/                        # Administrative utilities (future use)
```

## 🚀 Main Application

### **Streamer** (`cmd/streamer/`)
The core streaming application with enhanced features:

**Features:**
- ✅ Dynamic WebSocket subscriptions (subscribe/unsubscribe without reconnection)
- ✅ Intelligent 52-week data management (preserves existing data)
- ✅ Real-time 52-week high/low detection and alerts
- ✅ Market hours control (9:15 AM - 3:30 PM IST)
- ✅ Persistent client connections with session management
- ✅ Multi-storage support (SQLite + Redis + TimescaleDB)

**Usage:**
```bash
cd cmd/streamer
./streamer
```

**WebSocket Endpoints:**
- `ws://localhost:8080/live-stream` - Dynamic subscription WebSocket
- `ws://localhost:8080/enhanced-stream` - Enhanced WebSocket with market hours
- `ws://localhost:8080/stream` - Legacy candle WebSocket

## 🔧 Tools

### **Data Management** (`cmd/tools/data-management/`)

#### **update_52week_data.go**
Updates 52-week high/low data for all stocks from StocksEmoji API.

**Usage:**
```bash
cd cmd/tools/data-management
go run update_52week_data.go
```

**Features:**
- Fetches 52-week data from StocksEmoji API
- Updates SQLite database with high/low values
- Batch processing with API rate limiting
- Progress tracking and error handling

### **Database Management** (`cmd/tools/database/`)

#### **update_cocode.go**
Updates company codes (co_code) for all stocks from the companies-details API.

**Usage:**
```bash
cd cmd/tools/database
go run update_cocode.go
```

**Features:**
- Fetches company details from IndiraTrade API
- Maps tokens to company codes
- Updates SQLite database with co_code values
- Required for StocksEmoji API integration

## 🗄️ Migrations

### **migrate_add_cocode.go**
Database migration utility to add co_code field to existing stock tables.

**Usage:**
```bash
cd cmd/migrations
go run migrate_add_cocode.go
```

**Purpose:**
- Adds co_code column to stock_subscriptions table
- Ensures backward compatibility with existing databases
- Safe to run multiple times (idempotent)

## 🧪 Tests

### **test_stocksemoji_integration.go**
Integration test for StocksEmoji API connectivity and data fetching.

**Usage:**
```bash
cd cmd/tests
go run test_stocksemoji_integration.go
```

**Tests:**
- API connectivity and authentication
- Data format validation
- Error handling and rate limiting
- Sample data fetching for verification

## 📋 Common Workflows

### **Initial Setup**
```bash
# 1. Fetch and store stock data
cd cmd/streamer && ./streamer  # Will auto-fetch if database is empty

# 2. Update company codes (if needed)
cd cmd/tools/database && go run update_cocode.go

# 3. Update 52-week data (if needed)
cd cmd/tools/data-management && go run update_52week_data.go
```

### **Regular Maintenance**
```bash
# Update 52-week data (weekly/monthly)
cd cmd/tools/data-management && go run update_52week_data.go

# Test API connectivity
cd cmd/tests && go run test_stocksemoji_integration.go
```

### **Development Testing**
```bash
# Start the streaming application
cd cmd/streamer && ./streamer

# In another terminal, test WebSocket connections
# Open web/test/dynamic_subscription_test.html in browser
# Connect to ws://localhost:8080/live-stream
```

## 🔧 Build Instructions

### **Build All Tools**
```bash
# Build main streamer
cd cmd/streamer && go build -o streamer main.go

# Build individual tools
cd cmd/tools/data-management && go build -o update_52week_data update_52week_data.go
cd cmd/tools/database && go build -o update_cocode update_cocode.go
cd cmd/migrations && go build -o migrate_add_cocode migrate_add_cocode.go
cd cmd/tests && go build -o test_stocksemoji_integration test_stocksemoji_integration.go
```

### **Cross-Platform Builds**
```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o streamer-linux main.go

# Windows
GOOS=windows GOARCH=amd64 go build -o streamer-windows.exe main.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o streamer-macos main.go
```

## 📊 Environment Variables

All tools use the following environment variables (defined in `.env`):

```bash
# Database
STOCK_DB=stocks.db

# APIs
API_BASE_URL=https://uatdev.indiratrade.com/companies-details
HISTORICAL_API_URL=https://trading.indiratrade.com:3000

# Storage
REDIS_URL=redis://localhost:6379/0
TIMESCALE_URL=postgres://odin_user:odin_password@localhost:5432/odin_streamer?sslmode=disable

# Server
WS_PORT=8080
PYTHON_SCRIPT=../../scripts/b2c_bridge.py
```

## 🎯 Architecture Integration

These command-line tools integrate with the main Odin Streamer architecture:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   cmd/tools/    │───▶│  internal/stock/ │───▶│   SQLite DB     │
│  (Data Updates) │    │   (Processing)   │    │  (Persistence)  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                                │
                                ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  cmd/streamer/  │───▶│  internal/api/   │───▶│  WebSocket      │
│ (Main Service)  │    │  (WebSocket)     │    │   Clients       │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

This organized structure provides clear separation of concerns and makes the codebase maintainable and scalable.
