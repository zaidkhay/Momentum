// Package supabase implements the Supabase REST API client for the ingestion
// service. One SupabaseClient struct satisfies all four interfaces used by
// the watchlist manager, Hopeful promoter, reason pipeline, and tick processor.
//
// Uses standard library net/http only — no external Supabase SDK.
// All writes go through PostgREST's REST endpoint with upsert via the
// Prefer: resolution=merge-duplicates header.
//
// See ARCHITECTURE.md §8  — Supabase schema.
// See ARCHITECTURE.md §3.2 — supabaseWriter goroutine.
package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"momentum/ingestion/internal/types"
)

// SupabaseClient makes authenticated HTTP requests to the Supabase PostgREST API.
// A single instance satisfies:
//   - watchlist.SupabaseWriter  (UpsertAvgVolume)
//   - hopeful.HopefulLogger     (LogWatchlistEvent)
//   - reasons.ReasonStorer      (StoreReason)
//   - direct call               (WriteSignal)
type SupabaseClient struct {
	url        string // e.g. "https://xxx.supabase.co"
	serviceKey string
	// httpClient has a 10-second timeout so a slow Supabase response cannot
	// stall the ingestion pipeline. All Supabase writes are best-effort.
	httpClient *http.Client
	logger     *log.Logger
}

// NewSupabaseClient initialises a SupabaseClient. No connectivity check is
// performed — PostgREST errors surface on first write.
func NewSupabaseClient(url, serviceKey string) *SupabaseClient {
	return &SupabaseClient{
		url:        url,
		serviceKey: serviceKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     log.New(os.Stderr, "[supabase] ", log.LstdFlags),
	}
}

// ── Interface implementations ─────────────────────────────────────────────────

// UpsertAvgVolume upserts a row into the avg_volumes table.
// Satisfies watchlist.SupabaseWriter.
// On conflict (ticker) the avg_volume and updated_at columns are updated.
// See ARCHITECTURE.md §8 — avg_volumes table schema.
func (s *SupabaseClient) UpsertAvgVolume(ctx context.Context, ticker string, avgVol int64) error {
	body := map[string]interface{}{
		"ticker":     ticker,
		"avg_volume": avgVol,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	return s.supabaseRequest(ctx, http.MethodPost, "avg_volumes", body, map[string]string{
		// Prefer: resolution=merge-duplicates enables PostgREST upsert.
		// Without this header, a duplicate ticker would return 409 Conflict.
		"Prefer": "resolution=merge-duplicates",
	})
}

// LogWatchlistEvent inserts a row into the watchlist_log table.
// Satisfies hopeful.HopefulLogger.
// See ARCHITECTURE.md §8 — watchlist_log table schema.
func (s *SupabaseClient) LogWatchlistEvent(ctx context.Context, ticker, action, reason string) error {
	body := map[string]interface{}{
		"ticker":    ticker,
		"action":    action,
		"reason":    reason,
		"logged_at": time.Now().UTC().Format(time.RFC3339),
	}
	return s.supabaseRequest(ctx, http.MethodPost, "watchlist_log", body, nil)
}

// StoreReason upserts a row into the reasons table.
// Satisfies reasons.ReasonStorer.
// The composite primary key (ticker, trade_date) is used for upsert.
// trade_date uses ET (America/New_York) so the key aligns with the trading day.
// See ARCHITECTURE.md §8 — reasons table schema.
func (s *SupabaseClient) StoreReason(ctx context.Context, ticker, reason string, headlines []string) error {
	// Compute today's date in ET for the trade_date column.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return fmt.Errorf("StoreReason: load timezone: %w", err)
	}
	tradeDate := time.Now().In(loc).Format("2006-01-02")

	// headlines is stored as a JSONB column — marshal to a JSON array.
	headlinesJSON, err := json.Marshal(headlines)
	if err != nil {
		return fmt.Errorf("StoreReason: marshal headlines: %w", err)
	}

	body := map[string]interface{}{
		"ticker":     ticker,
		"trade_date": tradeDate,
		"reason":     reason,
		"headlines":  json.RawMessage(headlinesJSON),
	}
	return s.supabaseRequest(ctx, http.MethodPost, "reasons", body, map[string]string{
		"Prefer": "resolution=merge-duplicates",
	})
}

// WriteSignal inserts a row into the signals table.
// Called directly from the tickProcessor goroutine (in a separate goroutine
// via `go sb.WriteSignal(...)` so it never blocks the hot path).
// See ARCHITECTURE.md §8 — signals table schema.
func (s *SupabaseClient) WriteSignal(ctx context.Context, signal types.Signal) error {
	body := map[string]interface{}{
		"ticker":     signal.Ticker,
		"sector":     signal.Sector,
		"price":      signal.Price,
		"change_pct": signal.ChangePercent,
		"z_score":    signal.Z,
		"rel_vol":    signal.RelVol,
		"is_hopeful": signal.IsHopeful,
		"fired_at":   signal.DetectedAt.UTC().Format(time.RFC3339),
	}
	return s.supabaseRequest(ctx, http.MethodPost, "signals", body, nil)
}

// ── Private helper ────────────────────────────────────────────────────────────

// supabaseRequest builds and executes an authenticated HTTP request against
// the Supabase PostgREST API.
//
// PostgREST authentication requires two headers on every request:
//   - apikey: the service-role key (identifies the project)
//   - Authorization: Bearer {key} (authenticates the request)
//
// The optional extraHeaders map allows callers to set the Prefer header for
// upsert behaviour without duplicating auth logic.
func (s *SupabaseClient) supabaseRequest(
	ctx context.Context,
	method string,
	table string,
	body interface{},
	extraHeaders map[string]string,
) error {
	// Marshal body to JSON for the request payload.
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("supabaseRequest: marshal body for %s: %w", table, err)
	}

	endpoint := fmt.Sprintf("%s/rest/v1/%s", s.url, table)

	// http.NewRequestWithContext attaches ctx so the request is cancelled
	// on context expiry (e.g., service shutdown or httpClient timeout).
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("supabaseRequest: build request for %s: %w", table, err)
	}

	// PostgREST auth headers — required on every request.
	req.Header.Set("apikey", s.serviceKey)
	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("Content-Type", "application/json")

	// Apply extra headers (e.g., Prefer: resolution=merge-duplicates for upserts).
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("supabaseRequest: %s %s: %w", method, table, err)
	}
	// defer resp.Body.Close() returns the HTTP connection to the pool.
	defer resp.Body.Close()

	// PostgREST returns 2xx on success (200 for SELECT, 201 for INSERT/upsert).
	// Any non-2xx status is an error.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabaseRequest: %s %s returned HTTP %d: %s",
			method, table, resp.StatusCode, string(respBody))
	}

	return nil
}
