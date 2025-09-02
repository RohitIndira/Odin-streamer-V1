package candle

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Candle represents a minute-level OHLCV candle
type Candle struct {
	Exchange   string    `json:"exchange"`
	ScripToken string    `json:"scrip_token"`
	MinuteTS   time.Time `json:"minute_ts"`
	Open       float64   `json:"open"`
	High       float64   `json:"high"`
	Low        float64   `json:"low"`
	Close      float64   `json:"close"`
	Volume     int64     `json:"volume"`
	Source     string    `json:"source"` // "realtime", "historical_api", "synthetic"
}

// Tick represents incoming market data tick
type Tick struct {
	Exchange   string    `json:"exchange"`
	ScripToken string    `json:"scrip_token"`
	Symbol     string    `json:"symbol"`
	Price      float64   `json:"price"`
	Volume     int64     `json:"volume"`
	Timestamp  time.Time `json:"timestamp"`
}

// CandleUpdate represents a live candle update event
type CandleUpdate struct {
	Type   string `json:"type"` // "update", "close", "patch"
	Candle Candle `json:"candle"`
}

// CandleEngine manages real-time candle generation from ticks
type CandleEngine struct {
	// IST timezone for market hours
	istLocation *time.Location

	// Per-token candle state (single writer per token)
	tokenStates map[string]*TokenCandleState
	mutex       sync.RWMutex

	// Market hours configuration
	marketOpen  time.Duration // 09:15 IST as duration from midnight
	marketClose time.Duration // 15:30 IST as duration from midnight

	// Channels for publishing candle events
	updateChannel chan CandleUpdate
	closeChannel  chan CandleUpdate
	patchChannel  chan CandleUpdate

	// Configuration
	lateTolerance time.Duration // Maximum age for late tick processing (default: 2 minutes)

	// Callbacks for persistence
	persistCallback func(candle Candle) error
	publishCallback func(update CandleUpdate) error
}

// TokenCandleState maintains state for a single token's candle generation
type TokenCandleState struct {
	exchange       string
	scripToken     string
	symbol         string
	currentMinute  time.Time
	currentCandle  *Candle
	lastKnownPrice float64
	lastTickTime   time.Time
	mutex          sync.Mutex
}

// NewCandleEngine creates a new candle engine with IST timezone support
func NewCandleEngine() (*CandleEngine, error) {
	// Load IST timezone
	istLocation, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	engine := &CandleEngine{
		istLocation:   istLocation,
		tokenStates:   make(map[string]*TokenCandleState),
		marketOpen:    9*time.Hour + 15*time.Minute,  // 09:15 IST
		marketClose:   15*time.Hour + 30*time.Minute, // 15:30 IST
		updateChannel: make(chan CandleUpdate, 10000),
		closeChannel:  make(chan CandleUpdate, 10000),
		patchChannel:  make(chan CandleUpdate, 10000),
		lateTolerance: 2 * time.Minute,
	}

	// Start minute boundary timer
	go engine.minuteBoundaryTimer()

	log.Printf("✅ Candle Engine initialized with IST timezone (market: 09:15-15:30)")
	return engine, nil
}

// ProcessTick processes an incoming tick and updates candles
func (ce *CandleEngine) ProcessTick(tick Tick) error {
	// Convert tick timestamp to IST
	tickTimeIST := tick.Timestamp.In(ce.istLocation)

	// Get minute start (floor to minute boundary)
	minuteStart := tickTimeIST.Truncate(time.Minute)

	// Check if tick is within market hours
	if !ce.isMarketHours(tickTimeIST) {
		log.Printf("⚠️ Tick outside market hours for %s at %s", tick.ScripToken, tickTimeIST.Format("15:04:05"))
		return nil
	}

	// Get or create token state
	state := ce.getOrCreateTokenState(tick.Exchange, tick.ScripToken, tick.Symbol)

	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Check if this is a late tick
	now := time.Now().In(ce.istLocation)
	tickAge := now.Sub(tickTimeIST)

	if tickAge > ce.lateTolerance {
		log.Printf("⚠️ Ignoring late tick for %s: %v old (tolerance: %v)",
			tick.ScripToken, tickAge, ce.lateTolerance)
		return nil
	}

	// Handle minute boundary crossing
	if state.currentMinute.IsZero() || !minuteStart.Equal(state.currentMinute) {
		// Finalize previous minute if exists
		if state.currentCandle != nil {
			ce.finalizeCandle(state)
		}

		// Fill any gap minutes with synthetic candles
		if !state.currentMinute.IsZero() {
			ce.fillGapMinutes(state, state.currentMinute.Add(time.Minute), minuteStart)
		}

		// Start new minute
		ce.startNewMinute(state, minuteStart, tick.Price)
	}

	// Update current candle with tick data
	ce.updateCurrentCandle(state, tick)

	// Publish live update
	update := CandleUpdate{
		Type:   "update",
		Candle: *state.currentCandle,
	}

	select {
	case ce.updateChannel <- update:
	default:
		log.Printf("⚠️ Update channel full for %s", tick.ScripToken)
	}

	// Execute publish callback if set
	if ce.publishCallback != nil {
		if err := ce.publishCallback(update); err != nil {
			log.Printf("⚠️ Publish callback error for %s: %v", tick.ScripToken, err)
		}
	}

	return nil
}

// getOrCreateTokenState gets or creates token candle state
func (ce *CandleEngine) getOrCreateTokenState(exchange, scripToken, symbol string) *TokenCandleState {
	ce.mutex.Lock()
	defer ce.mutex.Unlock()

	state, exists := ce.tokenStates[scripToken]
	if !exists {
		state = &TokenCandleState{
			exchange:   exchange,
			scripToken: scripToken,
			symbol:     symbol,
		}
		ce.tokenStates[scripToken] = state
		log.Printf("📊 Created new candle state for %s (%s)", symbol, scripToken)
	}

	return state
}

// startNewMinute initializes a new minute candle
func (ce *CandleEngine) startNewMinute(state *TokenCandleState, minuteStart time.Time, openPrice float64) {
	state.currentMinute = minuteStart
	state.currentCandle = &Candle{
		Exchange:   state.exchange,
		ScripToken: state.scripToken,
		MinuteTS:   minuteStart,
		Open:       openPrice,
		High:       openPrice,
		Low:        openPrice,
		Close:      openPrice,
		Volume:     0,
		Source:     "realtime",
	}

	log.Printf("🕐 Started new minute candle for %s at %s (open: %.2f)",
		state.symbol, minuteStart.Format("15:04"), openPrice)
}

// updateCurrentCandle updates the current candle with tick data
func (ce *CandleEngine) updateCurrentCandle(state *TokenCandleState, tick Tick) {
	candle := state.currentCandle

	// Update OHLC
	if tick.Price > candle.High {
		candle.High = tick.Price
	}
	if tick.Price < candle.Low {
		candle.Low = tick.Price
	}
	candle.Close = tick.Price
	candle.Volume += tick.Volume

	// Update state
	state.lastKnownPrice = tick.Price
	state.lastTickTime = tick.Timestamp
}

// finalizeCandle finalizes and persists a completed candle
func (ce *CandleEngine) finalizeCandle(state *TokenCandleState) {
	if state.currentCandle == nil {
		return
	}

	finalCandle := *state.currentCandle

	log.Printf("✅ Finalized candle for %s at %s: O=%.2f H=%.2f L=%.2f C=%.2f V=%d",
		state.symbol, finalCandle.MinuteTS.Format("15:04"),
		finalCandle.Open, finalCandle.High, finalCandle.Low, finalCandle.Close, finalCandle.Volume)

	// Persist candle
	if ce.persistCallback != nil {
		if err := ce.persistCallback(finalCandle); err != nil {
			log.Printf("❌ Persist callback error for %s: %v", state.scripToken, err)
		}
	}

	// Publish close event
	closeUpdate := CandleUpdate{
		Type:   "close",
		Candle: finalCandle,
	}

	select {
	case ce.closeChannel <- closeUpdate:
	default:
		log.Printf("⚠️ Close channel full for %s", state.scripToken)
	}

	// Execute publish callback
	if ce.publishCallback != nil {
		if err := ce.publishCallback(closeUpdate); err != nil {
			log.Printf("⚠️ Publish callback error for %s: %v", state.scripToken, err)
		}
	}

	// Clear current candle
	state.currentCandle = nil
}

// fillGapMinutes creates synthetic candles for missing minutes
func (ce *CandleEngine) fillGapMinutes(state *TokenCandleState, fromMinute, toMinute time.Time) {
	if state.lastKnownPrice <= 0 {
		log.Printf("⚠️ Cannot fill gap minutes for %s: no last known price", state.symbol)
		return
	}

	current := fromMinute
	gapCount := 0

	for current.Before(toMinute) {
		// Skip if outside market hours
		if !ce.isMarketHours(current) {
			current = current.Add(time.Minute)
			continue
		}

		syntheticCandle := Candle{
			Exchange:   state.exchange,
			ScripToken: state.scripToken,
			MinuteTS:   current,
			Open:       state.lastKnownPrice,
			High:       state.lastKnownPrice,
			Low:        state.lastKnownPrice,
			Close:      state.lastKnownPrice,
			Volume:     0,
			Source:     "synthetic",
		}

		log.Printf("🔄 Created synthetic candle for %s at %s (price: %.2f)",
			state.symbol, current.Format("15:04"), state.lastKnownPrice)

		// Persist synthetic candle
		if ce.persistCallback != nil {
			if err := ce.persistCallback(syntheticCandle); err != nil {
				log.Printf("❌ Persist synthetic candle error for %s: %v", state.scripToken, err)
			}
		}

		// Publish synthetic candle close event
		closeUpdate := CandleUpdate{
			Type:   "close",
			Candle: syntheticCandle,
		}

		if ce.publishCallback != nil {
			if err := ce.publishCallback(closeUpdate); err != nil {
				log.Printf("⚠️ Publish synthetic candle error for %s: %v", state.scripToken, err)
			}
		}

		current = current.Add(time.Minute)
		gapCount++
	}

	if gapCount > 0 {
		log.Printf("🔄 Filled %d gap minutes for %s with synthetic candles", gapCount, state.symbol)
	}
}

// minuteBoundaryTimer handles minute boundary events for all tokens
func (ce *CandleEngine) minuteBoundaryTimer() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().In(ce.istLocation)

		// Skip if outside market hours
		if !ce.isMarketHours(now) {
			continue
		}

		currentMinute := now.Truncate(time.Minute)

		ce.mutex.RLock()
		states := make([]*TokenCandleState, 0, len(ce.tokenStates))
		for _, state := range ce.tokenStates {
			states = append(states, state)
		}
		ce.mutex.RUnlock()

		// Process minute boundary for all tokens
		for _, state := range states {
			state.mutex.Lock()

			// If no current candle but we have last known price, create synthetic
			if state.currentCandle == nil && state.lastKnownPrice > 0 {
				prevMinute := currentMinute.Add(-time.Minute)
				if ce.isMarketHours(prevMinute) {
					syntheticCandle := Candle{
						Exchange:   state.exchange,
						ScripToken: state.scripToken,
						MinuteTS:   prevMinute,
						Open:       state.lastKnownPrice,
						High:       state.lastKnownPrice,
						Low:        state.lastKnownPrice,
						Close:      state.lastKnownPrice,
						Volume:     0,
						Source:     "synthetic",
					}

					log.Printf("🔄 Timer: Created synthetic candle for %s at %s",
						state.symbol, prevMinute.Format("15:04"))

					// Persist and publish synthetic candle
					if ce.persistCallback != nil {
						ce.persistCallback(syntheticCandle)
					}

					if ce.publishCallback != nil {
						closeUpdate := CandleUpdate{
							Type:   "close",
							Candle: syntheticCandle,
						}
						ce.publishCallback(closeUpdate)
					}
				}
			}

			state.mutex.Unlock()
		}
	}
}

// isMarketHours checks if the given time is within market hours (09:15-15:30 IST)
func (ce *CandleEngine) isMarketHours(t time.Time) bool {
	// Convert to IST if not already
	istTime := t.In(ce.istLocation)

	// Get time of day as duration from midnight
	timeOfDay := time.Duration(istTime.Hour())*time.Hour +
		time.Duration(istTime.Minute())*time.Minute +
		time.Duration(istTime.Second())*time.Second

	return timeOfDay >= ce.marketOpen && timeOfDay <= ce.marketClose
}

// SetPersistCallback sets the callback for persisting candles
func (ce *CandleEngine) SetPersistCallback(callback func(candle Candle) error) {
	ce.persistCallback = callback
}

// SetPublishCallback sets the callback for publishing candle updates
func (ce *CandleEngine) SetPublishCallback(callback func(update CandleUpdate) error) {
	ce.publishCallback = callback
}

// GetUpdateChannel returns the channel for candle updates
func (ce *CandleEngine) GetUpdateChannel() <-chan CandleUpdate {
	return ce.updateChannel
}

// GetCloseChannel returns the channel for candle close events
func (ce *CandleEngine) GetCloseChannel() <-chan CandleUpdate {
	return ce.closeChannel
}

// GetPatchChannel returns the channel for candle patch events
func (ce *CandleEngine) GetPatchChannel() <-chan CandleUpdate {
	return ce.patchChannel
}

// GetTokenStates returns current token states for monitoring
func (ce *CandleEngine) GetTokenStates() map[string]*TokenCandleState {
	ce.mutex.RLock()
	defer ce.mutex.RUnlock()

	// Return copy to avoid race conditions
	states := make(map[string]*TokenCandleState)
	for k, v := range ce.tokenStates {
		states[k] = v
	}
	return states
}

// GetStats returns engine statistics
func (ce *CandleEngine) GetStats() map[string]interface{} {
	ce.mutex.RLock()
	defer ce.mutex.RUnlock()

	activeTokens := 0
	tokensWithCandles := 0

	for _, state := range ce.tokenStates {
		activeTokens++
		if state.currentCandle != nil {
			tokensWithCandles++
		}
	}

	return map[string]interface{}{
		"active_tokens":       activeTokens,
		"tokens_with_candles": tokensWithCandles,
		"market_open":         ce.marketOpen.String(),
		"market_close":        ce.marketClose.String(),
		"late_tolerance":      ce.lateTolerance.String(),
		"timezone":            ce.istLocation.String(),
		"update_channel_size": len(ce.updateChannel),
		"close_channel_size":  len(ce.closeChannel),
		"patch_channel_size":  len(ce.patchChannel),
	}
}

// ProcessLateTick handles late ticks that arrive after candle finalization
func (ce *CandleEngine) ProcessLateTick(tick Tick) error {
	tickTimeIST := tick.Timestamp.In(ce.istLocation)
	now := time.Now().In(ce.istLocation)

	// Check if tick is within late tolerance
	if now.Sub(tickTimeIST) > ce.lateTolerance {
		return fmt.Errorf("tick too old: %v", now.Sub(tickTimeIST))
	}

	// Get minute start
	minuteStart := tickTimeIST.Truncate(time.Minute)

	// Create or update the historical candle
	patchCandle := Candle{
		Exchange:   tick.Exchange,
		ScripToken: tick.ScripToken,
		MinuteTS:   minuteStart,
		Open:       tick.Price, // This would need to be fetched from storage
		High:       tick.Price,
		Low:        tick.Price,
		Close:      tick.Price,
		Volume:     tick.Volume,
		Source:     "realtime",
	}

	log.Printf("🔧 Processing late tick for %s at %s (%.2f)",
		tick.ScripToken, minuteStart.Format("15:04"), tick.Price)

	// Publish patch event
	patchUpdate := CandleUpdate{
		Type:   "patch",
		Candle: patchCandle,
	}

	select {
	case ce.patchChannel <- patchUpdate:
	default:
		log.Printf("⚠️ Patch channel full for %s", tick.ScripToken)
	}

	// Execute callbacks
	if ce.persistCallback != nil {
		if err := ce.persistCallback(patchCandle); err != nil {
			log.Printf("❌ Persist patch error for %s: %v", tick.ScripToken, err)
		}
	}

	if ce.publishCallback != nil {
		if err := ce.publishCallback(patchUpdate); err != nil {
			log.Printf("⚠️ Publish patch error for %s: %v", tick.ScripToken, err)
		}
	}

	return nil
}

// Close shuts down the candle engine
func (ce *CandleEngine) Close() error {
	log.Printf("🛑 Shutting down Candle Engine")

	// Finalize all current candles
	ce.mutex.RLock()
	states := make([]*TokenCandleState, 0, len(ce.tokenStates))
	for _, state := range ce.tokenStates {
		states = append(states, state)
	}
	ce.mutex.RUnlock()

	for _, state := range states {
		state.mutex.Lock()
		if state.currentCandle != nil {
			ce.finalizeCandle(state)
		}
		state.mutex.Unlock()
	}

	// Close channels
	close(ce.updateChannel)
	close(ce.closeChannel)
	close(ce.patchChannel)

	log.Printf("✅ Candle Engine shutdown complete")
	return nil
}
