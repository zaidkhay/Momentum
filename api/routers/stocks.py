"""Stocks router — individual stock and reason endpoints.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /stocks/{ticker}         — reads Redis price:{ticker}
  GET /stocks/{ticker}/reason  — reads Redis reasons:{ticker}:{date}

Rule 2 (WINDSURF.md): live endpoints read Redis only, never Supabase.
Rule 3 (WINDSURF.md): reason status may return 'generating' — never block.
"""

import logging

from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel

from routers.sectors import StockResponse, _hydrate_ticker, _today_et

logger = logging.getLogger("momentum.stocks")

# ── Pydantic models ──────────────────────────────────────────────────────────


class ReasonResponse(BaseModel):
    reason: str
    status: str  # "ready" | "generating" | "unavailable"


# ── Router ────────────────────────────────────────────────────────────────────

router = APIRouter(prefix="/stocks", tags=["stocks"])


@router.get("/{ticker}", response_model=StockResponse)
async def get_stock(request: Request, ticker: str) -> StockResponse:
    """Return a single stock's live data from Redis.

    Reads price:{ticker} and reasons:{ticker}:{today}.
    Returns 404 if the ticker has no price data in Redis.
    See ARCHITECTURE.md §9.1 — GET /stocks/{ticker}.
    """
    redis = request.app.state.redis
    today = await _today_et()

    stock = await _hydrate_ticker(redis, ticker.upper(), today)
    if stock is None:
        raise HTTPException(
            status_code=404,
            detail=f"Ticker '{ticker.upper()}' not found in Redis",
        )

    return stock


@router.get("/{ticker}/reason", response_model=ReasonResponse)
async def get_reason(request: Request, ticker: str) -> ReasonResponse:
    """Return the AI-generated reason for a ticker's move today.

    Reads reasons:{ticker}:{today ET date} from Redis.
    - If found: status = "ready"
    - If not found: status = "generating" (pipeline may still be running)
    See ARCHITECTURE.md §6.3 — reason cache strategy.
    """
    redis = request.app.state.redis
    today = await _today_et()

    # Redis key: reasons:{ticker}:{YYYY-MM-DD}
    # See ARCHITECTURE.md §7 — reasons key schema.
    reason_raw = await redis.get(f"reasons:{ticker.upper()}:{today}")

    if reason_raw:
        return ReasonResponse(reason=reason_raw, status="ready")

    return ReasonResponse(reason="", status="generating")
