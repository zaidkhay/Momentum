"""Signals router — historical signals endpoint.

See ARCHITECTURE.md §9.1 for endpoint contracts:
  GET /signals/recent — reads Supabase signals table, last 50 rows
"""
# TODO: See ARCHITECTURE.md §9.1 — implement endpoint
# This is the ONLY router permitted to query Supabase (historical route)

from fastapi import APIRouter

router = APIRouter()
