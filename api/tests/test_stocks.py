import pytest
import json
from httpx import AsyncClient, ASGITransport
from unittest.mock import AsyncMock
from main import app

MOCK_STOCK = json.dumps({
    "ticker": "NVDA",
    "sector": "Technology",
    "price": 876.20,
    "changePercent": 2.84,
    "zScore": 2.9,
    "relVol": 3.1,
    "isHopeful": False,
    "isSympathy": False,
    "parent": None,
})

@pytest.mark.asyncio
async def test_get_stock_returns_200():
    mock_redis = AsyncMock()
    mock_redis.get.return_value = MOCK_STOCK.encode()
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/stocks/NVDA")

    assert response.status_code == 200
    assert response.json()["ticker"] == "NVDA"

@pytest.mark.asyncio
async def test_missing_ticker_returns_404():
    mock_redis = AsyncMock()
    mock_redis.get.return_value = None
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/stocks/FAKEFAKE")

    assert response.status_code == 404

@pytest.mark.asyncio
async def test_reason_ready_when_cached():
    mock_redis = AsyncMock()
    mock_redis.get.return_value = b"DOE grant triggered short squeeze."
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/stocks/AMTX/reason")

    assert response.status_code == 200
    data = response.json()
    assert data["status"] == "ready"
    assert data["reason"] == "DOE grant triggered short squeeze."

@pytest.mark.asyncio
async def test_reason_generating_when_missing():
    mock_redis = AsyncMock()
    mock_redis.get.return_value = None
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/stocks/AMTX/reason")

    assert response.status_code == 200
    data = response.json()
    assert data["status"] == "generating"
    assert data["reason"] == ""