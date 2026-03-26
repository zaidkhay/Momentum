package watchlist

import (
    "sort"
    "testing"
	"time"
	"context"
)

func TestDiffAddNew(t *testing.T) {
    current := map[string]bool{
        "AAPL": true,
        "TSLA": true,
    }
    next := []string{"AAPL", "TSLA", "NVDA"}

    add, remove := diff(current, next)

    if len(add) != 1 || add[0] != "NVDA" {
        t.Errorf("expected add=[NVDA], got %v", add)
    }
    if len(remove) != 0 {
        t.Errorf("expected remove=[], got %v", remove)
    }
}

func TestDiffRemoveDropped(t *testing.T) {
    current := map[string]bool{
        "AAPL": true,
        "TSLA": true,
        "NVDA": true,
    }
    next := []string{"AAPL", "TSLA"}

    add, remove := diff(current, next)

    if len(add) != 0 {
        t.Errorf("expected add=[], got %v", add)
    }
    if len(remove) != 1 || remove[0] != "NVDA" {
        t.Errorf("expected remove=[NVDA], got %v", remove)
    }
}

func TestDiffNoChange(t *testing.T) {
    current := map[string]bool{
        "AAPL": true,
        "TSLA": true,
    }
    next := []string{"AAPL", "TSLA"}

    add, remove := diff(current, next)

    if len(add) != 0 {
        t.Errorf("expected add=[], got %v", add)
    }
    if len(remove) != 0 {
        t.Errorf("expected remove=[], got %v", remove)
    }
}

func TestDiffMixedChanges(t *testing.T) {
    current := map[string]bool{
        "AAPL": true,
        "TSLA": true,
        "NVDA": true,
    }
    next := []string{"AAPL", "AMTX", "IMRX"}

    add, remove := diff(current, next)

    sort.Strings(add)
    sort.Strings(remove)

    if len(add) != 2 {
        t.Errorf("expected 2 additions, got %v", add)
    }
    if len(remove) != 2 {
        t.Errorf("expected 2 removals, got %v", remove)
    }
}

func TestDiffEmptyCurrent(t *testing.T) {
    current := map[string]bool{}
    next := []string{"AAPL", "TSLA", "NVDA"}

    add, remove := diff(current, next)

    if len(add) != 3 {
        t.Errorf("expected 3 additions, got %v", add)
    }
    if len(remove) != 0 {
        t.Errorf("expected no removals, got %v", remove)
    }
}

func TestDiffEmptyNext(t *testing.T) {
    current := map[string]bool{
        "AAPL": true,
        "TSLA": true,
    }
    next := []string{}

    add, remove := diff(current, next)

    if len(add) != 0 {
        t.Errorf("expected no additions, got %v", add)
    }
    if len(remove) != 2 {
        t.Errorf("expected 2 removals, got %v", remove)
    }
}
func TestSympathyMapNoDuplicates(t *testing.T) {
    for leader, peers := range SympathyMap {
        seen := map[string]bool{}
        for _, peer := range peers {
            if peer == leader {
                t.Errorf("%s lists itself as a sympathy peer", leader)
            }
            if seen[peer] {
                t.Errorf("%s has duplicate peer %s", leader, peer)
            }
            seen[peer] = true
        }
    }
}
func TestMarketHoursWeekday(t *testing.T) {
    // Monday 10:00am ET — should be open
    loc, _ := time.LoadLocation("America/New_York")
    monday := time.Date(2026, 3, 23, 10, 0, 0, 0, loc)
    if !isMarketOpen(monday) {
        t.Error("expected market open on Monday 10am ET")
    }
}

func TestMarketHoursWeekend(t *testing.T) {
    // Saturday — should be closed
    loc, _ := time.LoadLocation("America/New_York")
    saturday := time.Date(2026, 3, 21, 10, 0, 0, 0, loc)
    if isMarketOpen(saturday) {
        t.Error("expected market closed on Saturday")
    }
}

