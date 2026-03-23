# Momentum

Real-time sector momentum dashboard. Discovers highly volatile stocks across 9 market sectors, detects statistically unusual price moves via a rolling Z-score engine, and explains each move in plain English using AI-generated reasons from live news headlines.

Standout feature: the **Hopeful sector** — surfaces high-volatility low-float micro-caps and automatically discovers their sympathy plays.

See `ARCHITECTURE.md` for full system design. See `WINDSURF.md` for build rules and build order.

---

## Stack

| Layer | Tech |
|---|---|
| Ingestion | Go 1.22 — WebSocket, Z-score, Redis writer |
| API | Python 3.11 + FastAPI — Redis reads, Supabase historical reads |
| Frontend | TypeScript + React 18 + Vite + TailwindCSS |
| Cache | Redis 7 |
| Database | Supabase (Postgres) |
| Local orchestration | Docker Compose |

---

## Prerequisites

- Docker + Docker Compose
- Node.js 20+
- Go 1.22+
- A `.env` file (copy from `.env.example` and fill in real values)

---

## Setup

```bash
# 1. Clone the repo
git clone https://github.com/zaidkhay/stock_monitoring.git
cd stock_monitoring

# 2. Create your environment file
cp .env.example .env
# Edit .env and fill in all API keys

# 3. Apply the Supabase schema
# Run supabase/schema.sql against your Supabase project via the SQL editor
```

---

## Running locally

```bash
# Start Redis + ingestion + API
docker compose up --build

# In a separate terminal — start the frontend dev server
cd frontend
npm install
npm run dev
```

- API: http://localhost:8000
- Frontend: http://localhost:5173

---

## Environment variables

| Variable | Description |
|---|---|
| `ALPACA_API_KEY` | Alpaca paper trading API key |
| `ALPACA_SECRET_KEY` | Alpaca paper trading secret |
| `FINNHUB_API_KEY` | Finnhub free tier key |
| `ANTHROPIC_API_KEY` | Anthropic API key (Claude Haiku only) |
| `SUPABASE_URL` | Supabase project URL |
| `SUPABASE_SERVICE_KEY` | Supabase service role key |
| `REDIS_URL` | Redis connection string (default: `redis://localhost:6379`) |
| `MARKET_OPEN_ET` | Market open time ET (default: `09:28`) |
