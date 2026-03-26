// pipeline.go — reason generation orchestration, caching, and goroutine management.
// Coordinates FinnhubClient and ClaudeClient, caches results in Redis, and
// persists to Supabase. All dependencies are accessed through interfaces.
//
// See ARCHITECTURE.md §6   — reason generation pipeline design.
// See ARCHITECTURE.md §6.3 — caching and TTL rules for reasons.
// See WINDSURF.md Rule 1   — Submit() must never block the caller.
package reasons

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"momentum/ingestion/internal/types"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

// ReasonCacher abstracts Redis reason storage so Pipeline never imports the
// concrete redis package. The concrete implementation is responsible for
// setting the key format (reasons:{ticker}:{YYYY-MM-DD ET}) and TTL.
type ReasonCacher interface {
	// SetReason stores a generated reason in Redis.
	// The concrete implementation sets the TTL to expire at 16:00 ET today.
	SetReason(ctx context.Context, ticker, reason string) error
	// GetReason returns the cached reason and true if a hit exists,
	// or ("", false) on a cache miss. Follows the Go comma-ok idiom.
	GetReason(ctx context.Context, ticker string) (string, bool)
}

// ReasonStorer abstracts Supabase reason persistence.
// Errors from StoreReason must never block or fail the pipeline — they are
// logged and discarded so a Supabase outage does not impact the live feed.
type ReasonStorer interface {
	StoreReason(ctx context.Context, ticker, reason string, headlines []string) error
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// Pipeline receives signals, generates one-sentence reasons via Finnhub + Claude,
// caches them in Redis, and persists to Supabase. All signal handling is
// asynchronous — Submit() returns immediately and processing happens in a
// dedicated worker goroutine.
type Pipeline struct {
	finnhub  *FinnhubClient
	claude   *ClaudeClient
	redis    ReasonCacher
	supabase ReasonStorer

	// signalChan is a buffered channel of capacity 100.
	// A buffered channel decouples the fast tickProcessor (producer) from the
	// slower reason pipeline (consumer) — the producer never blocks as long as
	// fewer than 100 signals are queued. See WINDSURF.md Rule 1.
	signalChan chan types.Signal

	// done is closed by Stop() to broadcast shutdown to the worker goroutine.
	done chan struct{}

	logger *log.Logger
}

// NewPipeline initialises a Pipeline. Does not start the worker goroutine —
// call Start() separately after all dependencies are wired.
func NewPipeline(
	finnhubAPIKey string,
	claudeAPIKey string,
	redis ReasonCacher,
	supabase ReasonStorer,
) *Pipeline {
	return &Pipeline{
		finnhub:    NewFinnhubClient(finnhubAPIKey),
		claude:     NewClaudeClient(claudeAPIKey),
		redis:      redis,
		supabase:   supabase,
		signalChan: make(chan types.Signal, 100),
		done:       make(chan struct{}),
		logger:     log.New(os.Stderr, "[reasons] ", log.LstdFlags),
	}
}

// Start launches the worker goroutine and returns immediately.
// The worker reads signals from signalChan and calls process() for each.
// It exits cleanly when Stop() closes the done channel.
func (p *Pipeline) Start() {
	// 'go' schedules this closure as an independent goroutine — it runs
	// concurrently without blocking the caller.
	go func() {
		for {
			// select blocks until one case is ready.
			select {
			case sig := <-p.signalChan:
				// A signal arrived — process it synchronously within the worker.
				// Only one signal is processed at a time (sequential pipeline).
				// If concurrent processing is needed later, spawn a goroutine here.
				p.process(sig)

			case <-p.done:
				// Stop() was called — exit the worker cleanly.
				return
			}
		}
	}()
}

// Stop closes the done channel, causing the worker goroutine launched by
// Start() to exit on its next select iteration. Call at most once.
func (p *Pipeline) Stop() {
	close(p.done)
}

// Submit enqueues a signal for reason generation. It is non-blocking per
// WINDSURF.md Rule 1 — if signalChan is full (100 pending signals), the
// signal is dropped and logged rather than blocking the tickProcessor hot path.
//
// select with a default case makes the send non-blocking: if the channel
// is not immediately ready to receive, the default branch executes.
func (p *Pipeline) Submit(signal types.Signal) {
	select {
	case p.signalChan <- signal:
		// Signal queued successfully.
	default:
		// Channel full — drop the signal rather than blocking.
		// This trades completeness for latency: the tickProcessor must never wait.
		p.logger.Printf("Submit: channel full, dropping signal for %s (Z=%.2f)", signal.Ticker, signal.Z)
	}
}

// ── Private methods ───────────────────────────────────────────────────────────

// process handles a single signal through the full 7-step reason pipeline.
// Each step degrades gracefully on error — the pipeline always produces a
// stored reason even if Finnhub or Claude is temporarily unavailable.
func (p *Pipeline) process(signal types.Signal) {
	start := time.Now()
	ctx := context.Background()

	// Step 1: check Redis cache.
	// If a reason was already generated for this ticker today, skip the
	// expensive Finnhub + Claude calls. The cache key includes the date (ET)
	// so a new reason is generated each trading day automatically.
	if reason, hit := p.redis.GetReason(ctx, signal.Ticker); hit {
		p.logger.Printf("process(%s): cache hit — %q", signal.Ticker, truncate(reason, 50))
		return
	}

	// Step 2: determine direction string for the Claude prompt.
	direction := "up"
	if signal.ChangePercent < 0 {
		direction = "down"
	}

	// Step 3: fetch headlines from Finnhub.
	// On error, continue with an empty slice — Claude uses the no-news prompt.
	// The pipeline must always produce a reason even when Finnhub is down.
	headlines, err := p.finnhub.FetchHeadlines(ctx, signal.Ticker)
	if err != nil {
		p.logger.Printf("process(%s): FetchHeadlines: %v — continuing with no headlines", signal.Ticker, err)
		headlines = []string{}
	}

	// Step 4: call Claude to generate a one-sentence reason.
	// On error, use a safe fallback string so the pipeline always stores something.
	reason, err := p.claude.GenerateReason(ctx, signal.Ticker, direction, signal.ChangePercent, headlines)
	if err != nil {
		p.logger.Printf("process(%s): GenerateReason: %v — using fallback reason", signal.Ticker, err)
		reason = "Unable to determine reason — technical move on elevated volume."
	}

	// Step 5: write reason to Redis.
	// The concrete ReasonCacher implementation is responsible for setting the
	// key format (reasons:{ticker}:{YYYY-MM-DD ET}) and TTL until 16:00 ET.
	// Errors are logged but do not abort the pipeline — Supabase still gets written.
	if err := p.redis.SetReason(ctx, signal.Ticker, reason); err != nil {
		p.logger.Printf("process(%s): SetReason: %v", signal.Ticker, err)
	}

	// Step 6: persist reason to Supabase signals / reasons table.
	// This call is best-effort — a Supabase outage must never block the live feed.
	if err := p.supabase.StoreReason(ctx, signal.Ticker, reason, headlines); err != nil {
		p.logger.Printf("process(%s): StoreReason: %v", signal.Ticker, err)
	}

	// Step 7: log completion with ticker, reason preview, and elapsed time.
	p.logger.Printf("process(%s): done in %v — %q", signal.Ticker, time.Since(start), truncate(reason, 50))
}

// ttlUntilMarketClose computes the duration from time.Now() until 16:00 ET
// today. Returns 24 hours if the current time is already past 16:00 ET to
// prevent a negative TTL from being passed to Redis.
//
// The date component of the Redis key (`reasons:{ticker}:{YYYY-MM-DD}`) uses
// ET so the key naturally aligns with the trading day.
func (p *Pipeline) TtlUntilMarketClose() time.Duration {
	// time.LoadLocation looks up the IANA timezone database ("tzdata").
	// "America/New_York" handles both EST (UTC-5) and EDT (UTC-4) automatically.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Timezone database unavailable — return a safe 1-hour TTL.
		return time.Hour
	}

	now := time.Now().In(loc)

	// Build 16:00 ET on today's calendar date.
	closeToday := time.Date(now.Year(), now.Month(), now.Day(), 16, 0, 0, 0, loc)

	ttl := time.Until(closeToday)
	if ttl <= 0 {
		// Already past market close — return 24 hours so the key expires
		// well before tomorrow's session begins.
		return 24 * time.Hour
	}
	return ttl
}

// truncate returns the first n characters of s, or s itself if len(s) <= n.
// Used for log preview lines to keep log output readable.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return fmt.Sprintf("%s…", string(runes[:n]))
}
