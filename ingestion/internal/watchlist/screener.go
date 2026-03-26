// screener.go — Alpaca HTTP screener API calls only. No watchlist logic.
// All methods return raw results; filtering decisions live in manager.go.
//
// See ARCHITECTURE.md §3.2 — watchlistRefresher goroutine uses this client.
// See WINDSURF.md §External APIs — Alpaca Data API base URL.
package watchlist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ScreenerClient makes authenticated HTTP requests to the Alpaca Data API.
// It is used exclusively by Manager.build() — callers must not share it across
// goroutines without external synchronisation (http.Client is safe, but the
// struct holds no mutable state so it is effectively safe to share).
type ScreenerClient struct {
	apiKey    string
	apiSecret string
	baseURL   string      // "https://data.alpaca.markets"
	// httpClient has a hard 10-second timeout to prevent slow Alpaca responses
	// from stalling the 5-minute refresh loop.
	httpClient *http.Client
	logger     *log.Logger
}

// MoverResult is a screener result returned to manager.go after filtering.
// See ARCHITECTURE.md §3.2 — watchlistRefresher uses movers to build the set.
type MoverResult struct {
	Symbol        string
	Price         float64
	ChangePercent float64
}

// ── Internal JSON unmarshal targets (unexported) ─────────────────────────────

type moversResponse struct {
	Gainers []moverEntry `json:"gainers"`
	Losers  []moverEntry `json:"losers"`
}

type mostActivesResponse struct {
	MostActives []moverEntry `json:"most_actives"`
}

type moverEntry struct {
	Symbol        string  `json:"symbol"`
	Price         float64 `json:"price"`
	ChangePercent float64 `json:"percent_change"`
}

type barsResponse struct {
	Bars []struct {
		Volume int64 `json:"v"`
	} `json:"bars"`
}

// ── Constructor ──────────────────────────────────────────────────────────────

// NewScreenerClient initialises a ScreenerClient with a 10-second HTTP timeout.
func NewScreenerClient(apiKey, apiSecret string) *ScreenerClient {
	return &ScreenerClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   "https://data.alpaca.markets",
		// &http.Client{Timeout} applies to the entire request lifecycle
		// (dial + TLS + headers + body). After 10s the request is cancelled.
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     log.New(os.Stderr, "[screener] ", log.LstdFlags),
	}
}

// ── Public methods ────────────────────────────────────────────────────────────

// FetchMovers calls /v2/screener/stocks/movers with top=50, merges gainers and
// losers into a single list, then applies the price and percent-change filters:
//
//	1.0 ≤ price ≤ 50.0  AND  |changePercent| ≥ 8.0
//
// Both gainers and losers are included because the Z-score engine uses absolute
// value thresholds — crashes are valid signals (see ARCHITECTURE.md §5.2).
func (s *ScreenerClient) FetchMovers(ctx context.Context) ([]MoverResult, error) {
	endpoint := s.baseURL + "/v2/screener/stocks/movers?top=50"

	body, err := s.doGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("FetchMovers: %w", err)
	}

	var resp moversResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("FetchMovers: unmarshal: %w", err)
	}

	// Merge gainers and losers into one slice for unified filtering.
	all := append(resp.Gainers, resp.Losers...)

	var results []MoverResult
	for _, e := range all {
		if e.Price < 1.0 || e.Price > 50.0 {
			continue
		}
		if math.Abs(e.ChangePercent) < 8.0 {
			continue
		}
		results = append(results, MoverResult{
			Symbol:        e.Symbol,
			Price:         e.Price,
			ChangePercent: e.ChangePercent,
		})
	}

	s.logger.Printf("FetchMovers: %d results after filter (from %d total)", len(results), len(all))
	return results, nil
}

// FetchMostActives calls /v2/screener/stocks/most-actives with top=30, by=trades.
// Returns all results unfiltered — the relative-volume filter is applied in
// manager.go build() when merging with movers and seed tickers.
func (s *ScreenerClient) FetchMostActives(ctx context.Context) ([]MoverResult, error) {
	endpoint := s.baseURL + "/v2/screener/stocks/most-actives?top=30&by=trades"

	body, err := s.doGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("FetchMostActives: %w", err)
	}

	var resp mostActivesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("FetchMostActives: unmarshal: %w", err)
	}

	results := make([]MoverResult, 0, len(resp.MostActives))
	for _, e := range resp.MostActives {
		results = append(results, MoverResult{
			Symbol:        e.Symbol,
			Price:         e.Price,
			ChangePercent: e.ChangePercent,
		})
	}

	s.logger.Printf("FetchMostActives: %d results", len(results))
	return results, nil
}

// FetchAvgVolume fetches up to 30 daily OHLCV bars for ticker over the last
// 30 calendar days and returns the mean daily volume as int64.
//
// Returns (0, nil) if the API returns no bars — this covers new listings or
// symbols with insufficient history. The zero value is stored in avgVolumes
// so we don't re-fetch on every refresh cycle.
func (s *ScreenerClient) FetchAvgVolume(ctx context.Context, ticker string) (int64, error) {
	// Build date range: 30 days ago → yesterday (UTC midnight boundaries).
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -30).Format(time.RFC3339)
	end := now.AddDate(0, 0, -1).Format(time.RFC3339)

	// url.QueryEscape handles any special characters in the ticker symbol.
	params := url.Values{}
	params.Set("timeframe", "1Day")
	params.Set("start", start)
	params.Set("end", end)
	params.Set("limit", "30")

	endpoint := fmt.Sprintf("%s/v2/stocks/%s/bars?%s",
		s.baseURL, url.PathEscape(ticker), params.Encode())

	body, err := s.doGet(ctx, endpoint)
	if err != nil {
		return 0, fmt.Errorf("FetchAvgVolume(%s): %w", ticker, err)
	}

	var resp barsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("FetchAvgVolume(%s): unmarshal: %w", ticker, err)
	}

	if len(resp.Bars) == 0 {
		// New listing or data gap — return 0 so the caller can still store
		// the ticker in avgVolumes and skip re-fetching.
		return 0, nil
	}

	var total int64
	for _, bar := range resp.Bars {
		total += bar.Volume
	}
	return total / int64(len(resp.Bars)), nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// doGet builds an authenticated GET request, executes it using the shared
// httpClient, and returns the response body bytes.
// Authentication uses the APCA-API-KEY-ID / APCA-API-SECRET-KEY header pair
// required by all Alpaca Data API endpoints (WINDSURF.md §External APIs).
func (s *ScreenerClient) doGet(ctx context.Context, rawURL string) ([]byte, error) {
	// http.NewRequestWithContext attaches a context so the request is
	// cancelled automatically if ctx is Done (e.g., service shutdown or
	// a 10-second client timeout fires).
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("doGet: build request for %s: %w", rawURL, err)
	}

	req.Header.Set("APCA-API-KEY-ID", s.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", s.apiSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doGet: execute request for %s: %w", rawURL, err)
	}
	// defer resp.Body.Close() guarantees the HTTP connection is returned to
	// the pool even if reading fails — prevents connection leaks.
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doGet: %s returned HTTP %d", rawURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("doGet: read body for %s: %w", rawURL, err)
	}

	return body, nil
}
