"""Stocks router — individual stock and reason endpoints.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /stocks/{ticker}         — reads Redis price:{ticker}
  GET /stocks/{ticker}/reason  — reads Redis reasons:{ticker}:{date}
  GET /health                  — in-process health check
"""
# TODO: See ARCHITECTURE.md §9.1 — implement endpoints
# Rule 2 (WINDSURF.md): live endpoints read Redis only, never Supabase
# Rule 3 (WINDSURF.md): reason status may return 'generating' — never block

from fastapi import APIRouter

router = APIRouter()
