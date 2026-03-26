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