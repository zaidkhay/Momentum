// finnhub.go — Finnhub company-news API client.
// No pipeline logic lives here — this file only fetches and parses headlines.
//
// See ARCHITECTURE.md §6.1 — Finnhub as the news source for reason generation.
package reasons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"
)

// FinnhubClient makes authenticated HTTP requests to the Finnhub REST API.
// httpClient has a 10-second timeout to prevent slow Finnhub responses from
// stalling the reason generation worker goroutine.
type FinnhubClient struct {
	apiKey     string
	baseURL    string      // "https://finnhub.io/api/v1"
	httpClient *http.Client
	logger     *log.Logger
}

// NewsArticle maps one element of the Finnhub /company-news response array.
// Only Headline and Datetime are used downstream; Summary and Source are
// retained for potential future use.
type NewsArticle struct {
	Headline string `json:"headline"`
	Summary  string `json:"summary"`
	Source   string `json:"source"`
	Datetime int64  `json:"datetime"` // Unix timestamp — used for recency sort
}

// NewFinnhubClient initialises a FinnhubClient with a 10-second HTTP timeout.
func NewFinnhubClient(apiKey string) *FinnhubClient {
	return &FinnhubClient{
		apiKey:  apiKey,
		baseURL: "https://finnhub.io/api/v1",
		// &http.Client{Timeout} applies to the full request lifecycle
		// (dial + TLS handshake + read body). After 10s the request is cancelled.
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     log.New(os.Stderr, "[finnhub] ", log.LstdFlags),
	}
}

// FetchHeadlines fetches company news for ticker over the last two calendar
// days (yesterday → today UTC), sorts articles by recency, and returns the
// headline strings of the top 3 most recent articles.
//
// Returns an empty slice (not an error) when Finnhub has no articles for the
// ticker — the pipeline continues with the no-news Claude prompt in that case.
// Never returns more than 3 headlines.
func (f *FinnhubClient) FetchHeadlines(ctx context.Context, ticker string) ([]string, error) {
	// Date range: yesterday → today (UTC).
	// Finnhub requires YYYY-MM-DD format for the from/to query parameters.
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// url.Values encodes query parameters safely — handles special chars in ticker.
	params := url.Values{}
	params.Set("symbol", ticker)
	params.Set("from", yesterday)
	params.Set("to", today)
	params.Set("token", f.apiKey) // Finnhub auth uses a query parameter, not a header

	endpoint := f.baseURL + "/company-news?" + params.Encode()

	// http.NewRequestWithContext attaches ctx so the request is cancelled
	// if ctx expires (e.g., pipeline worker shuts down) or the 10s client
	// timeout fires — whichever comes first.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("FetchHeadlines(%s): build request: %w", ticker, err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FetchHeadlines(%s): execute request: %w", ticker, err)
	}
	// defer resp.Body.Close() returns the connection to the pool even on error.
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FetchHeadlines(%s): HTTP %d", ticker, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("FetchHeadlines(%s): read body: %w", ticker, err)
	}

	var articles []NewsArticle
	if err := json.Unmarshal(body, &articles); err != nil {
		return nil, fmt.Errorf("FetchHeadlines(%s): unmarshal: %w", ticker, err)
	}

	// Empty response is not an error — new listing or no news today.
	if len(articles) == 0 {
		f.logger.Printf("FetchHeadlines(%s): no articles found", ticker)
		return []string{}, nil
	}

	// Sort descending by Datetime so the most recent article is first.
	// sort.Slice uses an in-place unstable sort — fine for headline ordering.
	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Datetime > articles[j].Datetime
	})

	// Take only the top 3 headlines — Claude prompt is capped at 30 words
	// and long context does not improve one-sentence output quality.
	const maxHeadlines = 3
	if len(articles) > maxHeadlines {
		articles = articles[:maxHeadlines]
	}

	headlines := make([]string, 0, len(articles))
	for _, a := range articles {
		if a.Headline != "" {
			headlines = append(headlines, a.Headline)
		}
	}

	f.logger.Printf("FetchHeadlines(%s): returning %d headlines", ticker, len(headlines))
	return headlines, nil
}
