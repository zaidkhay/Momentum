package hopeful

import (
    "testing"
    "momentum/ingestion/internal/types"
)

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