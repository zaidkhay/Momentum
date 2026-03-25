package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "momentum/ingestion/internal/alpaca"
    "momentum/ingestion/internal/redis"
    "momentum/ingestion/internal/types"
    "os"
)

func main() {
    // Create the Redis writer
    rw, err := redis.NewRedisWriter("redis://localhost:6379")
    if err != nil {
        log.Fatal("Redis connection failed:", err)
    }
    defer rw.Close()

    // Create the output channel
    out := make(chan types.SymbolState, 500)

    // Create the Alpaca client
    client := alpaca.NewAlpacaClient(
        os.Getenv("ALPACA_API_KEY"),
        os.Getenv("ALPACA_SECRET_KEY"),
        out,
    )

    // Connect
    ctx := context.Background()
    if err := client.Connect(ctx); err != nil {
        log.Fatal("Alpaca connection failed:", err)
    }
    defer client.Close()

    // Subscribe to a small set of symbols
    if err := client.Subscribe([]string{"AAPL", "TSLA", "NVDA"}); err != nil {
        log.Fatal("Subscribe failed:", err)
    }

    fmt.Println("Connected and subscribed. Listening for 30 seconds...")

    // Forward updates from out channel to Redis writer
    go func() {
        for state := range out {
            rw.Enqueue(state)
        }
    }()

    // Run for 30 seconds then exit
    time.Sleep(30 * time.Second)
    fmt.Println("Done.")
}