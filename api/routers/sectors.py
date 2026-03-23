"""Sectors router — live sector feed endpoints.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /sectors/{sector}  — reads Redis sector:{name}
  GET /sectors/hopeful   — reads Redis hopeful:tickers + price:{ticker}
"""
# TODO: See ARCHITECTURE.md §9.1 — implement endpoints
# Rule 2 (WINDSURF.md): live endpoints read Redis only, never Supabase

from fastapi import APIRouter

router = APIRouter()
