// Package types defines the shared data structures used across all ingestion
// service packages. No logic lives here — only type declarations.
// See ARCHITECTURE.md §3.3 for SymbolState definition.
// See ARCHITECTURE.md §5.2 for Signal tier definitions.
package types

import "time"

// SectorName is a typed string constant to prevent passing arbitrary strings
// where a sector name is expected. All valid sector values are declared below.
type SectorName string

// The nine sectors tracked by Momentum, including the custom Hopeful sector.
// See ARCHITECTURE.md §1 for sector list.
const (
	SectorTechnology    SectorName = "Technology"
	SectorHealthcare    SectorName = "Healthcare"
	SectorEnergy        SectorName = "Energy"
	SectorFinancials    SectorName = "Financials"
	SectorConsumer      SectorName = "Consumer"
	SectorIndustrials   SectorName = "Industrials"
	SectorMaterials     SectorName = "Materials"
	SectorCommunication SectorName = "Communication"
	SectorHopeful       SectorName = "Hopeful"
)

// SymbolState holds the complete in-memory state for a single watched symbol.
// One entry exists per subscribed ticker in the stateMap sync.Map.
// See ARCHITECTURE.md §3.3 — in-memory data structure.
type SymbolState struct {
	Ticker        string
	Sector        string
	Price         float64
	PrevClose     float64
	ChangePercent float64
	RelVol        float64     // current volume / 30-day average volume at this time of day
	Volume        int64       // raw trade size from this tick
	ZScore        float64     // rolling Z-score on 1-minute returns
	Window        [20]float64 // ring buffer of the last 20 one-minute returns
	WindowIdx     int         // next write position in the ring buffer (modulo 20)
	IsHopeful     bool
	Sympathy      []string  // tickers that historically move with this one
	ReasonCached  bool      // true if an AI reason has been written to Redis today
	LastSignalAt  time.Time // timestamp of the last signal fired for this ticker
}

// Signal is emitted by the Z-score engine when a threshold is crossed.
// It travels through a buffered channel to the reason pipeline and
// Hopeful promoter goroutines — all off the hot path.
// See ARCHITECTURE.md §5.2 for signal tier thresholds.
type Signal struct {
	Ticker        string
	Z             float64
	RelVol        float64
	Sector        string
	Price         float64
	ChangePercent float64
	IsHopeful     bool
	DetectedAt    time.Time
}
