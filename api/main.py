"""Momentum — FastAPI REST API service.

See ARCHITECTURE.md §9 for full API contracts, endpoint definitions,
and latency requirements.

Startup sequence:
  1. Validate required environment variables
  2. Connect to Redis (async) and verify with PING
  3. Connect to Supabase client
  4. Store both on app.state so routers access them via request.app.state

Shutdown sequence:
  1. Close Redis connection pool
"""

import logging
import os

import redis.asyncio as aioredis
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from supabase import create_client

from routers.sectors import router as sectors_router
from routers.stocks import router as stocks_router
from routers.signals import router as signals_router

# ── Logging ───────────────────────────────────────────────────────────────────

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
logger = logging.getLogger("momentum.api")

# ── App ───────────────────────────────────────────────────────────────────────

app = FastAPI(title="Momentum API")

# CORS middleware — allow all origins so the React frontend (Step 9) can
# call the API from any dev server or Vercel deployment.
# See WINDSURF.md — frontend polls every 1 second.
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Include routers — order matters: sectors before stocks to avoid
# path parameter collisions.
app.include_router(sectors_router)
app.include_router(stocks_router)
app.include_router(signals_router)


# ── Lifecycle events ──────────────────────────────────────────────────────────


@app.on_event("startup")
async def startup() -> None:
    """Connect to Redis and Supabase, store clients on app.state.

    Validates required environment variables and raises RuntimeError
    with a clear message if any are missing.
    See ARCHITECTURE.md §11 — required environment variables.
    """
    # Step 1: validate environment variables.
    redis_url = os.environ.get("REDIS_URL", "redis://localhost:6379")
    supabase_url = os.environ.get("SUPABASE_URL")
    supabase_key = os.environ.get("SUPABASE_SERVICE_KEY")

    if not supabase_url:
        raise RuntimeError("SUPABASE_URL environment variable is required")
    if not supabase_key:
        raise RuntimeError("SUPABASE_SERVICE_KEY environment variable is required")

    # Step 2: connect to Redis using the async client.
    # redis.asyncio provides a fully async interface compatible with FastAPI's
    # event loop. All Redis commands return coroutines.
    logger.info("Connecting to Redis at %s", redis_url)
    redis_client = aioredis.from_url(redis_url, decode_responses=True)

    # Verify connectivity with a PING — raises on connection failure.
    await redis_client.ping()
    logger.info("Redis connected successfully")

    # Step 3: connect to Supabase.
    # create_client returns a synchronous client — Supabase queries in the
    # signals router are blocking but acceptable per ARCHITECTURE.md §9.2
    # (100-300ms for historical routes, not on the hot path).
    logger.info("Connecting to Supabase at %s", supabase_url)
    supabase_client = create_client(supabase_url, supabase_key)
    logger.info("Supabase client created successfully")

    # Store on app.state so routers access via request.app.state.redis / .supabase.
    app.state.redis = redis_client
    app.state.supabase = supabase_client


@app.on_event("shutdown")
async def shutdown() -> None:
    """Close the Redis connection pool on shutdown.

    Supabase client does not require explicit cleanup.
    """
    if hasattr(app.state, "redis"):
        await app.state.redis.close()
        logger.info("Redis connection closed")
