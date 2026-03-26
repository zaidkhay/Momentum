// cacher.go — Redis reason cache for the reason generation pipeline.
// Implements the reasons.ReasonCacher interface. Separate from writer.go
// which handles the bulk SymbolState flush cycle.
//
// See ARCHITECTURE.md §6.3 — reason caching and TTL rules.
// See ARCHITECTURE.md §7   — Redis key schema (reasons:{ticker}:{date}).
package redis

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ReasonCache implements reasons.ReasonCacher using Redis GET/SET with TTL.
// The key format is reasons:{ticker}:{YYYY-MM-DD} where the date is in
// America/New_York (ET) so it aligns with the trading day.
type ReasonCache struct {
	client *goredis.Client
	logger *log.Logger
}

// NewReasonCache connects to Redis and verifies connectivity with a Ping.
// Returns an error if the connection cannot be established.
func NewReasonCache(redisURL string) (*ReasonCache, error) {
	// goredis.ParseURL converts "redis://host:port/db" into *goredis.Options.
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("NewReasonCache: parse URL %q: %w", redisURL, err)
	}

	client := goredis.NewClient(opts)

	// Verify connectivity at startup with a 5-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("NewReasonCache: ping failed: %w", err)
	}

	return &ReasonCache{
		client: client,
		logger: log.New(os.Stderr, "[redis-cache] ", log.LstdFlags),
	}, nil
}

// SetReason stores a generated reason in Redis with a TTL that expires at
// 16:00 ET today. The key format is reasons:{ticker}:{YYYY-MM-DD in ET}.
//
// The TTL ensures stale reasons are automatically evicted after market close,
// so a fresh reason is generated on the next trading day without manual cleanup.
func (rc *ReasonCache) SetReason(ctx context.Context, ticker, reason string) error {
	key := rc.reasonKey(ticker)
	ttl := ttlUntilMarketCloseET()

	// Set with EX (expiry) — Redis atomically sets the value and TTL in one call.
	if err := rc.client.Set(ctx, key, reason, ttl).Err(); err != nil {
		return fmt.Errorf("SetReason(%s): %w", ticker, err)
	}

	rc.logger.Printf("SetReason: %s cached (TTL %v)", ticker, ttl)
	return nil
}

// GetReason retrieves a cached reason from Redis.
// Returns (reason, true) on cache hit, or ("", false) on cache miss.
// Follows the Go comma-ok idiom so callers can distinguish "no entry"
// from "empty string entry" (though empty reasons should never be stored).
func (rc *ReasonCache) GetReason(ctx context.Context, ticker string) (string, bool) {
	key := rc.reasonKey(ticker)

	val, err := rc.client.Get(ctx, key).Result()
	if err == goredis.Nil {
		// Key does not exist — cache miss.
		return "", false
	}
	if err != nil {
		// Unexpected Redis error — treat as cache miss so the pipeline regenerates.
		rc.logger.Printf("GetReason(%s): %v — treating as cache miss", ticker, err)
		return "", false
	}

	return val, true
}

// ── Private helpers ───────────────────────────────────────────────────────────

// reasonKey builds the Redis key for a ticker using today's date in ET.
// Format: reasons:{ticker}:{YYYY-MM-DD}
// The date uses America/New_York so it aligns with the trading day —
// a reason generated at 3:55pm ET and one at 4:05pm ET belong to
// different trading days.
func (rc *ReasonCache) reasonKey(ticker string) string {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Fallback to UTC if timezone DB is unavailable.
		return fmt.Sprintf("reasons:%s:%s", ticker, time.Now().UTC().Format("2006-01-02"))
	}
	today := time.Now().In(loc).Format("2006-01-02")
	return fmt.Sprintf("reasons:%s:%s", ticker, today)
}

// ttlUntilMarketCloseET computes the duration from now until 16:00 ET today.
// Returns 24 hours if already past 16:00 ET to prevent negative TTL.
// Same logic as reasons.TtlUntilMarketClose but lives here to avoid an
// import cycle between the redis and reasons packages.
func ttlUntilMarketCloseET() time.Duration {
	// time.LoadLocation handles EST/EDT transitions automatically.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Timezone DB unavailable — return a safe 1-hour TTL.
		return time.Hour
	}

	now := time.Now().In(loc)
	closeToday := time.Date(now.Year(), now.Month(), now.Day(), 16, 0, 0, 0, loc)

	ttl := time.Until(closeToday)
	if ttl <= 0 {
		// Already past market close — expire well before next session.
		return 24 * time.Hour
	}
	return ttl
}
