// Package redis implements the Redis batch writer for the ingestion service.
// It collects SymbolState updates from the tickProcessor goroutine via a
// buffered channel and flushes them to Redis every 250ms using a pipeline
// (single network round trip per flush cycle).
//
// See ARCHITECTURE.md §3.2 — redisWriter goroutine.
// See ARCHITECTURE.md §7   — Redis key schema.
// Rule 1 (WINDSURF.md): Enqueue must never block the tickProcessor caller.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"momentum/ingestion/internal/types"
)

// RedisWriter batches SymbolState updates and flushes them to Redis every 250ms.
// All fields are unexported — callers use only the public methods.
type RedisWriter struct {
	// client is the go-redis connection pool. A single *redis.Client manages
	// multiple underlying TCP connections automatically.
	client *goredis.Client

	// queue is a buffered channel that decouples the tickProcessor goroutine
	// (producer) from the flushLoop goroutine (consumer).
	// Capacity 500: at ~250ms flush intervals and typical tick rates, this
	// provides ample headroom before any drops occur.
	// Buffered channels in Go allow the sender to proceed without waiting
	// for the receiver, up to the capacity limit.
	queue chan types.SymbolState

	// done is a signal-only channel (zero-size struct carries no data).
	// Closing it broadcasts a shutdown signal to flushLoop.
	// This is the standard Go idiom for one-to-many goroutine cancellation.
	done chan struct{}

	// mirror is an in-memory copy of every SymbolState seen so far.
	// It is only ever read and written from the flushLoop goroutine,
	// so no mutex is needed — goroutine confinement provides safety.
	mirror map[string]types.SymbolState

	logger *log.Logger
}

// NewRedisWriter parses redisURL, connects to Redis, and starts the background
// flushLoop goroutine. Returns a ready-to-use *RedisWriter or an error if the
// connection cannot be established.
//
// See ARCHITECTURE.md §3.2 — redisWriter goroutine is started here.
func NewRedisWriter(redisURL string) (*RedisWriter, error) {
	// redis.ParseURL converts "redis://host:port/db" into *redis.Options.
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("NewRedisWriter: parse URL %q: %w", redisURL, err)
	}

	client := goredis.NewClient(opts)

	// Verify connectivity at startup with a 5-second timeout.
	// context.WithTimeout returns a derived context that cancels automatically.
	// defer cancel() ensures the context's internal timer is always freed,
	// even if Ping returns an error early.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("NewRedisWriter: ping failed: %w", err)
	}

	w := &RedisWriter{
		client: client,
		// make(chan T, N) creates a buffered channel with capacity N.
		// The producer (Enqueue) can write up to 500 items before the channel
		// fills. We never let it fill — see Enqueue's non-blocking select.
		queue: make(chan types.SymbolState, 500),
		// make(chan struct{}) creates an unbuffered signal channel.
		// Closing it (via Close()) unblocks any goroutine receiving on it.
		done:   make(chan struct{}),
		mirror: make(map[string]types.SymbolState),
		logger: log.New(os.Stderr, "[redis] ", log.LstdFlags),
	}

	// 'go' launches flushLoop as a concurrent goroutine.
	// It runs independently, sleeping between 250ms flush cycles,
	// until Close() signals the done channel.
	go w.flushLoop()

	return w, nil
}

// Enqueue pushes a SymbolState update into the buffered channel.
// It is safe to call from any goroutine concurrently.
// It never blocks — if the channel is full, the update is dropped.
//
// Rule 1 (WINDSURF.md): the tickProcessor hot path must never block on I/O.
func (w *RedisWriter) Enqueue(state types.SymbolState) {
	// select with a default case is Go's non-blocking channel send idiom.
	// If the channel has room, the state is queued immediately.
	// If the channel is full (500 items backed up), the default branch runs
	// and the update is silently dropped rather than stalling the caller.
	select {
	case w.queue <- state:
		// Queued successfully — flushLoop will pick it up within 250ms.
	default:
		// Channel full: this should not happen at normal tick rates.
		// Log and drop rather than block. See Rule 1 (WINDSURF.md).
		w.logger.Printf("Enqueue: queue full, dropping update for %s", state.Ticker)
	}
}

// SetWatchlist atomically replaces the watchlist:active Redis SET with
// the given tickers. Uses a pipeline to DEL the old key and SADD all
// tickers in a single round trip. No expiry — the set persists until
// the next build() call overwrites it.
func (w *RedisWriter) SetWatchlist(tickers []string) error {
	ctx := context.Background()
	const key = "watchlist:active"

	// Redis pipeline batches DEL + SADD into one network round trip.
	pipe := w.client.Pipeline()
	pipe.Del(ctx, key)

	if len(tickers) > 0 {
		// Convert []string to []interface{} for SADD's variadic parameter.
		members := make([]interface{}, len(tickers))
		for i, t := range tickers {
			members[i] = t
		}
		pipe.SAdd(ctx, key, members...)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("SetWatchlist: pipeline exec: %w", err)
	}

	w.logger.Printf("SetWatchlist: wrote %d tickers to %s", len(tickers), key)
	return nil
}

// Close signals the flushLoop goroutine to stop after one final flush,
// then closes the Redis client's connection pool.
// Call this once during graceful shutdown.
func (w *RedisWriter) Close() {
	// close(ch) on a struct{} channel broadcasts to all goroutines blocked
	// on a receive from that channel. flushLoop selects on w.done, so it
	// will exit its loop, run a final flush, then return.
	close(w.done)

	if err := w.client.Close(); err != nil {
		w.logger.Printf("Close: redis client error: %v", err)
	}
}

// flushLoop runs in its own goroutine, calling flush() every 250ms.
// It exits cleanly when the done channel is closed by Close().
//
// See ARCHITECTURE.md §3.2 — redisWriter goroutine trigger interval.
func (w *RedisWriter) flushLoop() {
	// time.NewTicker returns a Ticker whose channel fires every 250ms.
	// Unlike time.Sleep, a Ticker accounts for the time flush() itself takes,
	// keeping the interval consistent under load.
	ticker := time.NewTicker(250 * time.Millisecond)

	// defer ensures ticker.Stop() is always called when this goroutine exits.
	// Without Stop(), the time package's internal goroutine would leak.
	defer ticker.Stop()

	for {
		// select blocks until one of its cases is ready.
		// Both channels are checked simultaneously — whichever fires first wins.
		select {
		case <-ticker.C:
			// 250ms elapsed — flush all queued updates to Redis.
			w.flush()

		case <-w.done:
			// Close() was called — perform a final flush to drain any
			// remaining items before this goroutine exits.
			w.flush()
			return
		}
	}
}

// flush drains all pending SymbolState values from the queue, updates the
// internal mirror map, then writes all Redis keys in a single pipeline batch.
//
// Keys written per ARCHITECTURE.md §7:
//
//	price:{ticker}    — full SymbolState JSON, no expiry
//	rvol:{ticker}     — RelVol as string, no expiry
//	sector:{name}     — JSON array of tickers sorted by |ChangePercent| desc
//	hopeful:tickers   — JSON array of all tickers where IsHopeful == true
//
// All writes go through a single pipeline.Exec() call — one network round trip.
func (w *RedisWriter) flush() {
	// Step 1: Drain all pending items from the queue channel into the mirror.
	// This is a non-blocking loop using a labeled break.
	// In Go, 'break' inside a select only exits the select, not the enclosing
	// for loop. A labeled break ('break drain') exits the for loop itself.
drain:
	for {
		select {
		case state := <-w.queue:
			// Overwrite any previous value for this ticker — we only care
			// about the latest state at flush time.
			w.mirror[state.Ticker] = state
		default:
			// No more items buffered in the channel — stop draining.
			break drain
		}
	}

	// Nothing to write if no state has been seen yet.
	if len(w.mirror) == 0 {
		return
	}

	ctx := context.Background()

	// Step 2: Open a Redis pipeline.
	// A pipeline queues commands client-side and sends them all in one
	// network round trip when Exec() is called, instead of one round trip
	// per SET command. This is critical for the 250ms flush window.
	pipe := w.client.Pipeline()

	// Step 3: Write price:{ticker} and rvol:{ticker} for every known ticker.
	// See ARCHITECTURE.md §7 — key schema.
	for ticker, state := range w.mirror {
		stateJSON, err := json.Marshal(state)
		if err != nil {
			w.logger.Printf("flush: marshal SymbolState for %s: %v", ticker, err)
			continue
		}
		// SET price:{ticker} <JSON> — no expiry (0 = persist indefinitely).
		// Overwritten every 250ms by the next flush cycle.
		pipe.Set(ctx, "price:"+ticker, string(stateJSON), 0)

		// SET rvol:{ticker} <float> — relative volume as a plain string.
		// See ARCHITECTURE.md §7 — rvol:{ticker}.
		pipe.Set(ctx, "rvol:"+ticker, strconv.FormatFloat(state.RelVol, 'f', 6, 64), 0)
	}

	// Step 4: Rebuild sector:{name} arrays.
	// Group all tickers in the mirror by their Sector field, sort each group
	// by absolute ChangePercent descending, then write the ticker-only array.
	// See ARCHITECTURE.md §7 — sector:{name} value is a JSON array of tickers.
	sectorBuckets := make(map[string][]types.SymbolState)
	for _, state := range w.mirror {
		sectorBuckets[state.Sector] = append(sectorBuckets[state.Sector], state)
	}

	for sectorName, states := range sectorBuckets {
		// sort.Slice sorts in-place using a comparison function (a closure).
		// math.Abs ensures descending order by magnitude regardless of sign.
		sort.Slice(states, func(i, j int) bool {
			return math.Abs(states[i].ChangePercent) > math.Abs(states[j].ChangePercent)
		})

		// Extract ticker strings only — the API hydrates full objects from
		// individual price:{ticker} keys. See ARCHITECTURE.md §7 + §9.1.
		tickers := make([]string, len(states))
		for i, s := range states {
			tickers[i] = s.Ticker
		}

		tickersJSON, err := json.Marshal(tickers)
		if err != nil {
			w.logger.Printf("flush: marshal sector %s: %v", sectorName, err)
			continue
		}
		pipe.Set(ctx, "sector:"+sectorName, string(tickersJSON), 0)
	}

	// Step 5: Rebuild hopeful:tickers.
	// Collect all tickers currently flagged IsHopeful == true.
	// See ARCHITECTURE.md §7 — hopeful:tickers.
	hopefulTickers := make([]string, 0)
	for ticker, state := range w.mirror {
		if state.IsHopeful {
			hopefulTickers = append(hopefulTickers, ticker)
		}
	}

	hopefulJSON, err := json.Marshal(hopefulTickers)
	if err != nil {
		w.logger.Printf("flush: marshal hopeful tickers: %v", err)
	} else {
		pipe.Set(ctx, "hopeful:tickers", string(hopefulJSON), 0)
	}

	// Step 6: Execute all queued pipeline commands in one round trip.
	// Errors are logged but never cause a panic — the next flush will retry
	// with the latest state. See WINDSURF.md §Code style — no bare panics.
	if _, err := pipe.Exec(ctx); err != nil {
		w.logger.Printf("flush: pipeline exec: %v", err)
	}
}
