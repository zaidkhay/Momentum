"""Sectors router — live sector feed endpoints.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /sectors/{sector}  — reads Redis sector:{name}
  GET /sectors/hopeful   — reads Redis hopeful:tickers + price:{ticker}

Rule 2 (WINDSURF.md): live endpoints read Redis only, never Supabase.
All ticker hydration uses asyncio.gather — never sequential awaits in a loop.
"""

import asyncio
import json
import logging
from datetime import datetime
from zoneinfo import ZoneInfo

from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel

logger = logging.getLogger("momentum.sectors")

# ── Pydantic models ──────────────────────────────────────────────────────────
# See ARCHITECTURE.md §9.3 — stock object schema.


class StockResponse(BaseModel):
    ticker: str
    sector: str
    price: float
    changePercent: float
    zScore: float
    relVol: float
    isHopeful: bool
    isSympathy: bool
    parent: str | None
    reason: str
    reasonStatus: str  # "ready" | "generating" | "unavailable"


class HopefulResponse(BaseModel):
    leaders: list[StockResponse]
    sympathy: list[StockResponse]


# ── Valid sectors ─────────────────────────────────────────────────────────────
# See ARCHITECTURE.md §1 — nine sectors including Hopeful.

VALID_SECTORS = {
    "Technology", "Healthcare", "Energy", "Financials",
    "Consumer", "Industrials", "Materials", "Communication",
    "Hopeful",
}

# ── Private helpers ───────────────────────────────────────────────────────────


async def _today_et() -> str:
    """Return today's date as YYYY-MM-DD in Eastern Time.

    Used to construct the Redis key for cached reasons:
    reasons:{ticker}:{date}.  See ARCHITECTURE.md §6.3.
    """
    return datetime.now(ZoneInfo("America/New_York")).strftime("%Y-%m-%d")


async def _hydrate_ticker(
    redis, ticker: str, today: str
) -> StockResponse | None:
    """Read price:{ticker} from Redis and enrich with reason status.

    Returns None if the key is missing or the JSON is malformed — a single
    bad key must never crash an entire sector request.
    See ARCHITECTURE.md §7 — price:{ticker} value schema.
    """
    # Fetch price data and reason in parallel — two independent Redis reads.
    raw, reason_raw = await asyncio.gather(
        redis.get(f"price:{ticker}"),
        redis.get(f"reasons:{ticker}:{today}"),
    )

    if raw is None:
        return None

    # Parse the JSON written by Go's redis batch writer.
    # Wrap in try/except so a single malformed key doesn't crash the request.
    try:
        data = json.loads(raw)
    except (json.JSONDecodeError, TypeError) as exc:
        logger.warning("_hydrate_ticker: bad JSON for %s: %s", ticker, exc)
        return None

    # Determine reason status.
    # See ARCHITECTURE.md §6.3 — cache key reasons:{ticker}:{date}.
    if reason_raw:
        reason = reason_raw
        reason_status = "ready"
    else:
        reason = ""
        reason_status = "generating"

    return StockResponse(
        ticker=data.get("ticker", ticker),
        sector=data.get("sector", ""),
        price=data.get("price", 0.0),
        changePercent=data.get("changePercent", 0.0),
        zScore=data.get("zScore", 0.0),
        relVol=data.get("relVol", 0.0),
        isHopeful=data.get("isHopeful", False),
        isSympathy=data.get("isSympathy", False),
        parent=data.get("parent") or None,
        reason=reason,
        reasonStatus=reason_status,
    )


# ── Router ────────────────────────────────────────────────────────────────────

router = APIRouter(prefix="/sectors", tags=["sectors"])


@router.get("/hopeful", response_model=HopefulResponse)
async def get_hopeful(request: Request) -> HopefulResponse:
    """Return Hopeful sector split into leaders and sympathy plays.

    Leaders: isHopeful=true, isSympathy=false.
    Sympathy: isSympathy=true.
    See ARCHITECTURE.md §10.3 — Hopeful tab layout.
    """
    redis = request.app.state.redis
    today = await _today_et()

    # Read the hopeful ticker list from Redis.
    # Value is a JSON array of ticker strings. See ARCHITECTURE.md §7.
    raw = await redis.get("hopeful:tickers")
    if not raw:
        return HopefulResponse(leaders=[], sympathy=[])

    try:
        tickers: list[str] = json.loads(raw)
    except (json.JSONDecodeError, TypeError):
        logger.warning("get_hopeful: bad JSON in hopeful:tickers")
        return HopefulResponse(leaders=[], sympathy=[])

    # Hydrate all tickers concurrently — asyncio.gather fires all coroutines
    # at once and waits for all to complete. Never sequential awaits in a loop.
    results = await asyncio.gather(
        *[_hydrate_ticker(redis, t, today) for t in tickers]
    )

    leaders: list[StockResponse] = []
    sympathy: list[StockResponse] = []

    for stock in results:
        if stock is None:
            continue
        if stock.isSympathy:
            sympathy.append(stock)
        else:
            leaders.append(stock)

    # Sort each list by |changePercent| descending.
    leaders.sort(key=lambda s: abs(s.changePercent), reverse=True)
    sympathy.sort(key=lambda s: abs(s.changePercent), reverse=True)

    return HopefulResponse(leaders=leaders, sympathy=sympathy)


@router.get("/{sector}", response_model=list[StockResponse])
async def get_sector(request: Request, sector: str) -> list[StockResponse]:
    """Return all stocks in a sector sorted by |changePercent| descending.

    Validates sector name against the 9 valid sectors.
    Reads sector:{name} → JSON array of tickers → hydrates each.
    See ARCHITECTURE.md §9.1 — GET /sectors/{sector}.
    """
    if sector not in VALID_SECTORS:
        raise HTTPException(
            status_code=400,
            detail=f"Invalid sector '{sector}'. Valid: {sorted(VALID_SECTORS)}",
        )

    redis = request.app.state.redis
    today = await _today_et()

    # Read the sector ticker list from Redis.
    # Value is a JSON array of ticker strings sorted by |changePercent| desc.
    # See ARCHITECTURE.md §7 — sector:{name}.
    raw = await redis.get(f"sector:{sector}")
    if not raw:
        return []

    try:
        tickers: list[str] = json.loads(raw)
    except (json.JSONDecodeError, TypeError):
        logger.warning("get_sector: bad JSON in sector:%s", sector)
        return []

    # Hydrate all tickers concurrently.
    results = await asyncio.gather(
        *[_hydrate_ticker(redis, t, today) for t in tickers]
    )

    # Filter out None (missing price keys) and sort by |changePercent| desc.
    stocks = [s for s in results if s is not None]
    stocks.sort(key=lambda s: abs(s.changePercent), reverse=True)

    return stocks
