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
	Ticker        string      `json:"ticker"`
	Sector        string      `json:"sector"`
	Price         float64     `json:"price"`
	PrevClose     float64     `json:"prevClose"`
	ChangePercent float64     `json:"changePercent"`
	RelVol        float64     `json:"relVol"` // current volume / 30-day average volume at this time of day
	Volume        int64       `json:"volume"` // raw trade size from this tick
	ZScore        float64     `json:"zScore"` // rolling Z-score on 1-minute returns
	Window        [20]float64 `json:"-"`      // ring buffer — internal, excluded from Redis JSON
	WindowIdx     int         `json:"-"`      // ring buffer index — internal, excluded from Redis JSON
	IsHopeful     bool        `json:"isHopeful"`
	IsSympathy    bool        `json:"isSympathy"` // true if subscribed as a sympathy peer of a Hopeful leader
	Parent        string      `json:"parent"`     // leader ticker if IsSympathy, empty otherwise
	Sympathy      []string    `json:"sympathy"`   // tickers that historically move with this one
	ReasonCached  bool        `json:"-"`          // internal — not sent to Redis
	LastSignalAt  time.Time   `json:"-"`          // internal — not sent to Redis
}

// Signal is emitted by the Z-score engine when a threshold is crossed.
// It travels through a buffered channel to the reason pipeline and
// Hopeful promoter goroutines — all off the hot path.
// See ARCHITECTURE.md §5.2 for signal tier thresholds.
type Signal struct {
	Ticker        string    `json:"ticker"`
	Z             float64   `json:"zScore"`
	RelVol        float64   `json:"relVol"`
	Sector        string    `json:"sector"`
	Price         float64   `json:"price"`
	ChangePercent float64   `json:"changePercent"`
	IsHopeful     bool      `json:"isHopeful"`
	DetectedAt    time.Time `json:"detectedAt"`
}
