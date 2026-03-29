// manager.go — core watchlist logic: seed list, screener merge, diff algorithm,
// and market-hours refresh cron. No direct Alpaca or Supabase imports —
// both are accessed through interfaces so Manager is fully testable with mocks.
//
// See ARCHITECTURE.md §3.2  — watchlistRefresher goroutine.
// See ARCHITECTURE.md §4.1  — watchlist seeding and refresh rules.
// See WINDSURF.md Rule 1    — never block the tickProcessor hot path.
package watchlist

import (
	"context"
	"log"
	"os"
	"sync"
	"time"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

// AlpacaSubscriber abstracts the WebSocket client so Manager never imports
// the concrete alpaca package. This is the standard Go interface idiom:
// define the interface where it is consumed, not where it is implemented.
// Any type that has Subscribe and Unsubscribe methods satisfies this interface.
type AlpacaSubscriber interface {
	Subscribe(tickers []string) error
	Unsubscribe(tickers []string) error
}

// SupabaseWriter abstracts Supabase upserts for avg_volumes persistence.
// Keeping this as an interface means Manager can be unit-tested with a
// no-op mock without spinning up a real Supabase connection.
type SupabaseWriter interface {
	UpsertAvgVolume(ctx context.Context, ticker string, avgVol int64) error
}

// RedisWriter abstracts Redis writes so Manager never imports the concrete
// redis package. Used to persist the active ticker set to Redis.
type RedisWriter interface {
	SetWatchlist(tickers []string) error
}

// maxWatchlistSize is the hard cap on subscribed symbols.
// Alpaca's free IEX WebSocket tier allows at most 30 concurrent subscriptions.
const maxWatchlistSize = 30

// ── Seed list ─────────────────────────────────────────────────────────────────

// seedTickers is the static baseline watchlist subscribed on every startup.
// These are high-volatility or Hopeful-sector tickers that should always be
// monitored regardless of what the screener returns.
// See ARCHITECTURE.md §4.1 — static seed plus dynamic screener merge.
var seedTickers = []string{
	"MARA", "RIOT", "CLSK", "BTBT", "COIN",
	"PLTR", "SOFI", "RIVN", "LCID", "BBIO",
	"SAVA", "AGEN", "HIMS", "ARQT", "PRTA",
	"AMTX", "IMRX", "EONR", "GEVO", "REGI",
}

// ── Manager ───────────────────────────────────────────────────────────────────

// Manager owns the active ticker watchlist and drives Alpaca subscriptions.
// All exported methods are safe to call concurrently.
type Manager struct {
	screener *ScreenerClient
	// alpaca and supabase are interfaces — Manager never references concrete types.
	// This keeps the dependency graph clean and enables mock testing.
	alpaca      AlpacaSubscriber
	supabase    SupabaseWriter
	redisWriter RedisWriter

	// active is the current set of subscribed tickers.
	// sync.RWMutex allows multiple concurrent RLock readers (tickProcessor
	// calling GetAvgVolume) or one exclusive Lock writer (build, PromoteToHopeful).
	active map[string]bool
	mu     sync.RWMutex

	// avgVolumes caches the 30-day average daily volume for each known ticker.
	// Populated lazily by build() — once fetched, never re-fetched (stable data).
	// Protected by a separate mutex so GetAvgVolume (called per-tick) does not
	// contend with build() writes on the main mu.
	avgVolumes map[string]int64
	avgMu      sync.RWMutex

	// hopeful tracks tickers that have been promoted to the Hopeful sector.
	// Protected by hopefulMu.
	hopeful   map[string]bool
	hopefulMu sync.RWMutex

	// sympathyPeers maps each sympathy peer ticker to its Hopeful leader.
	// Populated by PromoteToHopeful, read by GetSympathyParent.
	// Protected by mu (same lock as active map — always updated together).
	sympathyPeers map[string]string

	// done is a signal-only channel closed by Stop() to unblock all goroutines.
	done chan struct{}

	logger *log.Logger
}

// NewManager initialises all maps and channels. Call Start() to begin
// refreshing; NewManager itself performs no I/O.
func NewManager(screener *ScreenerClient, alpaca AlpacaSubscriber, supabase SupabaseWriter, redisWriter RedisWriter) *Manager {
	return &Manager{
		screener:      screener,
		alpaca:        alpaca,
		supabase:      supabase,
		redisWriter:   redisWriter,
		active:        make(map[string]bool),
		avgVolumes:    make(map[string]int64),
		hopeful:       make(map[string]bool),
		sympathyPeers: make(map[string]string),
		// Buffered size 0: closing done broadcasts to all blocked receivers.
		done:   make(chan struct{}),
		logger: log.New(os.Stderr, "[watchlist] ", log.LstdFlags),
	}
}

// Start performs the initial watchlist build immediately (intended to run at
// ~9:28am ET before market open), then launches the 5-minute refresh goroutine.
// Blocks until ctx is cancelled or Stop() closes the done channel.
// Callers should run Start in its own goroutine: go manager.Start(ctx).
func (m *Manager) Start(ctx context.Context) {
	m.logger.Printf("Start: performing initial watchlist build")
	m.build(ctx)

	// 'go' launches refreshLoop as an independent goroutine.
	// It runs on a 5-minute ticker and exits automatically after 4pm ET.
	go m.refreshLoop()

	// Block here until the service shuts down via ctx cancellation or Stop().
	// select on multiple channels simultaneously — whichever fires first wins.
	select {
	case <-ctx.Done():
		m.logger.Printf("Start: context cancelled, stopping")
	case <-m.done:
		m.logger.Printf("Start: done channel closed, stopping")
	}
}

// Stop closes the done channel, signalling all goroutines launched by Start
// to exit cleanly. Safe to call at most once — double-close panics on a channel.
func (m *Manager) Stop() {
	close(m.done)
}

// GetAvgVolume returns the cached 30-day average daily volume for ticker.
// Returns 0 if ticker is unknown or has no historical data.
// Called by tickProcessor on every trade tick — reads under avgMu.RLock()
// to avoid contending with build() writes. See WINDSURF.md Rule 1.
func (m *Manager) GetAvgVolume(ticker string) int64 {
	// RLock allows multiple goroutines to read simultaneously.
	// Only Lock (exclusive write) blocks concurrent readers.
	m.avgMu.RLock()
	defer m.avgMu.RUnlock()
	return m.avgVolumes[ticker] // returns 0 for missing keys — safe zero value
}

// GetActive returns a snapshot of the currently subscribed ticker list.
// Reads the active map under mu.RLock(). The returned slice is safe to
// iterate after the lock is released because it is a copy, not a reference.
func (m *Manager) GetActive() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tickers := make([]string, 0, len(m.active))
	for t := range m.active {
		tickers = append(tickers, t)
	}
	return tickers
}

// PromoteToHopeful adds ticker to the hopeful set, then subscribes any
// sympathy peers (from SympathyMap) not already in the active watchlist.
// Newly added peers are also recorded in the active map and the
// sympathyPeers map (peer → leader) so the tickProcessor can tag each
// peer's SymbolState with IsSympathy=true and Parent=leader.
// See ARCHITECTURE.md §4.3 — Hopeful promotion criteria.
// See ARCHITECTURE.md §4.4 — sympathy play subscription logic.
func (m *Manager) PromoteToHopeful(ticker string) {
	// Mark as hopeful under hopefulMu write lock.
	m.hopefulMu.Lock()
	m.hopeful[ticker] = true
	m.hopefulMu.Unlock()

	peers, ok := SympathyMap[ticker]
	if !ok || len(peers) == 0 {
		m.logger.Printf("PromoteToHopeful: %s promoted (no sympathy peers in SympathyMap)", ticker)
		return
	}

	// Find peers not already in the active map, then subscribe them.
	// Acquire write lock — we're both reading and mutating active.
	m.mu.Lock()
	defer m.mu.Unlock()

	var newPeers []string
	for _, peer := range peers {
		if !m.active[peer] {
			m.active[peer] = true
			newPeers = append(newPeers, peer)
		}
		// Record the sympathy relationship regardless of whether the peer
		// was already active — the leader may have changed.
		m.sympathyPeers[peer] = ticker
	}

	if len(newPeers) == 0 {
		m.logger.Printf("PromoteToHopeful: %s promoted, all sympathy peers already active", ticker)
		return
	}

	// Subscribe is called while holding mu write lock.
	// This is safe because AlpacaSubscriber.Subscribe is the only writer
	// on the WebSocket send path that also holds mu; readLoop never acquires mu.
	if err := m.alpaca.Subscribe(newPeers); err != nil {
		m.logger.Printf("PromoteToHopeful: subscribe peers for %s: %v", ticker, err)
		// Peers were added to active map — reconnectLoop will re-subscribe them.
	}
	m.logger.Printf("PromoteToHopeful: %s promoted, subscribed sympathy peers: %v", ticker, newPeers)
}

// GetSympathyParent returns the Hopeful leader ticker for a sympathy peer.
// Returns ("", false) if ticker is not a sympathy peer.
// Called by tickProcessor on every tick — reads under mu.RLock().
func (m *Manager) GetSympathyParent(ticker string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	parent, ok := m.sympathyPeers[ticker]
	return parent, ok
}

// ── Private methods ───────────────────────────────────────────────────────────

// build fetches movers and most-actives from the Alpaca screener, merges them
// with the static seedTickers, deduplicates, fetches missing avg volumes,
// then diffs against the current active set to compute subscribe/unsubscribe sets.
// All I/O errors are logged and skipped — a partial build is better than none.
func (m *Manager) build(ctx context.Context) {
	m.logger.Printf("build: starting watchlist refresh")

	// Step 1: collect tickers from screener in priority order.
	// Movers first (actively moving), then most-actives, then seed tickers.
	// This ordering ensures the cap in Step 3 keeps the highest-priority
	// symbols when the combined list exceeds maxWatchlistSize (30).
	var next []string

	movers, err := m.screener.FetchMovers(ctx)
	if err != nil {
		m.logger.Printf("build: FetchMovers: %v", err)
		// Continue — seed + most-actives still provide a useful list.
	}
	for _, r := range movers {
		next = append(next, r.Symbol)
	}

	actives, err := m.screener.FetchMostActives(ctx)
	if err != nil {
		m.logger.Printf("build: FetchMostActives: %v", err)
	}
	for _, r := range actives {
		next = append(next, r.Symbol)
	}

	// Step 2: merge with seed tickers (lowest priority — appended last).
	next = append(next, seedTickers...)

	// Step 3: deduplicate preserving priority order (movers > actives > seed),
	// then cap at maxWatchlistSize to stay within Alpaca's free-tier limit.
	seen := make(map[string]bool, len(next))
	deduped := make([]string, 0, len(next))
	for _, t := range next {
		if t != "" && !seen[t] {
			seen[t] = true
			deduped = append(deduped, t)
		}
	}
	if len(deduped) > maxWatchlistSize {
		deduped = deduped[:maxWatchlistSize]
	}
	next = deduped

	// Rebuild seen map to match the capped list — used later as the new active set.
	seen = make(map[string]bool, len(next))
	for _, t := range next {
		seen[t] = true
	}

	// Step 4: fetch avg volume for any ticker not already in avgVolumes cache.
	// Holds avgMu write lock for each store — brief critical section.
	for _, ticker := range next {
		m.avgMu.RLock()
		_, alreadyCached := m.avgVolumes[ticker]
		m.avgMu.RUnlock()

		if alreadyCached {
			continue
		}

		avgVol, err := m.screener.FetchAvgVolume(ctx, ticker)
		if err != nil {
			m.logger.Printf("build: FetchAvgVolume(%s): %v — skipping, will retry", ticker, err)
			// Do not store — allows a retry on the next build cycle.
			continue
		}

		m.avgMu.Lock()
		m.avgVolumes[ticker] = avgVol
		m.avgMu.Unlock()

		// Persist to Supabase avg_volumes table so the API service can read it.
		// See ARCHITECTURE.md §8 — avg_volumes table schema.
		if err := m.supabase.UpsertAvgVolume(ctx, ticker, avgVol); err != nil {
			m.logger.Printf("build: UpsertAvgVolume(%s): %v", ticker, err)
			// Log and continue — Redis still has the value for this session.
		}
	}

	// Step 5: compute diff against current active set.
	m.mu.RLock()
	activeCopy := make(map[string]bool, len(m.active))
	for k, v := range m.active {
		activeCopy[k] = v
	}
	m.mu.RUnlock()

	add, remove := diff(activeCopy, next)

	// Step 6: subscribe and unsubscribe — log errors but never abort the build.
	if len(add) > 0 {
		if err := m.alpaca.Subscribe(add); err != nil {
			m.logger.Printf("build: Subscribe(%v): %v", add, err)
		} else {
			m.logger.Printf("build: subscribed %d new tickers", len(add))
		}
	}

	if len(remove) > 0 {
		if err := m.alpaca.Unsubscribe(remove); err != nil {
			m.logger.Printf("build: Unsubscribe(%v): %v", remove, err)
		} else {
			m.logger.Printf("build: unsubscribed %d stale tickers", len(remove))
		}
	}

	// Step 7: atomically replace the active map under write lock.
	m.mu.Lock()
	m.active = seen // seen is the deduplicated next set as a map[string]bool
	m.mu.Unlock()

	// Step 8: persist active ticker set to Redis so the API service can read it.
	if err := m.redisWriter.SetWatchlist(next); err != nil {
		m.logger.Printf("build: SetWatchlist: %v", err)
	}

	m.logger.Printf("build: complete — %d active tickers (%d added, %d removed)",
		len(next), len(add), len(remove))
}

// diff is a pure function with no side effects — it computes the symmetric
// difference between the current active set and the next ticker list.
//
//	add    = tickers in next but not in current → need Subscribe
//	remove = tickers in current but not in next → need Unsubscribe
//
// Pure functions are trivially testable: given the same inputs they always
// produce the same outputs, with no observable state change anywhere.
// See ARCHITECTURE.md §3.2 — watchlistRefresher diff algorithm.
func diff(current map[string]bool, next []string) (add []string, remove []string) {
	// Build a set from next for O(1) lookups in the remove pass.
	nextSet := make(map[string]bool, len(next))
	for _, t := range next {
		nextSet[t] = true
	}

	// Tickers in next but absent from current → must subscribe.
	for _, t := range next {
		if !current[t] {
			add = append(add, t)
		}
	}

	// Tickers in current but absent from next → must unsubscribe.
	for t := range current {
		if !nextSet[t] {
			remove = append(remove, t)
		}
	}

	return add, remove
}

// refreshLoop runs in its own goroutine on a 5-minute ticker.
// It only calls build() during market hours (09:30–16:00 ET on weekdays).
// After 4pm ET, it stops the ticker and exits — no further refreshes until
// the next time Start() is called (i.e., the next trading day).
//
// See ARCHITECTURE.md §3.2 — watchlistRefresher goroutine trigger interval.
func (m *Manager) refreshLoop() {
	// time.NewTicker fires every 5 minutes. defer Stop() prevents a goroutine
	// leak in the time package's internal goroutine if this function returns early.
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		// select blocks until one of its cases is ready.
		select {
		case <-ticker.C:
			// Timer fired — check market hours before building.
			if !marketHoursET() {
				// Determine whether we are before open or after close.
				loc, _ := time.LoadLocation("America/New_York")
				nowET := time.Now().In(loc)
				etMinutes := nowET.Hour()*60 + nowET.Minute()

				if etMinutes >= 16*60 {
					// After 4pm ET: market is closed for today.
					// Stop the ticker and exit this goroutine permanently.
					// Start() will be called again on the next trading day.
					m.logger.Printf("refreshLoop: after 16:00 ET, market closed — exiting")
					return
				}
				// Before 9:28am or weekend — skip this tick, check again in 5 min.
				m.logger.Printf("refreshLoop: outside market hours, skipping build")
				continue
			}

			// Within market hours — run a full build.
			// context.Background() is intentional: the build should complete
			// even if the parent ctx (from Start) is about to be cancelled.
			// The 10-second httpClient timeout on ScreenerClient limits exposure.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			m.build(ctx)
			cancel()

		case <-m.done:
			// Stop() was called — exit cleanly.
			return
		}
	}
}

// isMarketOpen reports whether t, converted to America/New_York, falls on a
// weekday between 09:28 and 16:00 ET.
// Accepting a time.Time parameter makes this function deterministically
// testable with fixed timestamps — no dependency on the real wall clock.
// See ARCHITECTURE.md §3.2 — market hours constraint on watchlistRefresher.
func isMarketOpen(t time.Time) bool {
	// time.LoadLocation looks up the IANA timezone database bundled with Go.
	// "America/New_York" handles both EST (UTC-5) and EDT (UTC-4) automatically.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Timezone database unavailable — fail safe by returning false.
		// This prevents builds from running at unknown times.
		return false
	}

	tET := t.In(loc)

	// Reject weekends: Saturday (6) and Sunday (0) are not trading days.
	wd := tET.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}

	// Convert ET time to minutes-since-midnight for range comparison.
	etMinutes := tET.Hour()*60 + tET.Minute()

	// 09:28 ET = 9*60+28 = 568 minutes
	// 16:00 ET = 16*60   = 960 minutes
	return etMinutes >= 568 && etMinutes < 960
}

// marketHoursET returns true if the current wall-clock time is within market
// hours. This is the production entry point called by refreshLoop.
// NEVER uses time.Local — delegates to isMarketOpen which always converts
// explicitly via America/New_York regardless of container timezone.
func marketHoursET() bool {
	return isMarketOpen(time.Now())
}
