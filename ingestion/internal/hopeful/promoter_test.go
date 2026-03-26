package hopeful

import (
    "context"
    "testing"
    "time"

    "momentum/ingestion/internal/types"
)

// mockWatchlist simulates the WatchlistPromoter interface for testing
type mockWatchlist struct {
    promoted bool
}

func (m *mockWatchlist) PromoteToHopeful(ticker string) {
    m.promoted = true
}

// mockLogger simulates the HopefulLogger interface for testing
type mockLogger struct{}

func (m *mockLogger) LogWatchlistEvent(
    ctx context.Context,
    ticker string,
    action string,
    reason string,
) error {
    return nil
}

func TestMeetsAllCriteriaPass(t *testing.T) {
    signal := types.Signal{
        Z:             3.8,
        RelVol:        6.2,
        ChangePercent: 34.0,
    }
    state := &types.SymbolState{
        Price: 2.84,
    }
    if !meetsAllCriteria(signal, state) {
        t.Error("expected promotion criteria to pass")
    }
}
func TestFailsOnHighPrice(t *testing.T) {
    signal := types.Signal{Z: 3.8, RelVol: 6.2, ChangePercent: 34.0}
    state := &types.SymbolState{Price: 25.0} // above $20 limit
    if meetsAllCriteria(signal, state) {
        t.Error("expected failure: price too high")
    }
}

func TestFailsOnLowZScore(t *testing.T) {
    signal := types.Signal{Z: 2.1, RelVol: 6.2, ChangePercent: 34.0}
    state := &types.SymbolState{Price: 2.84}
    if meetsAllCriteria(signal, state) {
        t.Error("expected failure: Z-score too low")
    }
}

func TestFailsOnLowRelVol(t *testing.T) {
    signal := types.Signal{Z: 3.8, RelVol: 1.2, ChangePercent: 34.0}
    state := &types.SymbolState{Price: 2.84}
    if meetsAllCriteria(signal, state) {
        t.Error("expected failure: RelVol too low")
    }
}

func TestFailsOnLowChangePercent(t *testing.T) {
    signal := types.Signal{Z: 3.8, RelVol: 6.2, ChangePercent: 5.0}
    state := &types.SymbolState{Price: 2.84}
    if meetsAllCriteria(signal, state) {
        t.Error("expected failure: change percent too low")
    }
}

func TestNegativeZScorePromotes(t *testing.T) {
    signal := types.Signal{
        Z:             -3.8,
        RelVol:        6.2,
        ChangePercent: -34.0,
    }
    state := &types.SymbolState{Price: 4.20}
    if !meetsAllCriteria(signal, state) {
        t.Error("expected negative Z to still pass criteria")
    }
}
func TestEvaluateSetsIsHopeful(t *testing.T) {
    mock := &mockWatchlist{}
    p := NewPromoter(mock, &mockLogger{})

    signal := types.Signal{
        Ticker:        "AMTX",
        Z:             3.8,
        RelVol:        6.2,
        ChangePercent: 34.0,
        Sector:        "Hopeful",
    }
    state := &types.SymbolState{
        Ticker: "AMTX",
        Price:  2.84,
    }

    promoted := p.Evaluate(signal, state)

    if !promoted {
        t.Error("expected Evaluate to return true")
    }
    if !state.IsHopeful {
        t.Error("expected state.IsHopeful to be true after promotion")
    }
    if !mock.promoted {
        t.Error("expected watchlist.PromoteToHopeful to be called")
    }
}

func TestIsHopefulAfterEvaluate(t *testing.T) {
    p := NewPromoter(&mockWatchlist{}, &mockLogger{})

    signal := types.Signal{
        Ticker: "IMRX", Z: 3.8,
        RelVol: 6.2, ChangePercent: 34.0,
    }
    state := &types.SymbolState{Ticker: "IMRX", Price: 4.20}
    p.Evaluate(signal, state)

    if !p.IsHopeful("IMRX") {
        t.Error("expected IMRX to be hopeful after evaluation")
    }

    tickers := p.GetHopefulTickers()
    if len(tickers) != 1 || tickers[0] != "IMRX" {
        t.Errorf("expected [IMRX], got %v", tickers)
    }
}

func TestDemoteRemovesTicker(t *testing.T) {
    p := NewPromoter(&mockWatchlist{}, &mockLogger{})

    signal := types.Signal{
        Ticker: "AMTX", Z: 3.8,
        RelVol: 6.2, ChangePercent: 34.0,
    }
    state := &types.SymbolState{Ticker: "AMTX", Price: 2.84}
    p.Evaluate(signal, state)

    p.Demote("AMTX")

    if p.IsHopeful("AMTX") {
        t.Error("expected AMTX to be removed after demotion")
    }
}

func TestRefreshResetsTimer(t *testing.T) {
    p := NewPromoter(&mockWatchlist{}, &mockLogger{})

    // Manually seed hopeful map with an old timestamp
    p.mu.Lock()
    p.hopeful["AMTX"] = time.Now().Add(-25 * time.Minute)
    p.mu.Unlock()

    // Refresh should reset the clock
    p.RefreshHopeful("AMTX")

    p.mu.RLock()
    promotedAt := p.hopeful["AMTX"]
    p.mu.RUnlock()

    if time.Since(promotedAt) > time.Second {
        t.Error("expected promotedAt to be reset to now")
    }
}