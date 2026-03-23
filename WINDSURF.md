# Momentum — Windsurf Project Guide

## What this project is

Momentum is a real-time sector momentum dashboard that dynamically discovers highly volatile stocks, detects statistically unusual price moves using a rolling Z-score engine, ranks them live by % change across 9 major market sectors, and explains each move in plain English using AI-generated reasons derived from live news headlines.

The standout feature is the **Hopeful sector** — a custom watchlist that surfaces high-volatility low-float micro-caps (stocks like AMTX, IMRX, EONR that move 20–80% on catalyst days) and automatically surfaces their sympathy plays: sector peers likely to move in reaction to the lead stock's catalyst.

---

## Role assignments

| Role | Who |
|---|---|
| System architect | Human (me) |
| Implementer | Windsurf (you) |

The architect manages all structural decisions: service boundaries, data flow, Redis key schema, sympathy map entries, API contracts, and infrastructure. Windsurf implements those decisions. When in doubt about a structural choice, ask the architect — do not infer.

Always read `momentum_architecture.docx` before implementing any service or module. Reference the specific section number in your implementation comments.

---

## Languages by layer

### Go 1.22+ — ingestion service only
- Handles: Alpaca WebSocket feed, Z-score engine, watchlist refresh cron, Hopeful promotion logic, Finnhub + Claude reason pipeline, Redis batch writer, async Supabase writer
- Concurrency model: goroutines + buffered channels
- No framework — standard library + Chi router for any internal HTTP if needed
- Explain Go-specific patterns (goroutines, channels, defer, interfaces) inline with comments — the architect is building Go familiarity through this project

### Python 3.11+ (FastAPI) — REST API service only
- Handles: serving live data from Redis, serving historical data from Supabase, all client-facing HTTP routes
- Async via uvicorn + asyncio throughout
- No ORM — raw Supabase client queries
- Type hints on all functions

### TypeScript + React 18 + Vite + TailwindCSS — frontend only
- Polls Python API every 1 second for live price data
- Polls every 2 seconds for reason status until ready
- No WebSocket on the frontend — polling only
- Strict mode on, explicit types on all props and hooks, no `any`

---

## Infrastructure

### Redis
- Sole source of truth for all live data reads
- Python API never touches Postgres for live endpoints
- Go ingestion writes to Redis every 250ms in batch
- Key schema is strictly defined in architecture doc section 7 — do not add keys outside this schema without architect approval

### Supabase (Postgres)
- Persistent storage only: signals, reasons, avg_volumes, watchlist_log
- Written to asynchronously by Go ingestion, off the hot path
- Python API reads from here only for historical/non-live routes
- Schema defined in supabase/schema.sql

### Docker Compose
- Orchestrates Redis + Go ingestion + Python API locally
- Frontend runs separately via Vite dev server
- Three services only: redis, ingestion, api

### Fly.io
- Production deployment for Go and Python services
- Upstash Redis on Fly for production

### Vercel
- Frontend deployment only

---

## External APIs

### Alpaca Markets
- WebSocket: `wss://stream.data.alpaca.markets/v2/iex`
- Screener: `GET /v2/screener/stocks/movers`
- Most actives: `GET /v2/screener/stocks/most-actives`
- Paper trading key — free tier
- Used in: ingestion service only

### Finnhub
- News: `GET /api/v1/company-news?symbol={ticker}`
- Free tier, 60 calls/min
- Used in: reason generation pipeline only

### Anthropic Claude API
- Model: `claude-haiku-4-5-20251001` — do not substitute any other model
- Used only in reason generation pipeline
- Called once per ticker per trading day then cached in Supabase and Redis
- Used in: ingestion service reason pipeline only

---

## Architecture rules — never violate these

### Rule 1 — Hot path is I/O free
The Go `tickProcessor` goroutine must never block on I/O. All Redis and Supabase writes go through buffered channels and are handled by separate goroutines.

### Rule 2 — Live endpoints read Redis only
All live API endpoints in the Python FastAPI service read from Redis only. Never query Postgres for a live data route. Supabase is only queried for historical routes (e.g. `/signals/recent`).

### Rule 3 — Reason pipeline is async
The reason pipeline is fully async and off the hot path. Signal detection completes and the price feed continues before reason generation begins. The frontend shows "Analyzing..." until the reason is ready.

### Rule 4 — No WebSocket on the frontend
Frontend polling intervals are fixed: 1 second for prices, 2 seconds for reason status. Do not upgrade to WebSocket without explicit architect approval.

### Rule 5 — Sympathy map is architect-managed
The sympathy map in `ingestion/internal/watchlist/sympathy.go` is maintained by the architect. Never auto-generate, infer, or add entries without being explicitly told to.

### Rule 6 — No raw floats in the UI
All numbers displayed in the frontend must use `toFixed(2)`. No raw JavaScript floats reach any rendered element.

### Rule 7 — Haiku only for reasons
`claude-haiku-4-5-20251001` is the only permitted model for reason generation. Never substitute Sonnet, Opus, or any other model.

---

## Redis key schema (section 7 of architecture doc)

| Key pattern | Value | TTL |
|---|---|---|
| `price:{ticker}` | JSON: full stock object | No expiry — overwritten every 250ms |
| `sector:{name}` | JSON array of tickers sorted by \|changePercent\| desc | No expiry |
| `hopeful:tickers` | JSON array of Hopeful ticker strings | No expiry |
| `reasons:{ticker}:{date}` | Plain string — AI-generated reason | Expires at 4:00pm ET |
| `rvol:{ticker}` | Float — relative volume vs 30-day avg | No expiry |
| `watchlist:active` | Set of all currently subscribed tickers | No expiry |

Do not add keys outside this schema without architect approval.

---

## Stock object schema (API response)

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

## Hopeful sector — promotion criteria

A symbol is promoted to Hopeful when ALL of the following are true:

| Criterion | Threshold |
|---|---|
| Price | < $20 |
| Relative volume | > 5× 30-day average at this time of day |
| Z-score | \|Z\| > 3.0 |
| % change from prev close | > 10% |

---

## Signal detection thresholds

| Tier | Z-score | Rel vol | Action |
|---|---|---|---|
| Strong | \|Z\| > 3.0 | > 4× | Alert + reason pipeline + Hopeful evaluation |
| Moderate | \|Z\| > 2.5 | > 2× | Alert + reason pipeline |
| Noise | \|Z\| < 2.5 | any | Update price map only — no alert |

---

## Repository structure

```
momentum/
├── docker-compose.yml
├── .env.example
├── .gitignore
├── WINDSURF.md                     ← this file
├── .windsurfrules
├── momentum_architecture.docx
├── supabase/
│   └── schema.sql
├── ingestion/                      ← Go
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
├── api/                            ← Python FastAPI
│   ├── Dockerfile
│   ├── requirements.txt
│   ├── main.py
│   └── routers/
│       ├── sectors.py
│       ├── stocks.py
│       └── signals.py
└── frontend/                       ← TypeScript + React + Vite
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

## Code style

- **Flat and explicit** — no unnecessary abstraction layers
- **Clear variable names** — no single-letter variables outside loop indices
- **Minimal helper functions** — only extract a function if it genuinely reduces repetition
- **Go** — explain goroutines, channels, defer, and interfaces inline with comments
- **Python** — async/await throughout, type hints on all functions, no bare `except`
- **TypeScript** — strict mode, explicit types on all props and hooks, no `any`
- **Never use `\n` in docx strings** — use separate elements
- **Comments reference architecture doc sections** — e.g. `// See architecture doc section 5.1`

---

## Build order for implementation

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

---

## Environment variables

```
ALPACA_API_KEY=
ALPACA_SECRET_KEY=
FINNHUB_API_KEY=
ANTHROPIC_API_KEY=
SUPABASE_URL=
SUPABASE_SERVICE_KEY=
REDIS_URL=redis://localhost:6379
MARKET_OPEN_ET=09:28
```

Never hardcode any of these values. Always read from environment variables. Never commit the `.env` file.
