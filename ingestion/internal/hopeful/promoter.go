// Package hopeful implements the Hopeful sector promoter for the ingestion
// service. It evaluates signals against promotion criteria, maintains the
// live set of Hopeful tickers, and demotes stale entries after a 30-minute
// window.
//
// See ARCHITECTURE.md §4.3 — Hopeful promotion criteria.
// See ARCHITECTURE.md §4.4 — sympathy play subscription logic.
// See ARCHITECTURE.md §4.5 — Hopeful demotion rules.
package hopeful

import (
	"context"
	"log"
	"math"
	"os"
	"sync"
	"time"

	"momentum/ingestion/internal/types"
)

// ── Promotion criteria constants ──────────────────────────────────────────────
// All threshold values live here at package level — never hardcoded inside
// functions. Changing a threshold only requires editing this one block.
// See ARCHITECTURE.md §4.3.
const (
	MinPrice         = 20.0            // state.Price must be BELOW this (penny/micro-cap focus)
	MinZScore        = 3.0             // math.Abs(signal.Z) must be ABOVE this
	MinRelVol        = 5.0             // signal.RelVol must be ABOVE this
	MinChangePercent = 10.0            // math.Abs(signal.ChangePercent) must be ABOVE this
	DemotionWindow   = 30 * time.Minute // stale threshold — reset by RefreshHopeful on new signals
)

// ── Interfaces ────────────────────────────────────────────────────────────────

// WatchlistPromoter abstracts watchlist.Manager so the Promoter never imports
// the concrete watchlist package. Any type with PromoteToHopeful satisfies it.
// See WINDSURF.md Rule — interfaces used for all cross-package dependencies.
type WatchlistPromoter interface {
	PromoteToHopeful(ticker string)
}

// HopefulLogger abstracts Supabase event logging.
// Logging failures must never block or abort the promotion path — the caller
// logs the error and continues regardless.
type HopefulLogger interface {
	LogWatchlistEvent(ctx context.Context, ticker, action, reason string) error
}

// ── Promoter ──────────────────────────────────────────────────────────────────

// Promoter evaluates signals against promotion criteria, maintains the live
// Hopeful ticker set, and runs a background demotion goroutine.
// All exported methods are safe to call concurrently.
type Promoter struct {
	watchlist WatchlistPromoter
	supabase  HopefulLogger

	// hopeful maps each promoted ticker to the time it was last promoted or
	// refreshed. The demotion loop evicts entries older than DemotionWindow.
	// sync.RWMutex allows multiple concurrent readers (IsHopeful, GetHopefulTickers)
	// while serialising writes (Evaluate, Demote, RefreshHopeful).
	hopeful map[string]time.Time
	mu      sync.RWMutex

	// done is a signal-only channel closed by Stop() to broadcast shutdown
	// to all goroutines started by StartDemotionLoop.
	done chan struct{}

	logger *log.Logger
}

// NewPromoter initialises all fields. Does not start the demotion loop —
// call StartDemotionLoop() separately after wiring all dependencies.
func NewPromoter(watchlist WatchlistPromoter, supabase HopefulLogger) *Promoter {
	return &Promoter{
		watchlist: watchlist,
		supabase:  supabase,
		hopeful:   make(map[string]time.Time),
		done:      make(chan struct{}),
		logger:    log.New(os.Stderr, "[hopeful] ", log.LstdFlags),
	}
}

// ── Public methods ────────────────────────────────────────────────────────────

// Evaluate checks signal and state against all four promotion criteria.
// If all criteria pass, it promotes the ticker: updates state, records the
// promotion time, notifies the watchlist manager, and logs the event.
// Returns true on promotion, false on any criterion failure.
// Never panics — safe to call with zero-value inputs.
//
// See ARCHITECTURE.md §4.3 — promotion criteria and side effects.
func (p *Promoter) Evaluate(signal types.Signal, state *types.SymbolState) bool {
	// Delegate the pure criteria check to a private helper so that the
	// decision logic is independently testable without Promoter setup.
	if !meetsAllCriteria(signal, state) {
		return false
	}

	// All criteria passed — promote the ticker.

	// Mark the SymbolState so downstream consumers (Redis writer, API) know
	// this ticker is currently in the Hopeful sector.
	state.IsHopeful = true

	// Record the promotion timestamp under an exclusive write lock.
	// mu.Lock() blocks until all current RLock holders release.
	p.mu.Lock()
	p.hopeful[signal.Ticker] = time.Now()
	p.mu.Unlock()

	// Notify the watchlist manager to subscribe any sympathy peers.
	// This call happens outside the mutex — PromoteToHopeful acquires its
	// own internal lock and must not be called while p.mu is held to
	// avoid lock-ordering deadlocks.
	p.watchlist.PromoteToHopeful(signal.Ticker)

	// Log the event to Supabase. Errors are logged but never block promotion
	// — a Supabase outage must not prevent the live feed from working.
	ctx := context.Background()
	if err := p.supabase.LogWatchlistEvent(ctx, signal.Ticker, "hopeful_promoted", "criteria_met"); err != nil {
		p.logger.Printf("Evaluate: LogWatchlistEvent(%s): %v", signal.Ticker, err)
	}

	p.logger.Printf("Evaluate: PROMOTED %s — price=%.2f Z=%.2f RelVol=%.2f chg=%.2f%%",
		signal.Ticker, state.Price, signal.Z, signal.RelVol, signal.ChangePercent)

	return true
}

// IsHopeful returns true if ticker is currently in the Hopeful set.
// Reads under mu.RLock() — safe to call concurrently from any goroutine.
func (p *Promoter) IsHopeful(ticker string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.hopeful[ticker]
	return ok
}

// GetHopefulTickers returns a snapshot slice of all currently Hopeful tickers.
// Used by the Redis writer to rebuild the hopeful:tickers sorted set on each
// flush cycle. Reads under mu.RLock() — releases the lock before returning.
func (p *Promoter) GetHopefulTickers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tickers := make([]string, 0, len(p.hopeful))
	for t := range p.hopeful {
		tickers = append(tickers, t)
	}
	return tickers
}

// Demote removes ticker from the Hopeful set and logs the demotion event.
// Safe to call from the demotion loop or any external caller.
// Does not update state.IsHopeful — the caller (tickProcessor) is responsible
// for clearing that flag on the next tick if it still holds a reference.
func (p *Promoter) Demote(ticker string) {
	p.mu.Lock()
	delete(p.hopeful, ticker)
	p.mu.Unlock()

	// Log the demotion outside the lock — Supabase calls must not hold mu.
	ctx := context.Background()
	if err := p.supabase.LogWatchlistEvent(ctx, ticker, "demoted", "demotion_window_expired"); err != nil {
		p.logger.Printf("Demote: LogWatchlistEvent(%s): %v", ticker, err)
	}

	p.logger.Printf("Demote: %s removed from Hopeful set", ticker)
}

// RefreshHopeful resets the 30-minute demotion clock for ticker by updating
// its promotedAt timestamp to time.Now(). Called when a Hopeful stock fires
// another qualifying signal — keeps active movers in the Hopeful sector.
// No-op if ticker is not currently in the Hopeful map (avoids spurious
// re-promotion of a ticker that was already demoted this session).
func (p *Promoter) RefreshHopeful(ticker string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only refresh tickers that are already Hopeful — do not re-promote.
	if _, ok := p.hopeful[ticker]; !ok {
		return
	}
	p.hopeful[ticker] = time.Now()
}

// StartDemotionLoop launches a background goroutine that scans the Hopeful
// map every 5 minutes and demotes any ticker whose promotedAt timestamp is
// older than DemotionWindow (30 minutes without a new signal).
//
// Goroutine lifecycle: exits when Stop() closes the done channel.
// The snapshot pattern (read under RLock → collect stale list → release lock
// → Demote each) avoids holding a write lock during Supabase calls and
// prevents deadlock from Demote() acquiring mu.Lock() while mu is held.
func (p *Promoter) StartDemotionLoop() {
	// 'go' schedules this closure as an independent goroutine — it runs
	// concurrently with all other goroutines in the ingestion service.
	go func() {
		// time.NewTicker fires every 5 minutes. defer Stop() releases the
		// internal timer goroutine if this function returns early.
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			// select blocks until one channel is ready — whichever fires first.
			select {
			case <-ticker.C:
				p.checkAndDemoteStale()

			case <-p.done:
				// Stop() was called — exit the goroutine cleanly.
				return
			}
		}
	}()
}

// Stop closes the done channel, signalling the demotion goroutine to exit.
// Must be called at most once — closing a closed channel panics.
func (p *Promoter) Stop() {
	close(p.done)
}

// ── Private helpers ───────────────────────────────────────────────────────────

// meetsAllCriteria is a pure function — no side effects, no logging, no map
// writes. It checks the four promotion criteria in the exact order specified
// by ARCHITECTURE.md §4.3, returning false immediately on the first failure.
//
// Pure functions are trivially unit-testable: given the same inputs they always
// return the same output with no observable state change anywhere.
func meetsAllCriteria(signal types.Signal, state *types.SymbolState) bool {
	// Criterion 1: price must be below MinPrice (penny/micro-cap focus).
	// state may be nil in degenerate test inputs — guard against nil dereference.
	if state == nil || state.Price >= MinPrice {
		return false
	}

	// Criterion 2: Z-score magnitude must exceed MinZScore.
	// math.Abs handles both up-spikes (Z > 0) and crashes (Z < 0).
	if math.Abs(signal.Z) < MinZScore {
		return false
	}

	// Criterion 3: relative volume must exceed MinRelVol.
	// High RelVol confirms the move is driven by real order flow, not noise.
	if signal.RelVol < MinRelVol {
		return false
	}

	// Criterion 4: percent change from previous close must exceed MinChangePercent.
	// Absolute value catches both large up-moves and gap-downs.
	if math.Abs(signal.ChangePercent) < MinChangePercent {
		return false
	}

	return true
}

// checkAndDemoteStale collects stale Hopeful tickers under RLock, then
// demotes them outside the lock. Called exclusively by StartDemotionLoop.
func (p *Promoter) checkAndDemoteStale() {
	// Phase 1: collect stale tickers under RLock (read-only scan).
	// time.Since(t) == time.Now().Sub(t) — cleaner standard library idiom.
	p.mu.RLock()
	var stale []string
	for ticker, promotedAt := range p.hopeful {
		if time.Since(promotedAt) > DemotionWindow {
			stale = append(stale, ticker)
		}
	}
	p.mu.RUnlock()

	// Phase 2: demote each stale ticker outside the lock.
	// Demote() acquires mu.Lock() internally — calling it while holding
	// mu.RLock() would deadlock. The snapshot above makes this safe.
	for _, ticker := range stale {
		p.logger.Printf("checkAndDemoteStale: %s stale after %v", ticker, DemotionWindow)
		p.Demote(ticker)
	}
}
