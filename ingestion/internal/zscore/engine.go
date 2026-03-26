// Package zscore implements the rolling Z-score signal detection engine for
// the ingestion service. It is purely computational — no I/O, no channels,
// no goroutines, no external dependencies beyond the standard library.
//
// All mutable state lives in the *types.SymbolState pointer owned by the
// caller (tickProcessor). The Engine struct itself is stateless.
//
// See ARCHITECTURE.md §5   — Z-score engine design.
// See ARCHITECTURE.md §5.1 — rolling statistics formula.
// See ARCHITECTURE.md §5.2 — signal tier thresholds.
package zscore

import (
	"math"
	"time"

	"momentum/ingestion/internal/types"
)

// Engine is a stateless Z-score computation engine.
// Because all mutable state lives in the *types.SymbolState pointer passed
// to ProcessTick, Engine has no fields, requires no mutex, and can be shared
// safely across goroutines without any synchronisation overhead.
type Engine struct{}

// NewEngine returns a new Engine. No initialisation is needed.
func NewEngine() *Engine {
	return &Engine{}
}

// ProcessTick is the single public entry point, called on every trade tick
// by the tickProcessor goroutine. It updates state in place and returns a
// Signal if a detection threshold is crossed.
//
// Steps execute in this exact order per ARCHITECTURE.md §5.1:
//
//	A  compute 1-minute return
//	B  write return into ring buffer
//	C  compute rolling mean + stddev (guards stddev == 0)
//	D  compute Z-score
//	E  compute relative volume (guards avgVolume == 0)
//	F  update price fields on state
//	G  evaluate signal thresholds (Strong → Moderate → Noise)
//
// Returns (Signal, true) when a threshold is crossed; (Signal{}, false) otherwise.
// Never panics — all zero-value inputs are handled explicitly.
func (e *Engine) ProcessTick(
	state *types.SymbolState,
	newPrice float64,
	newVolume int64,
	avgVolume int64,
) (types.Signal, bool) {

	// ── Step A: compute 1-minute return ──────────────────────────────────────
	// Return = (newPrice - prevPrice) / prevPrice.
	// On the very first tick ever for a symbol, state.Price is zero — we cannot
	// compute a meaningful return. Record the opening price and return early.
	if state.Price == 0 {
		state.Price = newPrice
		return types.Signal{}, false
	}

	ret := (newPrice - state.Price) / state.Price

	// ── Step B: update ring buffer ────────────────────────────────────────────
	// Window is a fixed-size [20]float64 circular buffer.
	// WindowIdx is never reset — modulo gives the current write slot.
	// When WindowIdx reaches 20, slot 0 is overwritten; the oldest return is lost.
	// This keeps exactly the last 20 one-minute returns in memory at all times.
	idx := state.WindowIdx % 20
	state.Window[idx] = ret
	state.WindowIdx++

	// ── Step C: compute rolling mean and standard deviation ──────────────────
	// rollingStats computes population stddev over all 20 window slots.
	// If stddev is 0, every slot holds the same value — the Z-score formula
	// would divide by zero. Update price fields and bail without a signal.
	mean, stddev := rollingStats(state.Window)

	if stddev == 0 {
		// Still update price so the state stays current even with no signal.
		state.Price = newPrice
		if state.PrevClose != 0 {
			state.ChangePercent = (newPrice - state.PrevClose) / state.PrevClose * 100
		}
		return types.Signal{}, false
	}

	// ── Step D: compute Z-score ───────────────────────────────────────────────
	// Z measures how many standard deviations the current return is from the
	// rolling mean. A large |Z| indicates a statistically unusual price move.
	// See ARCHITECTURE.md §5.1 — Z = (x - μ) / σ.
	z := (ret - mean) / stddev
	state.ZScore = z

	// ── Step E: compute relative volume ──────────────────────────────────────
	// RelVol = current period volume / 30-day average volume at this time of day.
	// Unusually high relative volume alongside a high |Z| confirms a real move
	// rather than a thin-market artefact. Guards against division by zero when
	// historical volume data is unavailable (avgVolume == 0).
	if avgVolume == 0 {
		state.RelVol = 0
	} else {
		state.RelVol = float64(newVolume) / float64(avgVolume)
	}

	// ── Step F: update price fields on state ─────────────────────────────────
	// Update Price and ChangePercent after computing the return (which used the
	// old price). PrevClose is set externally by the watchlist loader on market
	// open — guard against zero to avoid division by zero.
	state.Price = newPrice
	if state.PrevClose == 0 {
		state.ChangePercent = 0
	} else {
		state.ChangePercent = (newPrice - state.PrevClose) / state.PrevClose * 100
	}

	// ── Step G: evaluate signal thresholds ───────────────────────────────────
	// math.Abs(z) detects both upward spikes (z > 0) and crashes (z < 0).
	// Strong is checked before Moderate — first match wins.
	// See ARCHITECTURE.md §5.2 — signal tier thresholds.
	absZ := math.Abs(z)

	// Strong tier: statistically extreme move with very high volume confirmation.
	if absZ > 3.0 && state.RelVol > 4.0 {
		return types.Signal{
			Ticker:        state.Ticker,
			Z:             z,
			RelVol:        state.RelVol,
			Sector:        state.Sector,
			Price:         newPrice,
			ChangePercent: state.ChangePercent,
			IsHopeful:     state.IsHopeful,
			DetectedAt:    time.Now(),
		}, true
	}

	// Moderate tier: notable move with meaningful volume confirmation.
	if absZ > 2.5 && state.RelVol > 2.0 {
		return types.Signal{
			Ticker:        state.Ticker,
			Z:             z,
			RelVol:        state.RelVol,
			Sector:        state.Sector,
			Price:         newPrice,
			ChangePercent: state.ChangePercent,
			IsHopeful:     state.IsHopeful,
			DetectedAt:    time.Now(),
		}, true
	}

	// Noise: Z too small or volume not confirming — discard.
	return types.Signal{}, false
}

// rollingStats computes the mean and population standard deviation of the
// fixed-size [20]float64 ring buffer stored in SymbolState.Window.
//
// Population stddev (not sample / Bessel's correction) is used because
// we always evaluate all 20 values — there is no sampling uncertainty.
// Formula: σ = sqrt( Σ(xᵢ - μ)² / N )
//
// See ARCHITECTURE.md §5.1 — rolling statistics.
func rollingStats(window [20]float64) (mean float64, stddev float64) {
	const n = 20

	// Pass 1: compute mean.
	var sum float64
	for _, v := range window {
		sum += v
	}
	mean = sum / n

	// Pass 2: compute variance as the average squared deviation from the mean.
	var variance float64
	for _, v := range window {
		d := v - mean
		variance += d * d
	}
	variance /= n

	// math.Sqrt of a non-negative value — safe; variance is always ≥ 0.
	stddev = math.Sqrt(variance)
	return mean, stddev
}
