# Momentum — System Architecture

## 1. Project overview

Momentum is a real-time stock momentum dashboard that dynamically discovers highly volatile stocks across all major market sectors, detects statistically unusual price moves, and explains each move in plain English using AI-generated reasons derived from live news headlines.

### Core capabilities
- Live ranked feed of stocks by absolute % change, refreshing on every tick
- Nine standard sector tabs: Technology, Healthcare, Energy, Financials, Consumer, Industrials, Materials, Communication, plus a custom Hopeful sector
- Hopeful sector: dynamically discovers high-volatility low-float micro-caps (e.g. AMTX, IMRX, EONR) and surfaces their sympathy plays automatically
- AI-generated one-sentence reason for each mover, derived from Finnhub news headlines via Claude API
- Relative volume computation per symbol vs 30-day average at the same time of day
- Rolling Z-score on 1-minute returns to quantify how unusual each move is

### Architectural philosophy
- Hot path is entirely in memory — no database I/O between tick ingestion and signal detection
- Database writes are always asynchronous, off the hot path
- Redis is the source of truth for live reads; Postgres (Supabase) is the source of truth for history
- Service boundaries are strict — ingestion service never serves HTTP, API service never writes to Redis directly

---

## 2. Technology stack

### Languages by layer

| Layer | Language | Rationale |
|---|---|---|
| Ingestion service | Go | No GC pauses on hot path, goroutines map cleanly to concurrent WebSocket + cron workload |
| REST API | Python + FastAPI | Async I/O sufficient for read-heavy API, well-documented ecosystem |
| Frontend | TypeScript + React + Vite | Type safety for complex data shapes, Vite for fast dev builds, Tailwind for rapid styling |

### Infrastructure services

| Service | Role | Tier |
|---|---|---|
| Supabase (Postgres) | Persistent storage: alerts, generated reasons, historical prices, trade records | Free cloud-hosted |
| Redis | In-memory hot cache: latest price, Z-score, relative volume per symbol. API reads here, never Postgres for live data | Docker (local) / Upstash (prod) |
| Fly.io | Cloud deployment for Go ingestion service and Python API | Free tier |
| Docker Compose | Local orchestration: Go service, Python API, Redis container, .env wiring | Local dev only |

### External APIs

| API | Purpose | Cost |
|---|---|---|
| Alpaca Markets | WebSocket real-time price feed (IEX), movers screener, most-actives endpoint | Free tier |
| Finnhub | News headlines per ticker for reason generation | Free, 60 calls/min |
| Anthropic (Claude Haiku) | Generate plain-English reason from headlines | ~$0.02/day |

---

## 3. Service architecture

### 3.1 Service map

```
Alpaca WebSocket
      |
      v
Go Ingestion Service
  |- Z-score engine        (pure in-memory, Float64 ring buffers)
  |- Hopeful discovery     (promotes stocks, builds sympathy map)
  |- Reason pipeline       (Finnhub -> Claude -> Supabase cache)
  |- Redis batch writer    (every 250ms)
  |- Async Supabase writer (alerts, reasons, off hot path)
      |
      |-- writes every 250ms
      v
    Redis  <----------  Python FastAPI reads from here (live routes)
      |
    Supabase <---------  Python FastAPI reads from here (historical routes)
      |
    Python FastAPI
      |
    React + TypeScript frontend
```

### 3.2 Go ingestion service — goroutine map

| Goroutine | Responsibility | Trigger |
|---|---|---|
| wsClient | Maintains Alpaca WebSocket, reconnects on drop, writes ticks to tickChan | Always running |
| tickProcessor | Reads tickChan, updates in-memory price map, computes Z-score, detects signals | Every tick |
| watchlistRefresher | Fetches movers + most-actives, diffs against current watchlist, resubscribes | Every 5 min |
| redisWriter | Drains in-memory map to Redis in batch | Every 250ms |
| supabaseWriter | Writes signals and generated reasons to Supabase asynchronously | On signal detected |
| reasonPipeline | Fetches Finnhub headlines -> calls Claude -> writes reason to Supabase + Redis | On signal detected |
| hopefulPromoter | Evaluates symbols for Hopeful promotion criteria | On signal detected |

### 3.3 In-memory data structure

```go
// One entry per watched symbol, held entirely in memory
type SymbolState struct {
    Ticker        string
    Sector        string
    Price         float64
    PrevClose     float64
    ChangePercent float64
    RelVol        float64        // current vol / 30d avg at this time
    ZScore        float64        // rolling Z on 1-min returns
    Window        [20]float64    // ring buffer of 1-min returns
    WindowIdx     int
    IsHopeful     bool
    Sympathy      []string       // tickers that move with this one
    ReasonCached  bool
    LastSignalAt  time.Time
}

var stateMap sync.Map  // map[string]*SymbolState — concurrent-safe
```

---

## 4. Watchlist and Hopeful sector

### 4.1 Morning watchlist build (9:28am ET)

A time.AfterFunc goroutine triggers at 9:28am ET every trading day:

1. Fetch top 50 gainers + losers from Alpaca `/v2/screener/stocks/movers` — filter: price $1–$50, |change| > 8%
2. Fetch top 30 from Alpaca `/v2/screener/stocks/most-actives` — filter: relative volume > 3x
3. Merge and deduplicate — target ~80 symbols
4. For each new symbol: fetch 30-day average volume bars from Alpaca `/v2/stocks/{ticker}/bars`
5. Diff against current WebSocket subscription: subscribe new, unsubscribe dropped
6. Seed the Hopeful list from any symbol matching: price < $20, |change| > 10%, rvol > 5x

### 4.2 Intraday watchlist refresh (every 5 min)

The watchlistRefresher goroutine re-runs the movers fetch every 5 minutes during market hours (9:30am–4:00pm ET). New entrants get added, cold symbols get dropped after 30 minutes of no signal.

### 4.3 Hopeful sector — promotion criteria

| Criterion | Threshold | Why |
|---|---|---|
| Price | < $20 | Low-float micro-caps that produce AMTX/IMRX-style moves are almost always sub-$20 |
| Relative volume | > 5x 30-day avg at this time | Separates a real catalyst day from noise |
| Z-score | |Z| > 3.0 | Statistically unusual vs the stock's own recent behavior |
| % change | > 10% from prev close | Minimum move threshold |

### 4.4 Sympathy map

```go
// ingestion/internal/watchlist/sympathy.go
// ARCHITECT-MANAGED — do not auto-generate entries
var sympathyMap = map[string][]string{
    "AMTX": {"GEVO", "REGI", "VGFC"},    // biofuel
    "IMRX": {"ARQT", "PRTA", "HIMS"},    // autoimmune biotech
    "EONR": {"VAALCO", "CIVI", "RING"},   // micro-cap E&P
    "MARA": {"RIOT", "CLSK", "BTBT"},     // bitcoin miners
    "SAVA": {"ANAVEX", "PRTA", "AGEN"},   // neuro biotech
}
```

---

## 5. Signal detection — Z-score engine

### 5.1 Algorithm

```go
func computeSignal(state *SymbolState, newPrice float64) Signal {
    // 1. Compute 1-minute return
    ret := (newPrice - state.Price) / state.Price

    // 2. Update ring buffer
    state.Window[state.WindowIdx % 20] = ret
    state.WindowIdx++

    // 3. Compute rolling mean and stddev over window
    mean, std := rollingStats(state.Window)

    // 4. Z-score
    z := (ret - mean) / std
    state.ZScore = z

    // 5. Signal threshold
    if math.Abs(z) > 2.5 && state.RelVol > 2.0 {
        return Signal{Ticker: state.Ticker, Z: z, RelVol: state.RelVol}
    }
    return Signal{}
}
```

### 5.2 Signal thresholds

| Signal tier | Z-score | Rel vol | Action |
|---|---|---|---|
| Strong | |Z| > 3.0 | > 4x | Fire alert, reason pipeline, Hopeful evaluation |
| Moderate | |Z| > 2.5 | > 2x | Fire alert, reason pipeline |
| Noise | |Z| < 2.5 | any | Update price map only |

---

## 6. Reason generation pipeline

### 6.1 Flow

```
Signal detected
      |
      v
Check Supabase cache — reason exists for this ticker today?
      |                                   |
     No                                  Yes
      |                                   |
      v                                   v
Fetch last 3 headlines            Return cached reason
from Finnhub news API             to Redis immediately
      |
      v
POST to Anthropic Claude Haiku
Prompt: "Given these headlines, write one sentence
explaining why {TICKER} is moving {direction} today."
      |
      v
Write reason to Supabase reasons table (cache for today)
Write reason to Redis key reasons:{ticker}:{date}
      |
      v
Frontend reads reason from Redis via Python API
```

### 6.2 Latency contract

Reason generation is off the hot path. The price feed and signal detection complete in microseconds. The reason pipeline is triggered asynchronously via a Go channel and may take 1–3 seconds. The frontend displays "Analyzing..." until Redis is populated.

### 6.3 Cache strategy

- Cache key: `reasons:{ticker}:{date}` — expires at market close (4:00pm ET)
- A symbol that triggers multiple signals in one day reuses the cached reason
- Reasons are never regenerated intraday unless explicitly invalidated

---

## 7. Redis key schema

| Key pattern | Value | TTL |
|---|---|---|
| `price:{ticker}` | JSON: { price, changePercent, zScore, relVol, sector, isHopeful, isSympathy, parent } | No expiry — overwritten every 250ms |
| `sector:{name}` | JSON array of tickers sorted by |changePercent| desc | No expiry |
| `hopeful:tickers` | JSON array of Hopeful ticker strings | No expiry |
| `reasons:{ticker}:{date}` | Plain string — AI-generated reason | Expires at 4:00pm ET |
| `rvol:{ticker}` | Float — relative volume vs 30-day avg at this time | No expiry |
| `watchlist:active` | Set of all currently subscribed ticker strings | No expiry |

---

## 8. Supabase schema

```sql
-- Persists every signal fired during the trading day
CREATE TABLE signals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticker       TEXT NOT NULL,
    sector       TEXT NOT NULL,
    price        DECIMAL(12,4) NOT NULL,
    change_pct   DECIMAL(8,4) NOT NULL,
    z_score      DECIMAL(8,4) NOT NULL,
    rel_vol      DECIMAL(8,2) NOT NULL,
    is_hopeful   BOOLEAN DEFAULT FALSE,
    fired_at     TIMESTAMPTZ DEFAULT now()
);

-- Caches AI-generated reasons per ticker per day
CREATE TABLE reasons (
    ticker       TEXT NOT NULL,
    trade_date   DATE NOT NULL,
    reason       TEXT NOT NULL,
    headlines    JSONB,
    created_at   TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (ticker, trade_date)
);

-- 30-day average volume per symbol, refreshed nightly
CREATE TABLE avg_volumes (
    ticker       TEXT PRIMARY KEY,
    avg_volume   BIGINT NOT NULL,
    updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Watchlist audit log
CREATE TABLE watchlist_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticker       TEXT NOT NULL,
    action       TEXT NOT NULL,  -- 'added' | 'removed' | 'hopeful_promoted'
    reason       TEXT,
    logged_at    TIMESTAMPTZ DEFAULT now()
);

-- Indexes
CREATE INDEX ON signals(ticker, fired_at DESC);
CREATE INDEX ON signals(sector, fired_at DESC);
CREATE INDEX ON signals(is_hopeful, fired_at DESC);
```

---

## 9. Python FastAPI — API contracts

### 9.1 Endpoints

| Method | Path | Source | Response |
|---|---|---|---|
| GET | `/sectors/{sector}` | Redis `sector:{name}` | Array of stock objects sorted by |changePercent| desc |
| GET | `/sectors/hopeful` | Redis `hopeful:tickers` + `price:{ticker}` | Two arrays: leaders[], sympathy[] |
| GET | `/stocks/{ticker}` | Redis `price:{ticker}` | Single stock object |
| GET | `/stocks/{ticker}/reason` | Redis `reasons:{ticker}:{today}` | { reason, status: 'ready' or 'generating' or 'unavailable' } |
| GET | `/signals/recent` | Supabase signals table | Last 50 signals across all sectors |
| GET | `/health` | In-process | { status: 'ok', redis: bool, supabase: bool } |

### 9.2 Latency contract

- All live endpoints (sectors, stocks) must respond in under 20ms — Redis reads only
- `/stocks/{ticker}/reason` may return `status: generating` if the pipeline has not completed
- `/signals/recent` reads from Supabase — 100–300ms acceptable, not on hot path

### 9.3 Stock object schema

```json
{
  "ticker":        "AMTX",
  "sector":        "Hopeful",
  "price":         2.84,
  "changePercent": 34.21,
  "zScore":        3.62,
  "relVol":        6.8,
  "isHopeful":     true,
  "isSympathy":    false,
  "parent":        null,
  "reason":        "DOE cellulosic ethanol grant triggered short squeeze on low float.",
  "reasonStatus":  "ready"
}
```

---

## 10. React frontend architecture

### 10.1 Component structure

```
App.tsx
├── SectorTabs.tsx         — tab bar for all 9 sectors + Hopeful
├── LiveFeed.tsx           — ranked stock list, re-renders every 1s
│   └── StockRow.tsx       — ticker, reason, price, rvol, change%
├── HopefulFeed.tsx        — leaders above divider, sympathy plays below
└── hooks/
    ├── useSectorFeed.ts   — polls /sectors/:sector every 1s
    ├── useHopeful.ts      — polls /sectors/hopeful every 1s
    └── useReason.ts       — polls /stocks/:ticker/reason until ready
```

### 10.2 Polling design

- `useSectorFeed`: setInterval 1000ms → fetch `/sectors/{activeSector}` → setStocks
- `useReason`: setInterval 2000ms → fetch `/stocks/{ticker}/reason` → if status=ready, clearInterval
- Flash animation on StockRow: compare previous price to current, apply `flash-up` or `flash-down` CSS class for 350ms

### 10.3 Hopeful tab layout

Leaders (`isHopeful: true`, `isSympathy: false`) appear first ranked by |changePercent|. Sympathy plays (`isSympathy: true`) appear below a labeled divider, grouped by parent ticker with a badge indicating which leader drove the sympathy.

---

## 11. Local development setup

### docker-compose.yml structure

```yaml
services:
  redis:
    image: redis:7-alpine
    ports: ['6379:6379']

  ingestion:
    build: ./ingestion
    env_file: .env
    depends_on: [redis]
    environment:
      - REDIS_URL=redis://redis:6379

  api:
    build: ./api
    env_file: .env
    ports: ['8000:8000']
    depends_on: [redis, ingestion]
    environment:
      - REDIS_URL=redis://redis:6379
```

Frontend runs separately: `cd frontend && npm run dev`

### Required environment variables

| Variable | Service | Description |
|---|---|---|
| ALPACA_API_KEY | ingestion | Alpaca paper trading API key |
| ALPACA_SECRET_KEY | ingestion | Alpaca paper trading secret |
| FINNHUB_API_KEY | ingestion | Finnhub free tier key |
| ANTHROPIC_API_KEY | ingestion | Anthropic API key |
| SUPABASE_URL | ingestion + api | Supabase project URL |
| SUPABASE_SERVICE_KEY | ingestion + api | Supabase service role key |
| REDIS_URL | ingestion + api | Redis connection string |
| MARKET_OPEN_ET | ingestion | Default: 09:28 |

---

## 12. Fly.io deployment

- `momentum-ingestion` — Go service, 1 shared-cpu-1x machine, always running
- `momentum-api` — Python FastAPI, 1 shared-cpu-1x machine, always running
- `momentum-redis` — Upstash managed Redis, shared between both services
- Frontend — deployed to Vercel
- Supabase — cloud-hosted, same instance as local dev

---

## 13. Repository structure

```
momentum/
├── docker-compose.yml
├── .env.example
├── .gitignore
├── WINDSURF.md
├── ARCHITECTURE.md              <- this file
├── .windsurfrules
├── supabase/
│   └── schema.sql
├── ingestion/                   <- Go
│   ├── Dockerfile
│   ├── go.mod
│   ├── main.go
│   └── internal/
│       ├── alpaca/
│       ├── zscore/
│       ├── watchlist/
│       ├── hopeful/
│       ├── reasons/
│       ├── redis/
│       └── supabase/
├── api/                         <- Python FastAPI
│   ├── Dockerfile
│   ├── requirements.txt
│   ├── main.py
│   └── routers/
│       ├── sectors.py
│       ├── stocks.py
│       └── signals.py
└── frontend/                    <- TypeScript + React + Vite
    ├── Dockerfile
    ├── package.json
    ├── vite.config.ts
    ├── tsconfig.json
    └── src/
        ├── App.tsx
        ├── components/
        │   ├── SectorTabs.tsx
        │   ├── LiveFeed.tsx
        │   ├── StockRow.tsx
        │   └── HopefulFeed.tsx
        └── hooks/
            ├── useSectorFeed.ts
            ├── useHopeful.ts
            └── useReason.ts
```

---

## 14. Build order

Follow this order strictly. Do not begin a module until the previous one is approved by the architect.

1. `ingestion/internal/redis/` — batch writer
2. `ingestion/internal/alpaca/` — WebSocket client
3. `ingestion/internal/zscore/` — signal detection engine
4. `ingestion/internal/watchlist/` — refresh logic + sympathy map
5. `ingestion/internal/hopeful/` — promotion logic
6. `ingestion/internal/reasons/` — Finnhub + Claude pipeline
7. `ingestion/main.go` — wire all goroutines together
8. `api/routers/sectors.py` — live sector feed endpoint
9. `api/routers/stocks.py` — individual stock + reason endpoints
10. `api/routers/signals.py` — historical signals endpoint
11. `api/main.py` — wire FastAPI app
12. `frontend/src/hooks/` — all three polling hooks
13. `frontend/src/components/` — all four components
14. `frontend/src/App.tsx` — wire the dashboard together