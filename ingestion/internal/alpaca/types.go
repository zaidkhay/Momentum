// Package alpaca defines the wire-format types for the Alpaca Markets
// WebSocket stream (IEX feed). No logic lives here — only message structs
// used to unmarshal inbound messages and marshal outbound commands.
//
// See ARCHITECTURE.md §3.2 — wsClient goroutine, Alpaca IEX feed.
// See WINDSURF.md §External APIs — Alpaca WebSocket endpoint.
package alpaca

// TradeMessage is a single inbound trade event from the Alpaca IEX stream.
// Alpaca sends an array of these when Type == "t".
// JSON field names match Alpaca's wire format exactly.
type TradeMessage struct {
	Type      string  `json:"T"` // "t" for trade
	Symbol    string  `json:"S"` // ticker symbol, e.g. "AAPL"
	Price     float64 `json:"p"` // trade price
	Size      int     `json:"s"` // number of shares in this trade
	Timestamp string  `json:"t"` // RFC3339 timestamp of the trade
}

// AuthMessage is the outbound authentication payload sent immediately after
// the WebSocket connection is established.
type AuthMessage struct {
	Action string `json:"action"` // always "auth"
	Key    string `json:"key"`
	Secret string `json:"secret"`
}

// SubscribeMessage is the outbound command to add or remove ticker symbols
// from the active trade stream subscription.
type SubscribeMessage struct {
	Action string   `json:"action"` // "subscribe" or "unsubscribe"
	Trades []string `json:"trades"` // list of ticker symbols
}

// ServerMessage is the generic inbound envelope used to inspect the message
// type before routing to a typed handler. Alpaca sends arrays of these.
// Fields not present in a given message type unmarshal to zero values.
type ServerMessage struct {
	Type    string         `json:"T"`    // "success", "error", "subscription", "t", etc.
	Message string         `json:"msg"`  // human-readable description (success/error)
	Code    int            `json:"code"` // numeric error code (error messages only)
	Trades  []TradeMessage `json:"data"` // trade batch (when present)
}
