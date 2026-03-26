package supabase

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "momentum/ingestion/internal/types"
)

func TestWriteSignalSetsHeaders(t *testing.T) {
    var capturedHeaders http.Header

    server := httptest.NewServer(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            capturedHeaders = r.Header.Clone()
            w.WriteHeader(http.StatusCreated)
        }))
    defer server.Close()

    client := &SupabaseClient{
        url:        server.URL,
        serviceKey: "test-key",
        httpClient: server.Client(),
    }

    err := client.WriteSignal(
        context.Background(),
        types.Signal{
            Ticker: "AMTX",
            Z:      3.8,
        },
    )

    if err != nil {
        t.Fatal("unexpected error:", err)
    }
    if capturedHeaders.Get("apikey") == "" {
        t.Error("missing apikey header")
    }
    if capturedHeaders.Get("Authorization") == "" {
        t.Error("missing Authorization header")
    }
}

func TestStoreReasonUpserts(t *testing.T) {
    var capturedBody map[string]interface{}

    server := httptest.NewServer(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            json.NewDecoder(r.Body).Decode(&capturedBody)
            w.WriteHeader(http.StatusCreated)
        }))
    defer server.Close()

    client := &SupabaseClient{
        url:        server.URL,
        serviceKey: "test-key",
        httpClient: server.Client(),
    }

    err := client.StoreReason(
        context.Background(),
        "AMTX",
        "DOE grant triggered short squeeze.",
        []string{"DOE awards grant"},
    )

    if err != nil {
        t.Fatal("unexpected error:", err)
    }
    if capturedBody["ticker"] != "AMTX" {
        t.Errorf("expected ticker AMTX, got %v",
            capturedBody["ticker"])
    }
    if capturedBody["reason"] == "" {
        t.Error("expected reason in body")
    }
}

func TestUpsertAvgVolume(t *testing.T) {
    called := false

    server := httptest.NewServer(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            called = true
            w.WriteHeader(http.StatusCreated)
        }))
    defer server.Close()

    client := &SupabaseClient{
        url:        server.URL,
        serviceKey: "test-key",
        httpClient: server.Client(),
    }

    err := client.UpsertAvgVolume(
        context.Background(), "AAPL", 45000000)

    if err != nil {
        t.Fatal("unexpected error:", err)
    }
    if !called {
        t.Error("expected HTTP request to be made")
    }
}