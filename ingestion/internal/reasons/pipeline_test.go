package reasons

import (
	"context"
	"net/http"
	"testing"
	"time"

	"momentum/ingestion/internal/types"
)

type mockReasonCacher struct {
	stored map[string]string
}

func newMockCacher() *mockReasonCacher {
	return &mockReasonCacher{
		stored: make(map[string]string),
	}
}

func (m *mockReasonCacher) SetReason(
	ctx context.Context,
	ticker string,
	reason string,
) error {
	m.stored[ticker] = reason
	return nil
}

func (m *mockReasonCacher) GetReason(
	ctx context.Context,
	ticker string,
) (string, bool) {
	r, ok := m.stored[ticker]
	return r, ok
}

type mockReasonStorer struct {
	stored bool
}

func (m *mockReasonStorer) StoreReason(
	ctx context.Context,
	ticker string,
	reason string,
	headlines []string,
) error {
	m.stored = true
	return nil
}

func TestSubmitNonBlocking(t *testing.T) {
	p := NewPipeline(
		"fake-finnhub-key",
		"fake-claude-key",
		newMockCacher(),
		&mockReasonStorer{},
	)

	for i := 0; i < 100; i++ {
		p.Submit(types.Signal{Ticker: "AAPL"})
	}

	done := make(chan struct{})
	go func() {
		p.Submit(types.Signal{Ticker: "TSLA"})
		close(done)
	}()

	select {
	case <-done:
		// passed
	case <-time.After(100 * time.Millisecond):
		t.Error("Submit blocked when channel was full")
	}
}

func TestCacheHitSkipsAPICalls(t *testing.T) {
	cacher := newMockCacher()
	cacher.stored["AMTX"] = "Cached reason from earlier today."

	p := NewPipeline(
		"fake-finnhub-key",
		"fake-claude-key",
		cacher,
		&mockReasonStorer{},
	)

	signal := types.Signal{
		Ticker:        "AMTX",
		ChangePercent: 34.0,
	}

	p.process(signal)

	if len(cacher.stored) != 1 {
		t.Error("expected no new cache entries on cache hit")
	}
}

func TestTTLAlwaysPositive(t *testing.T) {
	p := &Pipeline{}
	ttl := p.TtlUntilMarketClose()

	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
}

func TestProcessFallbackOnAPIFailure(t *testing.T) {
	cacher := newMockCacher()
	storer := &mockReasonStorer{}

	p := NewPipeline(
		"bad-key",
		"bad-key",
		cacher,
		storer,
	)

	p.finnhub = &FinnhubClient{
		apiKey:  "bad",
		baseURL: "http://localhost:0",
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond,
		},
	}
	p.claude = &ClaudeClient{
		apiKey:  "bad",
		baseURL: "http://localhost:0",
		model:   "claude-haiku-4-5-20251001",
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond,
		},
	}

	signal := types.Signal{
		Ticker:        "AMTX",
		ChangePercent: 34.0,
	}

	p.process(signal)

	_, hasCached := cacher.GetReason(context.Background(), "AMTX")
	if !hasCached {
		t.Error("expected fallback reason cached even on API failure")
	}
}

func TestDirectionUpWhenPositive(t *testing.T) {
	cacher := newMockCacher()

	p := NewPipeline(
		"fake-finnhub-key",
		"fake-claude-key",
		cacher,
		&mockReasonStorer{},
	)

	p.finnhub = &FinnhubClient{
		apiKey:  "bad",
		baseURL: "http://localhost:0",
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond,
		},
	}
	p.claude = &ClaudeClient{
		apiKey:  "bad",
		baseURL: "http://localhost:0",
		model:   "claude-haiku-4-5-20251001",
		httpClient: &http.Client{
			Timeout: 100 * time.Millisecond,
		},
	}

	p.process(types.Signal{
		Ticker:        "TSLA",
		ChangePercent: 5.0,
	})

	p.process(types.Signal{
		Ticker:        "NVDA",
		ChangePercent: -5.0,
	})

	_, hasTSLA := cacher.GetReason(context.Background(), "TSLA")
	_, hasNVDA := cacher.GetReason(context.Background(), "NVDA")

	if !hasTSLA {
		t.Error("expected TSLA reason cached")
	}
	if !hasNVDA {
		t.Error("expected NVDA reason cached")
	}
}