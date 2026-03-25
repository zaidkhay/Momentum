// Package alpaca implements the Alpaca Markets WebSocket client for the
// ingestion service. It maintains a persistent connection to the IEX feed,
// authenticates, manages dynamic symbol subscriptions, and forwards raw
// trade events as types.SymbolState values into the out channel consumed
// by the tickProcessor goroutine.
//
// See ARCHITECTURE.md §3.2  — wsClient goroutine responsibilities.
// See WINDSURF.md §External APIs — Alpaca IEX WebSocket URL.
// Rule 1 (WINDSURF.md): the out channel send in handleTrade must never block.
package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"momentum/ingestion/internal/types"
)

// sectorMap maps known ticker symbols to their Momentum sector name.
// Used by handleTrade to populate the Sector field of SymbolState.
// Any ticker not present defaults to "Unknown".
//
// ARCHITECT NOTE: META, GOOGL, NFLX appear in both the Technology and
// Communication lists in ARCHITECTURE.md §1. They are assigned to
// Communication here (their primary market-cap classification). If you
// want them in Technology instead, update this map directly.
// See ARCHITECTURE.md §1 for sector definitions.
var sectorMap = map[string]string{
	// Technology — 7 tickers (META/GOOGL/NFLX assigned to Communication below)
	"NVDA": "Technology", "AMD": "Technology", "MSFT": "Technology",
	"AVGO": "Technology", "AAPL": "Technology", "CRM": "Technology", "ORCL": "Technology",

	// Healthcare
	"LLY": "Healthcare", "UNH": "Healthcare", "JNJ": "Healthcare", "ABBV": "Healthcare",
	"MRK": "Healthcare", "PFE": "Healthcare", "TMO": "Healthcare", "ABT": "Healthcare",
	"ISRG": "Healthcare", "AMGN": "Healthcare",

	// Energy
	"XOM": "Energy", "CVX": "Energy", "COP": "Energy", "SLB": "Energy",
	"EOG": "Energy", "PXD": "Energy", "MPC": "Energy", "PSX": "Energy",
	"VLO": "Energy", "HAL": "Energy",

	// Financials
	"JPM": "Financials", "BAC": "Financials", "GS": "Financials", "MS": "Financials",
	"WFC": "Financials", "BLK": "Financials", "SCHW": "Financials", "AXP": "Financials",
	"USB": "Financials", "PNC": "Financials",

	// Consumer
	"AMZN": "Consumer", "TSLA": "Consumer", "HD": "Consumer", "MCD": "Consumer",
	"NKE": "Consumer", "SBUX": "Consumer", "TGT": "Consumer", "LOW": "Consumer",
	"BKNG": "Consumer", "CMG": "Consumer",

	// Industrials
	"CAT": "Industrials", "DE": "Industrials", "GE": "Industrials", "RTX": "Industrials",
	"HON": "Industrials", "UPS": "Industrials", "BA": "Industrials", "LMT": "Industrials",
	"MMM": "Industrials", "FDX": "Industrials",

	// Materials
	"LIN": "Materials", "APD": "Materials", "NEM": "Materials", "FCX": "Materials",
	"NUE": "Materials", "DOW": "Materials", "DD": "Materials", "ALB": "Materials",
	"MOS": "Materials", "VMC": "Materials",

	// Communication (includes META, GOOGL, NFLX — see note above)
	"META": "Communication", "GOOGL": "Communication", "NFLX": "Communication",
	"DIS": "Communication", "T": "Communication", "VZ": "Communication",
	"CMCSA": "Communication", "TMUS": "Communication", "CHTR": "Communication", "PARA": "Communication",

	// Hopeful — high-volatility low-float micro-caps tracked by the Hopeful sector.
	// See ARCHITECTURE.md §4.3 — Hopeful promotion criteria.
	"AMTX": "Hopeful", "IMRX": "Hopeful", "EONR": "Hopeful", "MARA": "Hopeful",
	"RIOT": "Hopeful", "CLSK": "Hopeful", "BTBT": "Hopeful", "GEVO": "Hopeful",
	"REGI": "Hopeful", "SAVA": "Hopeful", "ANAVEX": "Hopeful", "AGEN": "Hopeful",
	"HIMS": "Hopeful", "ARQT": "Hopeful", "PRTA": "Hopeful", "SOFI": "Hopeful",
	"PLTR": "Hopeful", "RIVN": "Hopeful", "LCID": "Hopeful", "BBIO": "Hopeful",
}

// AlpacaClient maintains a persistent WebSocket connection to the Alpaca IEX
// stream, manages symbol subscriptions, and forwards trade events to the
// out channel for downstream processing by the tickProcessor goroutine.
type AlpacaClient struct {
	apiKey    string
	apiSecret string
	wsURL     string // wss://stream.data.alpaca.markets/v2/iex

	// conn is the active WebSocket connection.
	// Protected by mu: all writes to conn (Subscribe, Unsubscribe,
	// reconnectLoop re-subscribe) acquire the write lock before calling
	// WriteJSON. readLoop is the sole reader and holds no lock.
	conn *websocket.Conn

	// out is a write-only view of the caller-owned channel.
	// The chan<- direction is enforced by the compiler — this package
	// can only send, never receive. Owned and closed by main.go.
	out chan<- types.SymbolState

	// subscribed is the authoritative set of currently subscribed tickers.
	// Protected by mu on all reads and writes.
	subscribed map[string]bool

	// mu is a read/write mutex protecting both subscribed and conn writes.
	// sync.RWMutex allows multiple concurrent readers or one exclusive writer.
	// Use RLock/RUnlock for reads, Lock/Unlock for writes.
	mu sync.RWMutex

	// done is a signal-only channel (zero-size struct carries no data).
	// Closing it (via Close()) broadcasts a shutdown signal to all goroutines.
	// This is the standard Go idiom for graceful goroutine cancellation.
	done chan struct{}

	// reconnect is a buffered channel (capacity 1) used by readLoop to
	// signal reconnectLoop that the connection has been lost.
	// Buffered cap 1: readLoop can signal without blocking even if
	// reconnectLoop is momentarily busy handling a previous signal.
	reconnect chan struct{}

	logger *log.Logger
}

// NewAlpacaClient initialises the AlpacaClient struct. It does NOT connect —
// call Connect() to establish the WebSocket connection.
//
// out is a write-only channel owned and closed by the caller (main.go).
// See ARCHITECTURE.md §3.2 — wsClient goroutine is started in Connect().
func NewAlpacaClient(apiKey, apiSecret string, out chan<- types.SymbolState) *AlpacaClient {
	return &AlpacaClient{
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		wsURL:      "wss://stream.data.alpaca.markets/v2/iex",
		out:        out,
		subscribed: make(map[string]bool),
		done:       make(chan struct{}),
		// Buffered cap 1 — readLoop signals without blocking.
		reconnect: make(chan struct{}, 1),
		logger:    log.New(os.Stderr, "[alpaca] ", log.LstdFlags),
	}
}

// Connect establishes the WebSocket connection, authenticates with Alpaca,
// then starts the readLoop and reconnectLoop goroutines.
//
// ctx is used for the initial dial only. Long-running goroutines observe
// c.done for shutdown instead of a context, because a cancelled ctx would
// prevent reconnection attempts.
//
// Returns an error if the initial connection or authentication fails.
func (c *AlpacaClient) Connect(ctx context.Context) error {
	if err := c.connect(ctx); err != nil {
		return err
	}

	// 'go' launches each function as an independent concurrent goroutine.
	// Both goroutines run until Close() closes c.done.
	go c.readLoop()
	go c.reconnectLoop()

	return nil
}

// Subscribe adds tickers to the subscription set and sends a subscribe
// message to Alpaca over the active WebSocket connection.
//
// It is safe to call concurrently. The write lock is held for the entire
// operation so that the subscribed map and conn write are atomic together.
func (c *AlpacaClient) Subscribe(tickers []string) error {
	// Lock acquires the exclusive write lock. No other goroutine can read
	// or write the protected fields until Unlock is called.
	// defer ensures Unlock runs even if WriteJSON returns an error.
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, t := range tickers {
		c.subscribed[t] = true
	}

	if c.conn == nil {
		// Tickers are recorded in the map; reconnectLoop will subscribe
		// them when the connection is established.
		return fmt.Errorf("Subscribe: not connected — tickers queued for next connect")
	}

	msg := SubscribeMessage{Action: "subscribe", Trades: tickers}
	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("Subscribe: WriteJSON: %w", err)
	}
	return nil
}

// Unsubscribe removes tickers from the subscription set and sends an
// unsubscribe message to Alpaca. Acquires write lock on mu.
func (c *AlpacaClient) Unsubscribe(tickers []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, t := range tickers {
		delete(c.subscribed, t)
	}

	if c.conn == nil {
		return fmt.Errorf("Unsubscribe: not connected")
	}

	msg := SubscribeMessage{Action: "unsubscribe", Trades: tickers}
	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("Unsubscribe: WriteJSON: %w", err)
	}
	return nil
}

// Close signals all goroutines to stop and closes the WebSocket connection.
// Call once during graceful shutdown. Subsequent calls will panic on double-close
// of the done channel — callers should ensure Close is called at most once.
func (c *AlpacaClient) Close() {
	// close(ch) broadcasts to all goroutines blocked on <-c.done.
	// readLoop and reconnectLoop both select on c.done and will exit.
	close(c.done)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		// CloseMessage sends a clean WebSocket close frame to the server.
		_ = c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.conn.Close()
	}
}

// connect is the internal dial-and-authenticate method shared by Connect()
// and reconnectLoop(). It replaces c.conn with a fresh authenticated
// connection under the write lock.
//
// Alpaca's handshake sequence:
//  1. Server sends: [{"T":"success","msg":"connected"}]
//  2. Client sends: auth payload
//  3. Server sends: [{"T":"success","msg":"authenticated"}] or error
func (c *AlpacaClient) connect(ctx context.Context) error {
	// websocket.DefaultDialer.DialContext performs the HTTP→WebSocket upgrade.
	// The returned *websocket.Conn wraps the underlying TCP connection.
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect: dial %s: %w", c.wsURL, err)
	}

	// Step 1: Read the "connected" message Alpaca sends on every new connection.
	// We must consume it before sending auth, or reads get out of sync.
	var connMsgs []ServerMessage
	if err := conn.ReadJSON(&connMsgs); err != nil {
		conn.Close()
		return fmt.Errorf("connect: read connected msg: %w", err)
	}

	// Step 2: Send authentication credentials.
	authMsg := AuthMessage{Action: "auth", Key: c.apiKey, Secret: c.apiSecret}
	if err := conn.WriteJSON(authMsg); err != nil {
		conn.Close()
		return fmt.Errorf("connect: write auth: %w", err)
	}

	// Step 3: Read and validate the authentication response.
	var authMsgs []ServerMessage
	if err := conn.ReadJSON(&authMsgs); err != nil {
		conn.Close()
		return fmt.Errorf("connect: read auth response: %w", err)
	}

	for _, msg := range authMsgs {
		if msg.Type == "error" {
			conn.Close()
			return fmt.Errorf("connect: auth rejected (code %d): %s", msg.Code, msg.Message)
		}
	}

	// Step 4: Store the authenticated connection.
	// Write lock ensures no concurrent reader or writer sees a half-replaced conn.
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.logger.Printf("connect: authenticated to %s", c.wsURL)
	return nil
}

// readLoop runs in its own goroutine. It reads raw WebSocket frames, parses
// Alpaca's array-of-messages format, and routes each message by type.
//
// On any read error, it signals reconnectLoop and returns. A new readLoop
// is started by reconnectLoop after a successful reconnection.
//
// See ARCHITECTURE.md §3.2 — wsClient goroutine responsibilities.
func (c *AlpacaClient) readLoop() {
	// Capture conn locally at goroutine start under a read lock.
	// RLock allows multiple concurrent readers; no writer can proceed
	// until all readers have called RUnlock.
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	// defer conn.Close() ensures the connection is released when this
	// goroutine exits — whether by error, shutdown, or normal return.
	defer conn.Close()

	for {
		// ReadMessage returns raw bytes without JSON parsing.
		// We parse manually so we can dispatch by the "T" field first.
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			// Check if we're shutting down before signalling reconnect.
			select {
			case <-c.done:
				return
			default:
			}

			c.logger.Printf("readLoop: read error: %v", err)

			// Non-blocking send to the reconnect channel.
			// If the channel already has a pending signal (cap 1 is full),
			// reconnectLoop already knows about this failure — skip.
			select {
			case c.reconnect <- struct{}{}:
			default:
			}
			return
		}

		// Alpaca sends arrays: [{"T":"t",...}, {"T":"t",...}]
		// Unmarshal the outer array into raw JSON elements first so each
		// element can be unmarshaled into its specific type after type dispatch.
		var rawMsgs []json.RawMessage
		if err := json.Unmarshal(rawBytes, &rawMsgs); err != nil {
			c.logger.Printf("readLoop: unmarshal outer array: %v", err)
			continue
		}

		for _, raw := range rawMsgs {
			// Peek at the "T" field only — a minimal struct avoids allocating
			// a full ServerMessage or TradeMessage before we know which to use.
			var peek struct {
				T string `json:"T"`
			}
			if err := json.Unmarshal(raw, &peek); err != nil {
				c.logger.Printf("readLoop: peek type field: %v", err)
				continue
			}

			switch peek.T {
			case "t":
				// Trade event — unmarshal into TradeMessage and forward.
				var trade TradeMessage
				if err := json.Unmarshal(raw, &trade); err != nil {
					c.logger.Printf("readLoop: unmarshal trade: %v", err)
					continue
				}
				c.handleTrade(trade)

			case "error":
				var msg ServerMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					c.logger.Printf("readLoop: unmarshal error msg: %v", err)
					continue
				}
				c.logger.Printf("readLoop: server error (code %d): %s", msg.Code, msg.Message)

			case "subscription":
				// Alpaca confirms which symbols are now subscribed.
				var msg ServerMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					continue
				}
				c.logger.Printf("readLoop: subscription confirmed")

			case "success":
				var msg ServerMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					continue
				}
				c.logger.Printf("readLoop: %s", msg.Message)

			default:
				// Unknown message type — ignore silently.
			}
		}
	}
}

// handleTrade converts an inbound TradeMessage into a types.SymbolState and
// sends it to the out channel for the tickProcessor goroutine to consume.
//
// The send is non-blocking (select with default). If the out channel is full,
// the update is dropped and a warning is logged.
// Rule 1 (WINDSURF.md): must never block the read path.
func (c *AlpacaClient) handleTrade(trade TradeMessage) {
	// Look up sector from the hardcoded sectorMap.
	// Unknown tickers (dynamically added by watchlistRefresher) default to "Unknown".
	// The Z-score engine (Step 3) and watchlistRefresher (Step 4) will resolve
	// the sector for dynamically discovered symbols.
	sector, ok := sectorMap[trade.Symbol]
	if !ok {
		sector = "Unknown"
	}

	state := types.SymbolState{
		Ticker: trade.Symbol,
		Price:  trade.Price,
		Sector: sector,
		// All other fields are at zero value here.
		// ZScore, RelVol, ChangePercent, and PrevClose are populated
		// by the Z-score engine in ingestion/internal/zscore/ (Step 3).
	}

	// Non-blocking channel send — select with a default case.
	// If the out channel has capacity, the state is queued immediately.
	// If the out channel is full (tickProcessor is backed up), the default
	// branch runs and the update is dropped rather than blocking readLoop.
	// Rule 1 (WINDSURF.md): the WebSocket read path must never stall.
	select {
	case c.out <- state:
	default:
		c.logger.Printf("handleTrade: out channel full, dropping %s @ %.4f", trade.Symbol, trade.Price)
	}
}

// reconnectLoop runs in its own goroutine. It listens for connection-failure
// signals from readLoop and performs reconnection with exponential backoff.
//
// After a successful reconnect it re-reads the current subscribed map under
// mu.RLock to get the latest ticker list — it does NOT cache the list at
// connect time, because watchlistRefresher may have changed it during the
// reconnection window.
//
// See ARCHITECTURE.md §3.2 — wsClient goroutine, reconnection behaviour.
func (c *AlpacaClient) reconnectLoop() {
	const (
		initialDelay = 1 * time.Second
		maxDelay     = 30 * time.Second
	)

	for {
		// select blocks until one of its cases is ready.
		select {
		case <-c.reconnect:
			// readLoop signalled a connection failure — start reconnecting.
			delay := initialDelay

			for {
				c.logger.Printf("reconnectLoop: reconnecting in %v...", delay)

				// Interruptible sleep: use a timer + select so that Close()
				// can interrupt the backoff wait immediately via c.done.
				// time.NewTimer fires once after the given duration.
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
					// Timer elapsed — proceed with reconnection attempt.
				case <-c.done:
					// Shutdown during backoff — stop immediately.
					timer.Stop()
					return
				}

				ctx := context.Background()
				if err := c.connect(ctx); err != nil {
					c.logger.Printf("reconnectLoop: connect failed: %v", err)
					// Exponential backoff: double the delay each failure.
					// min() is a Go 1.21+ builtin — no import needed.
					delay = min(delay*2, maxDelay)
					continue
				}

				// Reconnected successfully — re-subscribe to the current
				// ticker list. Read the subscribed map under RLock here,
				// NOT at connection time, because watchlistRefresher may
				// have modified the map during the reconnection window.
				c.mu.RLock()
				tickers := make([]string, 0, len(c.subscribed))
				for t := range c.subscribed {
					tickers = append(tickers, t)
				}
				c.mu.RUnlock()

				if len(tickers) > 0 {
					// Acquire write lock to safely write to conn.
					c.mu.Lock()
					subMsg := SubscribeMessage{Action: "subscribe", Trades: tickers}
					if err := c.conn.WriteJSON(subMsg); err != nil {
						c.logger.Printf("reconnectLoop: resubscribe error: %v", err)
					}
					c.mu.Unlock()
				}

				// Start a fresh readLoop goroutine on the new connection.
				// The previous readLoop has already exited (it signalled reconnect
				// and returned), so there is no overlap.
				go c.readLoop()
				break // exit the inner retry loop — connection is healthy
			}

		case <-c.done:
			// Close() was called — exit cleanly.
			return
		}
	}
}
