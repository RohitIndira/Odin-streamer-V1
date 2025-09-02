package bridge

import (
	"fmt"
	"log"
	"sync"
	"time"

	"golang-market-service/internal/candle"
	"golang-market-service/internal/storage"
	"golang-market-service/internal/tick"
)

// MarketData represents the existing market data structure from main.go
type MarketData struct {
	Symbol        string  `json:"symbol"`
	Token         string  `json:"token"`
	LTP           float64 `json:"ltp"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Open          float64 `json:"open"`
	Close         float64 `json:"close"`
	Volume        int64   `json:"volume"`
	PercentChange float64 `json:"change"`
	Week52High    float64 `json:"week_52_high"`
	Week52Low     float64 `json:"week_52_low"`
	PrevClose     float64 `json:"prev_close"`
	AvgVolume5D   int64   `json:"avg_volume_5d"`
	Timestamp     int64   `json:"timestamp"`
}

// StreamerBridge connects the existing Golang streamer with the new candlestick system
type StreamerBridge struct {
	// Core components
	tickNormalizer *tick.TickNormalizer
	candleEngine   *candle.CandleEngine
	redisAdapter   *storage.RedisAdapter
	dbAdapter      *storage.TimescaleDBAdapter

	// Input channel from existing streamer
	marketDataChannel chan MarketData

	// Control
	isRunning bool
	stopChan  chan struct{}
	wg        sync.WaitGroup
	mutex     sync.RWMutex

	// Statistics
	totalProcessed   int64
	successfulTicks  int64
	failedTicks      int64
	duplicateTicks   int64
	candlesGenerated int64
	lastStatsTime    time.Time
	statsInterval    time.Duration

	// Configuration
	batchSize      int
	batchTimeout   time.Duration
	enableBatching bool
}

// NewStreamerBridge creates a new bridge between the existing streamer and candlestick system
func NewStreamerBridge(redisAdapter *storage.RedisAdapter, dbAdapter *storage.TimescaleDBAdapter) (*StreamerBridge, error) {
	// Initialize tick normalizer
	tickNormalizer, err := tick.NewTickNormalizer()
	if err != nil {
		return nil, fmt.Errorf("failed to create tick normalizer: %w", err)
	}

	// Initialize candle engine
	candleEngine, err := candle.NewCandleEngine()
	if err != nil {
		return nil, fmt.Errorf("failed to create candle engine: %w", err)
	}

	bridge := &StreamerBridge{
		tickNormalizer:    tickNormalizer,
		candleEngine:      candleEngine,
		redisAdapter:      redisAdapter,
		dbAdapter:         dbAdapter,
		marketDataChannel: make(chan MarketData, 100000), // Large buffer for high-frequency data
		stopChan:          make(chan struct{}),
		lastStatsTime:     time.Now(),
		statsInterval:     30 * time.Second,
		batchSize:         100,
		batchTimeout:      1 * time.Second,
		enableBatching:    false, // Start with single processing for reliability
	}

	// Set up candle engine callbacks
	bridge.setupCandleEngineCallbacks()

	log.Printf("✅ Streamer Bridge initialized with real-time tick processing")
	return bridge, nil
}

// setupCandleEngineCallbacks configures persistence and publishing callbacks
func (sb *StreamerBridge) setupCandleEngineCallbacks() {
	// Set persistence callback for TimescaleDB
	sb.candleEngine.SetPersistCallback(func(candle candle.Candle) error {
		if sb.dbAdapter != nil {
			return sb.dbAdapter.StoreCandle(candle)
		}
		return nil
	})

	// Set publish callback for Redis pub/sub
	sb.candleEngine.SetPublishCallback(func(update candle.CandleUpdate) error {
		if sb.redisAdapter != nil {
			return sb.redisAdapter.PublishCandleUpdate(update)
		}
		return nil
	})
}

// GetMarketDataChannel returns the channel for feeding market data from the existing streamer
func (sb *StreamerBridge) GetMarketDataChannel() chan<- MarketData {
	return sb.marketDataChannel
}

// Start begins processing market data from the existing streamer
func (sb *StreamerBridge) Start() error {
	sb.mutex.Lock()
	defer sb.mutex.Unlock()

	if sb.isRunning {
		return fmt.Errorf("bridge already running")
	}

	sb.isRunning = true
	sb.lastStatsTime = time.Now()

	log.Printf("🚀 Starting Streamer Bridge with real-time candle generation")

	// Start market data processing goroutine
	sb.wg.Add(1)
	go sb.processMarketDataLoop()

	// Start candle update monitoring
	sb.wg.Add(1)
	go sb.monitorCandleUpdates()

	// Start statistics reporting
	sb.wg.Add(1)
	go sb.statsReporter()

	log.Printf("✅ Streamer Bridge started successfully")
	return nil
}

// processMarketDataLoop processes incoming market data and converts to candles
func (sb *StreamerBridge) processMarketDataLoop() {
	defer sb.wg.Done()

	log.Printf("🔄 Market data processing loop started")

	if sb.enableBatching {
		sb.processBatched()
	} else {
		sb.processSingle()
	}

	log.Printf("🛑 Market data processing loop stopped")
}

// processSingle processes market data one by one (more reliable)
func (sb *StreamerBridge) processSingle() {
	for {
		select {
		case marketData := <-sb.marketDataChannel:
			sb.processMarketData(marketData)

		case <-sb.stopChan:
			return
		}
	}
}

// processBatched processes market data in batches (higher throughput)
func (sb *StreamerBridge) processBatched() {
	batch := make([]MarketData, 0, sb.batchSize)
	batchTimer := time.NewTimer(sb.batchTimeout)

	for {
		select {
		case marketData := <-sb.marketDataChannel:
			batch = append(batch, marketData)

			if len(batch) >= sb.batchSize {
				sb.processBatch(batch)
				batch = batch[:0] // Reset batch
				batchTimer.Reset(sb.batchTimeout)
			}

		case <-batchTimer.C:
			if len(batch) > 0 {
				sb.processBatch(batch)
				batch = batch[:0] // Reset batch
			}
			batchTimer.Reset(sb.batchTimeout)

		case <-sb.stopChan:
			// Process remaining batch before stopping
			if len(batch) > 0 {
				sb.processBatch(batch)
			}
			batchTimer.Stop()
			return
		}
	}
}

// processMarketData processes a single market data item
func (sb *StreamerBridge) processMarketData(marketData MarketData) {
	sb.totalProcessed++

	// Convert MarketData to candle.Tick using normalizer
	candleTick, err := sb.tickNormalizer.NormalizeMarketData(tick.MarketData{
		Symbol:        marketData.Symbol,
		Token:         marketData.Token,
		LTP:           marketData.LTP,
		High:          marketData.High,
		Low:           marketData.Low,
		Open:          marketData.Open,
		Close:         marketData.Close,
		Volume:        marketData.Volume,
		PercentChange: marketData.PercentChange,
		Week52High:    marketData.Week52High,
		Week52Low:     marketData.Week52Low,
		PrevClose:     marketData.PrevClose,
		AvgVolume5D:   marketData.AvgVolume5D,
		Timestamp:     marketData.Timestamp,
	})

	if err != nil {
		sb.failedTicks++
		if sb.failedTicks <= 10 || sb.failedTicks%100 == 0 {
			log.Printf("⚠️ Failed to normalize market data for %s: %v", marketData.Symbol, err)
		}
		return
	}

	if candleTick == nil {
		// Duplicate tick (filtered out by normalizer)
		sb.duplicateTicks++
		return
	}

	// Process tick through candle engine
	if err := sb.candleEngine.ProcessTick(*candleTick); err != nil {
		sb.failedTicks++
		log.Printf("⚠️ Failed to process tick for %s: %v", candleTick.Symbol, err)
		return
	}

	sb.successfulTicks++
}

// processBatch processes multiple market data items in batch
func (sb *StreamerBridge) processBatch(batch []MarketData) {
	// Convert batch to tick.MarketData format
	tickBatch := make([]tick.MarketData, len(batch))
	for i, marketData := range batch {
		tickBatch[i] = tick.MarketData{
			Symbol:        marketData.Symbol,
			Token:         marketData.Token,
			LTP:           marketData.LTP,
			High:          marketData.High,
			Low:           marketData.Low,
			Open:          marketData.Open,
			Close:         marketData.Close,
			Volume:        marketData.Volume,
			PercentChange: marketData.PercentChange,
			Week52High:    marketData.Week52High,
			Week52Low:     marketData.Week52Low,
			PrevClose:     marketData.PrevClose,
			AvgVolume5D:   marketData.AvgVolume5D,
			Timestamp:     marketData.Timestamp,
		}
	}

	// Process batch through normalizer
	candleTicks, errors := sb.tickNormalizer.ProcessBatch(tickBatch)

	sb.totalProcessed += int64(len(batch))
	sb.failedTicks += int64(len(errors))

	// Process each normalized tick through candle engine
	for _, candleTick := range candleTicks {
		if err := sb.candleEngine.ProcessTick(*candleTick); err != nil {
			sb.failedTicks++
			log.Printf("⚠️ Failed to process tick for %s: %v", candleTick.Symbol, err)
		} else {
			sb.successfulTicks++
		}
	}

	log.Printf("📊 Processed batch: %d items, %d successful, %d errors",
		len(batch), len(candleTicks), len(errors))
}

// monitorCandleUpdates monitors candle engine outputs
func (sb *StreamerBridge) monitorCandleUpdates() {
	defer sb.wg.Done()

	updateChan := sb.candleEngine.GetUpdateChannel()
	closeChan := sb.candleEngine.GetCloseChannel()
	patchChan := sb.candleEngine.GetPatchChannel()

	log.Printf("🔍 Candle update monitoring started")

	for {
		select {
		case update := <-updateChan:
			// Live candle update - could be sent to WebSocket clients
			log.Printf("📊 Live candle update: %s %s O=%.2f H=%.2f L=%.2f C=%.2f V=%d",
				update.Candle.ScripToken, update.Candle.MinuteTS.Format("15:04"),
				update.Candle.Open, update.Candle.High, update.Candle.Low,
				update.Candle.Close, update.Candle.Volume)

		case close := <-closeChan:
			// Finalized candle
			sb.candlesGenerated++
			log.Printf("✅ Candle closed: %s %s O=%.2f H=%.2f L=%.2f C=%.2f V=%d (%s)",
				close.Candle.ScripToken, close.Candle.MinuteTS.Format("15:04"),
				close.Candle.Open, close.Candle.High, close.Candle.Low,
				close.Candle.Close, close.Candle.Volume, close.Candle.Source)

		case patch := <-patchChan:
			// Late tick patch
			log.Printf("🔧 Candle patched: %s %s C=%.2f V=%d",
				patch.Candle.ScripToken, patch.Candle.MinuteTS.Format("15:04"),
				patch.Candle.Close, patch.Candle.Volume)

		case <-sb.stopChan:
			log.Printf("🛑 Candle update monitoring stopped")
			return
		}
	}
}

// statsReporter reports statistics periodically
func (sb *StreamerBridge) statsReporter() {
	defer sb.wg.Done()

	ticker := time.NewTicker(sb.statsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sb.reportStats()

		case <-sb.stopChan:
			sb.reportStats() // Final stats report
			return
		}
	}
}

// reportStats reports current statistics
func (sb *StreamerBridge) reportStats() {
	elapsed := time.Since(sb.lastStatsTime).Seconds()
	if elapsed == 0 {
		elapsed = 1 // Avoid division by zero
	}

	processingRate := float64(sb.totalProcessed) / elapsed
	successRate := float64(sb.successfulTicks) / float64(sb.totalProcessed) * 100

	log.Printf("📊 Streamer Bridge Statistics:")
	log.Printf("   Total Processed: %d", sb.totalProcessed)
	log.Printf("   Successful Ticks: %d (%.2f%%)", sb.successfulTicks, successRate)
	log.Printf("   Failed Ticks: %d", sb.failedTicks)
	log.Printf("   Duplicate Ticks: %d", sb.duplicateTicks)
	log.Printf("   Candles Generated: %d", sb.candlesGenerated)
	log.Printf("   Processing Rate: %.2f items/sec", processingRate)

	// Get normalizer stats
	normalizerStats := sb.tickNormalizer.GetStats()
	log.Printf("   Normalizer Success Rate: %s", normalizerStats["success_rate"])

	// Get candle engine stats
	engineStats := sb.candleEngine.GetStats()
	log.Printf("   Active Tokens: %d", engineStats["active_tokens"])
	log.Printf("   Tokens with Candles: %d", engineStats["tokens_with_candles"])

	sb.lastStatsTime = time.Now()
}

// Stop stops the bridge processing
func (sb *StreamerBridge) Stop() error {
	sb.mutex.Lock()
	defer sb.mutex.Unlock()

	if !sb.isRunning {
		return nil
	}

	log.Printf("🛑 Stopping Streamer Bridge")

	// Signal all goroutines to stop
	close(sb.stopChan)

	// Wait for all goroutines to finish
	sb.wg.Wait()

	// Close candle engine
	if err := sb.candleEngine.Close(); err != nil {
		log.Printf("⚠️ Error closing candle engine: %v", err)
	}

	sb.isRunning = false

	log.Printf("✅ Streamer Bridge stopped successfully")
	return nil
}

// GetStats returns current bridge statistics
func (sb *StreamerBridge) GetStats() map[string]interface{} {
	sb.mutex.RLock()
	defer sb.mutex.RUnlock()

	successRate := float64(0)
	if sb.totalProcessed > 0 {
		successRate = float64(sb.successfulTicks) / float64(sb.totalProcessed) * 100
	}

	return map[string]interface{}{
		"is_running":        sb.isRunning,
		"total_processed":   sb.totalProcessed,
		"successful_ticks":  sb.successfulTicks,
		"failed_ticks":      sb.failedTicks,
		"duplicate_ticks":   sb.duplicateTicks,
		"candles_generated": sb.candlesGenerated,
		"success_rate":      fmt.Sprintf("%.2f%%", successRate),
		"batch_size":        sb.batchSize,
		"enable_batching":   sb.enableBatching,
		"channel_buffer":    len(sb.marketDataChannel),
		"channel_capacity":  cap(sb.marketDataChannel),
		"normalizer_stats":  sb.tickNormalizer.GetStats(),
		"engine_stats":      sb.candleEngine.GetStats(),
	}
}

// EnableBatching enables batch processing for higher throughput
func (sb *StreamerBridge) EnableBatching(batchSize int, batchTimeout time.Duration) {
	sb.mutex.Lock()
	defer sb.mutex.Unlock()

	sb.enableBatching = true
	sb.batchSize = batchSize
	sb.batchTimeout = batchTimeout

	log.Printf("✅ Batch processing enabled: size=%d, timeout=%v", batchSize, batchTimeout)
}

// DisableBatching disables batch processing (more reliable)
func (sb *StreamerBridge) DisableBatching() {
	sb.mutex.Lock()
	defer sb.mutex.Unlock()

	sb.enableBatching = false
	log.Printf("✅ Batch processing disabled - using single item processing")
}

// GetCandleEngine returns the candle engine for direct access
func (sb *StreamerBridge) GetCandleEngine() *candle.CandleEngine {
	return sb.candleEngine
}

// GetTickNormalizer returns the tick normalizer for direct access
func (sb *StreamerBridge) GetTickNormalizer() *tick.TickNormalizer {
	return sb.tickNormalizer
}

// IsRunning returns whether the bridge is currently running
func (sb *StreamerBridge) IsRunning() bool {
	sb.mutex.RLock()
	defer sb.mutex.RUnlock()
	return sb.isRunning
}
