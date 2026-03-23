-- Momentum — Supabase schema
-- See ARCHITECTURE.md §8 for full table and index definitions

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
