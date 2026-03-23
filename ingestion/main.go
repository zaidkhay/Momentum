package main

// See ARCHITECTURE.md §3.2 — goroutine map for this service:
//   wsClient           — Alpaca WebSocket feed
//   tickProcessor      — Z-score engine + signal detection
//   watchlistRefresher — movers fetch every 5 min
//   redisWriter        — batch flush every 250ms
//   supabaseWriter     — async persistent writes
//   reasonPipeline     — Finnhub + Claude, off hot path
//   hopefulPromoter    — Hopeful promotion evaluation
//
// Build order per WINDSURF.md §Build order:
//   Start with ingestion/internal/redis/ before wiring here.

func main() {
	// TODO: See ARCHITECTURE.md §3.2 — wire all goroutines together
}
