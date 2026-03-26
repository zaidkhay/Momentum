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
)

func main() {
	apiKey := os.Getenv("ALPACA_API_KEY")
	apiSecret := os.Getenv("ALPACA_SECRET_KEY")

	if apiKey == "" || apiSecret == "" {
		log.Fatal("ALPACA_API_KEY and ALPACA_SECRET_KEY must be set")
	}

	// Connect to Redis
	rw, err := rediswriter.NewRedisWriter("redis://localhost:6379")
	if err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	defer rw.Close()
	fmt.Println("✓ Redis connected")

	// Create output channel
	out := make(chan types.SymbolState, 500)

	// Create Alpaca client
	client := alpaca.NewAlpacaClient(apiKey, apiSecret, out)

	// Connect to Alpaca
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		log.Fatal("Alpaca connection failed:", err)
	}
	defer client.Close()
	fmt.Println("✓ Alpaca connected and authenticated")

	// Subscribe to test symbols
	if err := client.Subscribe([]string{"AAPL", "TSLA", "NVDA"}); err != nil {
		log.Fatal("Subscribe failed:", err)
	}
	fmt.Println("✓ Subscribed to AAPL, TSLA, NVDA")
	fmt.Println("Listening for 30 seconds...")

	// Forward updates to Redis
	go func() {
		for state := range out {
			rw.Enqueue(state)
			fmt.Printf("  tick: %s $%.2f\n", state.Ticker, state.Price)
		}
	}()

	time.Sleep(30 * time.Second)
	fmt.Println("Done.")
}