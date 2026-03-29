"""Signals router — historical signals and health check endpoints.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /signals/recent  — reads Supabase signals table, last 50 rows
  GET /signals/health  — pings Redis and Supabase, always returns 200

This is the ONLY router permitted to query Supabase (historical route).
"""

import logging

from fastapi import APIRouter, Request
from pydantic import BaseModel

logger = logging.getLogger("momentum.signals")

# ── Pydantic models ──────────────────────────────────────────────────────────
# See ARCHITECTURE.md §8 — signals table schema.


class SignalResponse(BaseModel):
    id: str
    ticker: str
    sector: str
    price: float
    changePct: float
    zScore: float
    relVol: float
    isHopeful: bool
    firedAt: str


class HealthResponse(BaseModel):
    status: str
    redis: bool
    supabase: bool


# ── Router ────────────────────────────────────────────────────────────────────

router = APIRouter(prefix="/signals", tags=["signals"])


@router.get("/recent", response_model=list[SignalResponse])
async def get_recent_signals(request: Request) -> list[SignalResponse]:
    """Return the last 50 signals across all sectors from Supabase.

    This is the only endpoint that queries Supabase — it serves historical
    data, not live prices. See ARCHITECTURE.md §9.1.
    If Supabase is unavailable, returns an empty list with a warning log
    rather than crashing — the live feed must never be affected.
    """
    supabase = request.app.state.supabase

    try:
        # Supabase Python client returns a response object with .data attribute.
        # select("*") fetches all columns from the signals table.
        # order() sorts by fired_at descending (most recent first).
        # limit(50) caps the result set. See ARCHITECTURE.md §8.
        response = (
            supabase.table("signals")
            .select("*")
            .order("fired_at", desc=True)
            .limit(50)
            .execute()
        )

        signals: list[SignalResponse] = []
        for row in response.data:
            signals.append(
                SignalResponse(
                    id=str(row.get("id", "")),
                    ticker=row.get("ticker", ""),
                    sector=row.get("sector", ""),
                    price=float(row.get("price", 0)),
                    changePct=float(row.get("change_pct", 0)),
                    zScore=float(row.get("z_score", 0)),
                    relVol=float(row.get("rel_vol", 0)),
                    isHopeful=bool(row.get("is_hopeful", False)),
                    firedAt=str(row.get("fired_at", "")),
                )
            )

        return signals

    except Exception as exc:
        # Never crash on Supabase failure — return empty list.
        # The live feed (sectors, stocks) is unaffected.
        logger.warning("get_recent_signals: Supabase error: %s", exc)
        return []


@router.get("/health", response_model=HealthResponse)
async def health_check(request: Request) -> HealthResponse:
    """Check connectivity to Redis and Supabase.

    Always returns HTTP 200 — never 500 on a health check.
    Individual service status is reported as boolean flags.
    See ARCHITECTURE.md §9.1 — GET /health.
    """
    redis_ok = False
    supabase_ok = False

    # Check Redis with a PING command.
    try:
        pong = await request.app.state.redis.ping()
        redis_ok = bool(pong)
    except Exception as exc:
        logger.warning("health_check: Redis ping failed: %s", exc)

    # Check Supabase with a minimal query.
    try:
        request.app.state.supabase.table("signals").select("id").limit(1).execute()
        supabase_ok = True
    except Exception as exc:
        logger.warning("health_check: Supabase check failed: %s", exc)

    status = "ok" if (redis_ok and supabase_ok) else "degraded"

    return HealthResponse(status=status, redis=redis_ok, supabase=supabase_ok)
