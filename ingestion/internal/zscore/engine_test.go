// Tests for the Z-score signal detection engine.
// Uses only the standard library — no external test frameworks.
// All tests are in the same package (package zscore) so they can access
// the private rollingStats function directly if needed.
//
// See ARCHITECTURE.md §5.2 — signal tier thresholds used in assertions.
package zscore

import (
	"math"
	"testing"

	"momentum/ingestion/internal/types"
)

// eps is the floating-point tolerance used for approximate equality checks.
// All computed returns are subject to IEEE-754 rounding at the 10^-15 level,
// so 1e-9 is conservative enough to never produce false failures.
const eps = 1e-9

// setupSignalState directly fills state's ring buffer with 10 values of
// +smallRet and 10 values of -smallRet, giving:
//
//	mean   = 0
//	stddev = smallRet  (population stddev of a symmetric ±smallRet distribution)
//
// Populating the window directly avoids accumulated floating-point drift from
// repeated ProcessTick calls, keeping expected Z values predictable.
// state.WindowIdx is set to 20 so the next ProcessTick overwrites slot 0.
func setupSignalState(state *types.SymbolState, smallRet float64) {
	for i := 0; i < 10; i++ {
		state.Window[i] = smallRet
		state.Window[i+10] = -smallRet
	}
	state.WindowIdx = 20
	state.Price = 100.0
	state.PrevClose = 100.0
	state.Ticker = "TEST"
}

// ── Individual test cases ────────────────────────────────────────────────────

// TestFirstTick verifies the first-tick guard (state.Price == 0).
// ProcessTick must set state.Price and return no signal.
func TestFirstTick(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{} // Price == 0

	sig, ok := e.ProcessTick(state, 150.0, 1000, 10000)

	if ok {
		t.Error("expected no signal on first tick, got one")
	}
	if sig.Ticker != "" {
		t.Errorf("expected empty Signal on first tick, got ticker=%q", sig.Ticker)
	}
	if state.Price != 150.0 {
		t.Errorf("expected state.Price=150.0 after first tick, got %v", state.Price)
	}
}

// TestNoSignalNormalMove verifies that a small return (Z ≈ 1.8) does not
// cross either threshold and returns (Signal{}, false).
func TestNoSignalNormalMove(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}
	setupSignalState(state, 0.001) // stddev = 0.001

	// ret = 0.002 → after window update, Z ≈ 1.8 (computed in plan notes)
	// RelVol = 100/1_000_000 = 0.0001 — well below any threshold
	newPrice := state.Price * (1 + 0.002)
	sig, ok := e.ProcessTick(state, newPrice, 100, 1_000_000)

	if ok {
		t.Errorf("expected no signal for normal move, got Signal{Z=%.4f, RelVol=%.4f}", sig.Z, sig.RelVol)
	}
}

// TestModerateSignal verifies the Moderate tier: |Z| > 2.5 AND RelVol > 2.0.
// Uses ret=0.004 which produces Z ≈ 2.93 after window update.
func TestModerateSignal(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}
	setupSignalState(state, 0.001) // stddev = 0.001

	// ret = 0.004 → Z ≈ 2.93 (> 2.5, < 3.0 → Moderate tier)
	// avgVolume=1000, newVolume=2001 → RelVol=2.001 > 2.0
	newPrice := state.Price * (1 + 0.004)
	sig, ok := e.ProcessTick(state, newPrice, 2001, 1000)

	if !ok {
		t.Fatal("expected Moderate signal, got none")
	}
	if math.Abs(sig.Z) <= 2.5 {
		t.Errorf("expected |Z| > 2.5, got Z=%.4f", sig.Z)
	}
	if math.Abs(sig.Z) > 3.0 {
		t.Errorf("expected Moderate (|Z| ≤ 3.0), but got |Z|=%.4f (Strong tier fired incorrectly)", sig.Z)
	}
	if sig.RelVol <= 2.0 {
		t.Errorf("expected RelVol > 2.0, got %.4f", sig.RelVol)
	}
	if sig.Ticker != "TEST" {
		t.Errorf("expected Ticker=TEST, got %q", sig.Ticker)
	}
	if sig.DetectedAt.IsZero() {
		t.Error("expected DetectedAt to be set, got zero value")
	}
}

// TestStrongSignal verifies the Strong tier: |Z| > 3.0 AND RelVol > 4.0.
// Uses ret=0.005 which produces Z ≈ 3.27 after window update.
func TestStrongSignal(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}
	setupSignalState(state, 0.001) // stddev = 0.001

	// ret = 0.005 → Z ≈ 3.27 (> 3.0 → Strong tier)
	// avgVolume=1000, newVolume=4001 → RelVol=4.001 > 4.0
	newPrice := state.Price * (1 + 0.005)
	sig, ok := e.ProcessTick(state, newPrice, 4001, 1000)

	if !ok {
		t.Fatal("expected Strong signal, got none")
	}
	if math.Abs(sig.Z) <= 3.0 {
		t.Errorf("expected |Z| > 3.0 for Strong tier, got Z=%.4f", sig.Z)
	}
	if sig.RelVol <= 4.0 {
		t.Errorf("expected RelVol > 4.0 for Strong tier, got %.4f", sig.RelVol)
	}
	if sig.Ticker != "TEST" {
		t.Errorf("expected Ticker=TEST, got %q", sig.Ticker)
	}
}

// TestNegativeZScore verifies that a large negative return (crash) also
// triggers a signal. The threshold check uses math.Abs(z), so direction
// should not matter.
func TestNegativeZScore(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}
	setupSignalState(state, 0.001) // stddev = 0.001; window mean ≈ 0

	// ret = -0.004 → Z ≈ -2.93 → |Z| > 2.5 → Moderate signal expected
	// (symmetric with TestModerateSignal, just negative direction)
	newPrice := state.Price * (1 - 0.004)
	sig, ok := e.ProcessTick(state, newPrice, 2001, 1000)

	if !ok {
		t.Fatal("expected signal on large negative return (crash detection), got none")
	}
	if sig.Z >= 0 {
		t.Errorf("expected negative Z for a price drop, got Z=%.4f", sig.Z)
	}
	if math.Abs(sig.Z) <= 2.5 {
		t.Errorf("expected |Z| > 2.5, got |Z|=%.4f", math.Abs(sig.Z))
	}
}

// TestZeroStddev verifies that a window of 20 identical returns does not
// cause a division-by-zero panic and returns no signal.
func TestZeroStddev(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}

	// Fill window with 20 identical values (stddev == 0).
	for i := range state.Window {
		state.Window[i] = 0.001
	}
	state.WindowIdx = 20
	state.Price = 100.0
	state.PrevClose = 100.0

	// Adding another identical return keeps stddev == 0.
	// After window[0] is overwritten with 0.001 again, all 20 remain 0.001.
	newPrice := state.Price * 1.001

	// Must not panic.
	sig, ok := e.ProcessTick(state, newPrice, 1000, 1000)

	if ok {
		t.Errorf("expected no signal when stddev==0, got Signal{Z=%.4f}", sig.Z)
	}
	// Price should still be updated despite the early return.
	if math.Abs(state.Price-newPrice) > eps {
		t.Errorf("expected state.Price updated to %.4f, got %.4f", newPrice, state.Price)
	}
}

// TestRelVolZeroAvg verifies that avgVolume==0 does not panic and sets
// state.RelVol to 0.
func TestRelVolZeroAvg(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{}
	setupSignalState(state, 0.001)

	// avgVolume == 0 → RelVol must be 0, no panic.
	newPrice := state.Price * 1.001
	_, _ = e.ProcessTick(state, newPrice, 500, 0)

	if state.RelVol != 0 {
		t.Errorf("expected RelVol==0 when avgVolume==0, got %.6f", state.RelVol)
	}
}

// TestRingBufferWraps verifies that after more than 20 ticks the ring buffer
// correctly overwrites the oldest values and WindowIdx advances past 20.
func TestRingBufferWraps(t *testing.T) {
	e := NewEngine()
	// Start with non-zero price to skip the first-tick guard.
	state := &types.SymbolState{
		Ticker: "WRAP",
		Price:  100.0,
	}

	// 20 ticks of ret ≈ 0.001 fill all 20 slots.
	for i := 0; i < 20; i++ {
		newPrice := state.Price * 1.001
		e.ProcessTick(state, newPrice, 100, 1_000_000)
	}

	if state.WindowIdx != 20 {
		t.Fatalf("after 20 ticks expected WindowIdx=20, got %d", state.WindowIdx)
	}

	// 5 more ticks of ret ≈ 0.002 overwrite slots 0-4.
	for i := 0; i < 5; i++ {
		newPrice := state.Price * 1.002
		e.ProcessTick(state, newPrice, 100, 1_000_000)
	}

	if state.WindowIdx != 25 {
		t.Fatalf("after 25 ticks expected WindowIdx=25, got %d", state.WindowIdx)
	}

	// Slots 0-4 should hold ≈0.002 (most recently overwritten).
	for i := 0; i < 5; i++ {
		if math.Abs(state.Window[i]-0.002) > eps {
			t.Errorf("window[%d] = %.15f, want ≈0.002 (tolerance %v)", i, state.Window[i], eps)
		}
	}

	// Slots 5-19 should still hold ≈0.001 from the first 20 fills.
	for i := 5; i < 20; i++ {
		if math.Abs(state.Window[i]-0.001) > eps {
			t.Errorf("window[%d] = %.15f, want ≈0.001 (tolerance %v)", i, state.Window[i], eps)
		}
	}
}

// TestChangePercentCalculation verifies the ChangePercent formula:
//
//	ChangePercent = (newPrice - PrevClose) / PrevClose * 100
func TestChangePercentCalculation(t *testing.T) {
	e := NewEngine()
	// Use a non-zero Price to skip first-tick guard.
	// Window is all zeros → stddev==0 path runs, but price fields ARE updated.
	state := &types.SymbolState{
		Price:    100.0,
		PrevClose: 100.0,
	}

	_, _ = e.ProcessTick(state, 110.0, 100, 10000)

	const want = 10.0 // (110 - 100) / 100 * 100 = 10.0
	if math.Abs(state.ChangePercent-want) > eps {
		t.Errorf("expected ChangePercent=%.4f, got %.4f", want, state.ChangePercent)
	}
}

// TestZeroPrevClose verifies that PrevClose==0 does not cause a
// division-by-zero panic and sets ChangePercent to 0.
func TestZeroPrevClose(t *testing.T) {
	e := NewEngine()
	state := &types.SymbolState{
		Price:    100.0,
		PrevClose: 0, // guard should set ChangePercent = 0
	}

	// Must not panic.
	_, _ = e.ProcessTick(state, 110.0, 100, 10000)

	if state.ChangePercent != 0 {
		t.Errorf("expected ChangePercent==0 when PrevClose==0, got %.4f", state.ChangePercent)
	}
}
