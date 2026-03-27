// Package main wires all ingestion service goroutines together and manages
// the full lifecycle: startup, signal processing, and graceful shutdown.
//
// Goroutine map (see ARCHITECTURE.md §3.2):
//
//	wsClient           — Alpaca WebSocket feed (alpaca.AlpacaClient)
//	tickProcessor      — Z-score engine + signal detection (inline goroutine)
//	watchlistRefresher — movers fetch every 5 min (watchlist.Manager)
//	redisWriter        — batch flush every 250ms (redis.RedisWriter)
//	supabaseWriter     — async persistent writes (supabase.SupabaseClient)
//	reasonPipeline     — Finnhub + Claude, off hot path (reasons.Pipeline)
//	hopefulPromoter    — Hopeful promotion evaluation (hopeful.Promoter)
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"momentum/ingestion/internal/alpaca"
	"momentum/ingestion/internal/hopeful"
	"momentum/ingestion/internal/reasons"
	"momentum/ingestion/internal/redis"
	"momentum/ingestion/internal/supabase"
	"momentum/ingestion/internal/types"
	"momentum/ingestion/internal/watchlist"
	"momentum/ingestion/internal/zscore"
)

func main() {
	// ── Step 1: load and validate environment variables ───────────────────
	// Fail fast on missing credentials — never proceed with empty values.
	// os.Getenv returns "" for unset vars; we treat "" as missing.
	alpacaKey := requireEnv("ALPACA_API_KEY")
	alpacaSecret := requireEnv("ALPACA_SECRET_KEY")
	finnhubKey := requireEnv("FINNHUB_API_KEY")
	anthropicKey := requireEnv("ANTHROPIC_API_KEY")
	supabaseURL := requireEnv("SUPABASE_URL")
	supabaseKey := requireEnv("SUPABASE_SERVICE_KEY")

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	log.Println("main: environment validated")

	// ── Step 2: connect Redis writer ─────────────────────────────────────
	// RedisWriter starts its flushLoop goroutine internally on creation.
	// See ARCHITECTURE.md §3.2 — redisWriter goroutine.
	rw, err := redis.NewRedisWriter(redisURL)
	if err != nil {
		log.Fatalf("main: Redis writer: %v", err)
	}
	log.Println("main: Redis writer connected")

	// ── Step 3: connect Redis reason cache ───────────────────────────────
	// Separate connection for the reason pipeline cache (GET/SET with TTL).
	rc, err := redis.NewReasonCache(redisURL)
	if err != nil {
		log.Fatalf("main: Redis reason cache: %v", err)
	}
	log.Println("main: Redis reason cache connected")

	// ── Step 4: connect Supabase client ──────────────────────────────────
	// One SupabaseClient satisfies all four interfaces used across packages.
	sb := supabase.NewSupabaseClient(supabaseURL, supabaseKey)
	log.Println("main: Supabase client created")

	// ── Step 5: create Alpaca client ─────────────────────────────────────
	// tickChan carries raw trade events from the WebSocket readLoop to the
	// tickProcessor goroutine. Capacity 500 provides headroom at peak rates.
	// See ARCHITECTURE.md §3.2 — wsClient → tickProcessor channel.
	tickChan := make(chan types.SymbolState, 500)
	alpacaClient := alpaca.NewAlpacaClient(alpacaKey, alpacaSecret, tickChan)
	log.Println("main: Alpaca client created")

	// ── Step 6: create Z-score engine ────────────────────────────────────
	// Engine is stateless — all mutable state lives in *SymbolState pointers.
	engine := zscore.NewEngine()
	log.Println("main: Z-score engine created")

	// ── Step 7: create and start reason pipeline ─────────────────────────
	// Pipeline.Start() launches its worker goroutine and returns immediately.
	pipeline := reasons.NewPipeline(finnhubKey, anthropicKey, rc, sb)
	pipeline.Start()
	log.Println("main: reason pipeline started")

	// ── Step 8: create watchlist manager ──────────────────────────────────
	// Watchlist manager must be created before the Hopeful promoter because
	// the promoter needs a WatchlistPromoter interface reference.
	screener := watchlist.NewScreenerClient(alpacaKey, alpacaSecret)
	watchlistMgr := watchlist.NewManager(screener, alpacaClient, sb, rw)
	log.Println("main: watchlist manager created")

	// ── Step 9: create and start Hopeful promoter ────────────────────────
	// Receives watchlistMgr as the WatchlistPromoter interface.
	// StartDemotionLoop() launches its background goroutine.
	promoter := hopeful.NewPromoter(watchlistMgr, sb)
	promoter.StartDemotionLoop()
	log.Println("main: Hopeful promoter started")

	// ── Step 10: connect Alpaca and start WebSocket ──────────────────────
	// context.WithCancel creates a parent context whose cancellation
	// propagates to all child goroutines that accept this ctx.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := alpacaClient.Connect(ctx); err != nil {
		log.Fatalf("main: Alpaca connect: %v", err)
	}
	log.Println("main: Alpaca WebSocket connected")

	// ── Step 11: start watchlist manager ──────────────────────────────────
	// Manager.Start() blocks, so it runs in its own goroutine.
	// It performs the initial build() immediately, then launches refreshLoop.
	go watchlistMgr.Start(ctx)
	log.Println("main: watchlist manager started")

	// ── Step 12: start tick processor goroutine ──────────────────────────
	// This is the hot path: every trade tick flows through here.
	// For each tick: Z-score → signal check → promote/submit/write → enqueue.
	// See ARCHITECTURE.md §3.2 — tickProcessor goroutine.
	go func() {
		for {
			// select blocks until a tick arrives or the context is cancelled.
			select {
			case state, ok := <-tickChan:
				if !ok {
					// Channel was closed — Alpaca client shut down.
					return
				}

				// Fetch the 30-day average volume for this ticker from the
				// watchlist manager's cache. Returns 0 if unknown.
				avgVol := watchlistMgr.GetAvgVolume(state.Ticker)

				// Run the Z-score engine. Returns (Signal, true) if a
				// threshold was crossed, (Signal{}, false) otherwise.
				sig, fired := engine.ProcessTick(&state, state.Price, state.Volume, avgVol)

				if fired {
					// Evaluate Hopeful promotion criteria.
					promoter.Evaluate(sig, &state)

					// Submit signal to the reason pipeline (non-blocking).
					pipeline.Submit(sig)

					// Write signal to Supabase in a separate goroutine.
					// 'go' ensures the Supabase HTTP call never blocks the
					// tickProcessor — Rule 1 (WINDSURF.md).
					go sb.WriteSignal(context.Background(), sig)
				}

				// Enqueue the (possibly updated) state for Redis batch flush.
				// Enqueue is also non-blocking (select/default internally).
				rw.Enqueue(state)

			case <-ctx.Done():
				// Parent context cancelled — shut down the tick processor.
				return
			}
		}
	}()
	log.Println("main: tick processor started")

	// ── Step 13: wait for shutdown signal ─────────────────────────────────
	// signal.Notify registers sigChan to receive SIGINT (Ctrl-C) and
	// SIGTERM (Docker/Fly.io stop). The main goroutine blocks here until
	// one of these signals arrives.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Printf("main: received %v, starting graceful shutdown", sig)

	// ── Step 14: graceful shutdown ───────────────────────────────────────
	// Order matters: cancel context first (stops goroutines that select on
	// ctx.Done), then close components that own their own goroutines.

	// 1. Cancel the parent context — signals tickProcessor, watchlist manager,
	//    and any goroutine using ctx to stop.
	cancel()
	log.Println("shutdown: context cancelled")

	// 2. Close the Alpaca WebSocket connection and stop readLoop/reconnectLoop.
	alpacaClient.Close()
	log.Println("shutdown: Alpaca client closed")

	// 3. Stop the watchlist manager's refreshLoop goroutine.
	watchlistMgr.Stop()
	log.Println("shutdown: watchlist manager stopped")

	// 4. Stop the reason pipeline worker goroutine.
	pipeline.Stop()
	log.Println("shutdown: reason pipeline stopped")

	// 5. Stop the Hopeful promoter's demotion loop goroutine.
	promoter.Stop()
	log.Println("shutdown: Hopeful promoter stopped")

	// 6. Close the Redis writer last — this triggers a final flush of any
	//    buffered SymbolState updates before the connection is released.
	rw.Close()
	log.Println("shutdown: Redis writer closed")

	log.Println("shutdown complete")
}

// requireEnv reads an environment variable and calls log.Fatal if it is empty.
// This enforces fail-fast startup — the service never runs with missing credentials.
func requireEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("main: required environment variable %s is not set", key)
	}
	return val
}
