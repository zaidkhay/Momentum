"""Momentum — FastAPI REST API service.

See ARCHITECTURE.md §9 for full API contracts, endpoint definitions,
and latency requirements.
"""
# TODO: See ARCHITECTURE.md §9 — include routers after each is implemented
# Build order per WINDSURF.md: sectors.py → stocks.py → signals.py → here

from fastapi import FastAPI

app = FastAPI(title="Momentum API")
