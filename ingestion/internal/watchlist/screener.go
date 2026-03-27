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
	"sort"
	"strings"
	"time"
)

// ScreenerClient makes authenticated HTTP requests to the Alpaca Data API.
// It is used exclusively by Manager.build() — callers must not share it across
// goroutines without external synchronisation (http.Client is safe, but the
// struct holds no mutable state so it is effectively safe to share).
type ScreenerClient struct {
	apiKey    string
	apiSecret string
	baseURL   string // "https://data.alpaca.markets"
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

// screenerUniverse is a hardcoded list of ~100 well-known volatile symbols
// used as the candidate pool for FetchMovers and FetchMostActives.
// The paid-tier /v2/screener/stocks/movers and /most-actives endpoints return
// 404 on the free plan, so we fetch snapshots for this universe instead and
// rank them by change percent or volume locally.
var screenerUniverse = []string{
	// Technology
	"AAPL", "MSFT", "GOOG", "AMZN", "NVDA", "META", "TSLA", "AMD", "INTC",
	"MU", "SNAP", "UBER", "LYFT", "SQ", "SHOP", "NET", "CRWD", "DDOG",
	"PLTR", "RBLX", "HOOD", "COIN", "MARA", "RIOT", "SMCI",
	// Healthcare / Biotech
	"MRNA", "BNTX", "PFE", "JNJ", "ABBV", "BMY", "LLY", "NVO",
	"AMGN", "GILD", "BIIB", "REGN", "VRTX",
	// Energy
	"XOM", "CVX", "OXY", "DVN", "FANG", "MRO", "HAL", "SLB",
	// Financials
	"JPM", "BAC", "GS", "MS", "C", "WFC", "SCHW", "SOFI",
	// Consumer
	"NKE", "SBUX", "MCD", "DIS", "NFLX", "ROKU", "ABNB", "DASH",
	"WMT", "TGT", "COST",
	// Industrials
	"BA", "CAT", "DE", "GE", "LMT", "RTX", "UPS",
	// Materials
	"FCX", "NEM", "CLF", "X", "AA",
	// Communication
	"T", "VZ", "TMUS",
	// Volatile / meme / small-mid cap
	"GME", "AMC", "NIO", "LCID", "RIVN", "SPCE", "OPEN",
	"IONQ", "AFRM", "UPST", "DNA", "LAZR", "PLUG", "FCEL",
	"TTOO", "SOUN", "VLD", "NKLA", "GOEV", "WKHS",
	"CLOV", "WISH", "BB", "NOK", "TLRY", "SNDL",
}

// ── Internal JSON unmarshal targets (unexported) ─────────────────────────────

// snapshotData maps the Alpaca /v2/stocks/snapshots response per symbol.
// Only the fields needed for ranking (price, change, volume) are extracted.
type snapshotData struct {
	LatestTrade struct {
		Price float64 `json:"p"`
	} `json:"latestTrade"`
	DailyBar struct {
		Volume int64   `json:"v"`
		Close  float64 `json:"c"`
	} `json:"dailyBar"`
	PrevDailyBar struct {
		Close float64 `json:"c"`
	} `json:"prevDailyBar"`
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

// FetchMovers fetches snapshots for the screenerUniverse, computes each
// symbol's intraday change percent, and returns those passing the filters:
//
//	1.0 ≤ price ≤ 50.0  AND  |changePercent| ≥ 8.0
//
// Both gainers and losers are included because the Z-score engine uses absolute
// value thresholds — crashes are valid signals (see ARCHITECTURE.md §5.2).
//
// This replaces the paid-tier /v2/screener/stocks/movers endpoint which
// returns 404 on Alpaca's free plan.
func (s *ScreenerClient) FetchMovers(ctx context.Context) ([]MoverResult, error) {
	snapshots, err := s.fetchSnapshots(ctx, screenerUniverse)
	if err != nil {
		return nil, fmt.Errorf("FetchMovers: %w", err)
	}

	var results []MoverResult
	for symbol, snap := range snapshots {
		price := snap.LatestTrade.Price
		prevClose := snap.PrevDailyBar.Close

		// Guard against division by zero — new listing with no prev close.
		if prevClose == 0 {
			continue
		}

		changePct := ((price - prevClose) / prevClose) * 100.0

		if price < 1.0 || price > 50.0 {
			continue
		}
		if math.Abs(changePct) < 8.0 {
			continue
		}

		results = append(results, MoverResult{
			Symbol:        symbol,
			Price:         price,
			ChangePercent: changePct,
		})
	}

	// Sort by absolute change percent descending so the biggest movers come first.
	sort.Slice(results, func(i, j int) bool {
		return math.Abs(results[i].ChangePercent) > math.Abs(results[j].ChangePercent)
	})

	s.logger.Printf("FetchMovers: %d results after filter (from %d snapshots)", len(results), len(snapshots))
	return results, nil
}

// FetchMostActives fetches snapshots for the screenerUniverse and returns
// the top 30 symbols ranked by today's volume (dailyBar.v) descending.
// Returns all results unfiltered — the relative-volume filter is applied in
// manager.go build() when merging with movers and seed tickers.
//
// This replaces the paid-tier /v2/screener/stocks/most-actives endpoint
// which returns 404 on Alpaca's free plan.
func (s *ScreenerClient) FetchMostActives(ctx context.Context) ([]MoverResult, error) {
	snapshots, err := s.fetchSnapshots(ctx, screenerUniverse)
	if err != nil {
		return nil, fmt.Errorf("FetchMostActives: %w", err)
	}

	// Collect all symbols with their snapshot data for sorting.
	type entry struct {
		symbol string
		snap   snapshotData
	}
	entries := make([]entry, 0, len(snapshots))
	for symbol, snap := range snapshots {
		entries = append(entries, entry{symbol: symbol, snap: snap})
	}

	// Sort by daily volume descending — most traded symbols first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].snap.DailyBar.Volume > entries[j].snap.DailyBar.Volume
	})

	// Take top 30.
	const maxResults = 30
	if len(entries) > maxResults {
		entries = entries[:maxResults]
	}

	results := make([]MoverResult, 0, len(entries))
	for _, e := range entries {
		price := e.snap.LatestTrade.Price
		prevClose := e.snap.PrevDailyBar.Close
		var changePct float64
		if prevClose != 0 {
			changePct = ((price - prevClose) / prevClose) * 100.0
		}
		results = append(results, MoverResult{
			Symbol:        e.symbol,
			Price:         price,
			ChangePercent: changePct,
		})
	}

	s.logger.Printf("FetchMostActives: %d results (from %d snapshots)", len(results), len(snapshots))
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

// fetchSnapshots calls GET /v2/stocks/snapshots?symbols=... for the given
// symbol list and returns a map of symbol → snapshotData.
// This is a free-tier Alpaca endpoint that returns latest trade, daily bar,
// and previous daily bar for each requested symbol.
func (s *ScreenerClient) fetchSnapshots(ctx context.Context, symbols []string) (map[string]snapshotData, error) {
	// Join all symbols into a comma-separated query parameter.
	// The snapshots endpoint accepts up to ~200 symbols per call.
	params := url.Values{}
	params.Set("symbols", strings.Join(symbols, ","))

	endpoint := fmt.Sprintf("%s/v2/stocks/snapshots?%s", s.baseURL, params.Encode())

	body, err := s.doGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetchSnapshots: %w", err)
	}

	// The response is a JSON object keyed by symbol.
	var snapshots map[string]snapshotData
	if err := json.Unmarshal(body, &snapshots); err != nil {
		return nil, fmt.Errorf("fetchSnapshots: unmarshal: %w", err)
	}

	return snapshots, nil
}

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
