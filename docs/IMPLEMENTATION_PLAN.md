# Odin Streamer - Candlestick Extension Implementation Plan

## Current System Analysis
- ✅ **Existing**: WebSocket streaming, historical data fetching, SQLite database, market data processing
- ✅ **Existing**: Stock subscription management, intelligent filtering, live data broadcasting
- ❌ **Missing**: Real-time candlestick generation, Redis caching, TimescaleDB persistence, synthetic candles

## Implementation Priority Order

### Phase 1: Core Candle Engine (Priority 1)
- [ ] Create `/internal/candle` package with Candle Engine
- [ ] Implement tick-to-candle aggregation with IST timezone support
- [ ] Add synthetic candle generation for missing minutes
- [ ] Handle late tick processing (≤2 minutes)
- [ ] Implement minute boundary detection and finalization

### Phase 2: Historical Integration (Priority 2)
- [ ] Extend `/internal/historical` package for IndiraTrade client
- [ ] Implement fill algorithm for continuous minute series (09:15→now)
- [ ] Add previous-day close fetching for synthetic candles
- [ ] Handle market hours validation (09:15-15:30 IST)

### Phase 3: Storage Layer (Priority 3)
- [ ] Create `/internal/storage` package
- [ ] Add Redis adapter with pub/sub for live updates
- [ ] Add TimescaleDB adapter with hypertable migrations
- [ ] Implement Redis key schema for candle caching

### Phase 4: API Extensions (Priority 4)
- [ ] Add REST endpoint `GET /intraday/{exchange}/{scrip_token}`
- [ ] Extend WebSocket with `init_data` and live candle events
- [ ] Add `candle:update` and `candle:close` message types
- [ ] Implement client subscription to Redis pub/sub channels

### Phase 5: Configuration & Testing (Priority 5)
- [ ] Create `.env.example` with all required variables
- [ ] Add comprehensive logging and monitoring
- [ ] Test with sparse tick streams and late joiners
- [ ] Write developer README with setup instructions

## Technical Specifications

### Canonical Minute Candle JSON Format
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

### Redis Key Schema
- `candles:{exchange}:{scrip_token}:{YYYYMMDD}` → Full-day minute candles
- `intraday_latest:{exchange}:{scrip_token}` → Latest in-flight candle
- PubSub: `intraday_updates:{exchange}:{scrip_token}` → Live updates

### WebSocket Message Types
- `init_data` → Full day candles on connect
- `candle:update` → Current minute live update
- `candle:close` → Finalized minute candle
- `candle:patch` → Late tick correction (≤2 minutes)

### Market Hours & Timezone
- Market Open: 09:15 IST
- Market Close: 15:30 IST
- All timestamps in IST (Asia/Kolkata)
- Synthetic candles for missing minutes using last known price

## Integration Points

### Existing Components to Extend
1. **WebSocket Server** → Add init_data sending and candle message types
2. **Historical Manager** → Add fill algorithm and previous-day close fetching
3. **Market Service** → Add Candle Engine integration and Redis pub/sub
4. **Stock Database** → Add candle persistence queries

### New Components to Create
1. **Candle Engine** → Core tick aggregation and synthetic generation
2. **Redis Adapter** → Caching and pub/sub for live updates
3. **TimescaleDB Adapter** → Persistent candle storage with hypertables
4. **Fill Algorithm** → Historical→continuous series conversion

## File Structure
```
/cmd/streamer/           # Main server (existing, extend)
/internal/tick/          # Tick normalization (new)
/internal/candle/        # Candle Engine (new)
/internal/historical/    # IndiraTrade client (extend existing)
/internal/api/           # REST + WebSocket handlers (extend existing)
/internal/storage/       # Redis + TimescaleDB adapters (new)
/migrations/             # TimescaleDB SQL migrations (new)
.env.example            # Environment configuration (new)
```

## Dependencies to Add
- Redis client: `github.com/go-redis/redis/v8`
- TimescaleDB driver: `github.com/lib/pq`
- Time handling: Enhanced IST timezone support

## Success Criteria
- [ ] Client connecting at 13:31 IST receives continuous 09:15→now candles
- [ ] Missing trade minutes filled with synthetic candles (volume=0)
- [ ] Live updates show current minute progress + minute-close events
- [ ] Late ticks (≤2 min) update persisted candles correctly
- [ ] No-trade instruments use previous-day close for synthetic candles
- [ ] All candles persisted to TimescaleDB for audit/replay
- [ ] Subsequent client joins are fast (Redis cache hit)

## Implementation Notes
- Single-writer per token (goroutine per token) for MVP
- IST timezone handling throughout the system
- Robust error handling for API failures and network issues
- Comprehensive logging for production debugging
- Memory-efficient candle storage and transmission
