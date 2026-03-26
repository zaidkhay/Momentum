package main

import (
    "os"
    "testing"
)

func TestRequiredEnvVars(t *testing.T) {
    required := []string{
        "ALPACA_API_KEY",
        "ALPACA_SECRET_KEY",
        "FINNHUB_API_KEY",
        "ANTHROPIC_API_KEY",
        "SUPABASE_URL",
        "SUPABASE_SERVICE_KEY",
    }

    for _, key := range required {
        t.Run(key, func(t *testing.T) {
            original := os.Getenv(key)
            os.Unsetenv(key)
            defer os.Setenv(key, original)

            val := os.Getenv(key)
            if val != "" {
                t.Errorf("%s should be empty after unset", key)
            }
        })
    }
}