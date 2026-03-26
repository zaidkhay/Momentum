package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    "momentum/ingestion/internal/alpaca"
    rediswriter "momentum/ingestion/internal/redis"
    "momentum/ingestion/internal/types"
    "momentum/ingestion/internal/zscore"
)

func main() {
    apiKey := os.Getenv("ALPACA_API_KEY")
    apiSecret := os.Getenv("ALPACA_SECRET_KEY")

    if apiKey == "" || apiSecret == "" {
        log.Fatal("ALPACA_API_KEY and ALPACA_SECRET_KEY required")
    }

    // Step 1 — Redis writer
    rw, err := rediswriter.NewRedisWriter("redis://localhost:6379")
    if err != nil {
        log.Fatal("Redis failed:", err)
    }
    defer rw.Close()
    fmt.Println("✓ Redis writer connected")

    // Step 2 — Alpaca client
    tickChan := make(chan types.SymbolState, 500)
    client := alpaca.NewAlpacaClient(apiKey, apiSecret, tickChan)

    ctx := context.Background()
    if err := client.Connect(ctx); err != nil {
        log.Fatal("Alpaca failed:", err)
    }
    defer client.Close()
    fmt.Println("✓ Alpaca connected")

    // Subscribe to test symbols
    client.Subscribe([]string{"AAPL", "TSLA", "NVDA", "MARA", "AMTX"})
    fmt.Println("✓ Subscribed to 5 symbols")

    // Step 3 — Z-score engine
    engine := zscore.NewEngine()
    fmt.Println("✓ Z-score engine ready")

    // Tick processor
    signalCount := 0
    go func() {
        for state := range tickChan {
            signal, fired := engine.ProcessTick(
                &state,
                state.Price,
                int64(100),
                int64(100),
            )
            if fired {
                signalCount++
                fmt.Printf("  SIGNAL: %s Z=%.2f RelVol=%.2f\n",
                    signal.Ticker, signal.Z, signal.RelVol)
            }
            rw.Enqueue(state)
            fmt.Printf("  tick: %s $%.2f\n",
                state.Ticker, state.Price)
        }
    }()

    fmt.Println("Running smoke test for 60 seconds...")
    fmt.Println("(ticks only appear during market hours)")
    time.Sleep(60 * time.Second)

    fmt.Printf("\nSmoke test complete. Signals fired: %d\n",
        signalCount)
}