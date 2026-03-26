package redis

import (
    "context"
    "testing"
)

func TestReasonCacheSetAndGet(t *testing.T) {
    // Skip if Redis not running
    rc, err := NewReasonCache("redis://localhost:6379")
    if err != nil {
        t.Skip("Redis not available — skipping integration test")
    }
    defer rc.client.Close()

    ctx := context.Background()
    ticker := "AMTX-TEST"
    reason := "Test reason for AMTX."

    err = rc.SetReason(ctx, ticker, reason)
    if err != nil {
        t.Fatal("SetReason failed:", err)
    }

    got, ok := rc.GetReason(ctx, ticker)
    if !ok {
        t.Fatal("GetReason returned false — key not found")
    }
    if got != reason {
        t.Errorf("expected %q, got %q", reason, got)
    }

    // Cleanup
    rc.client.Del(ctx, "reasons:"+ticker+":*")
}

func TestReasonCacheMissReturnsEmpty(t *testing.T) {
    rc, err := NewReasonCache("redis://localhost:6379")
    if err != nil {
        t.Skip("Redis not available — skipping integration test")
    }
    defer rc.client.Close()

    ctx := context.Background()
    got, ok := rc.GetReason(ctx, "TICKER-THAT-DOES-NOT-EXIST")

    if ok {
        t.Error("expected false for missing key")
    }
    if got != "" {
        t.Errorf("expected empty string, got %q", got)
    }
}