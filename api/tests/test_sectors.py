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
async def test_get_sector_returns_stocks():
    mock_redis = AsyncMock()
    mock_redis.get.side_effect = lambda key: (
        json.dumps(["NVDA"]).encode()
        if key == "sector:Technology"
        else MOCK_STOCK.encode()
        if key == "price:NVDA"
        else None
    )
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/sectors/Technology")

    assert response.status_code == 200
    data = response.json()
    assert len(data) == 1
    assert data[0]["ticker"] == "NVDA"

@pytest.mark.asyncio
async def test_invalid_sector_returns_400():
    mock_redis = AsyncMock()
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/sectors/FakeSector")

    assert response.status_code == 400

@pytest.mark.asyncio
async def test_missing_sector_key_returns_empty():
    mock_redis = AsyncMock()
    mock_redis.get.return_value = None
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/sectors/Energy")

    assert response.status_code == 200
    assert response.json() == []

@pytest.mark.asyncio
async def test_reason_status_generating_when_missing():
    mock_redis = AsyncMock()
    mock_redis.get.side_effect = lambda key: (
        json.dumps(["NVDA"]).encode()
        if key == "sector:Technology"
        else MOCK_STOCK.encode()
        if key == "price:NVDA"
        else None
    )
    app.state.redis = mock_redis

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/sectors/Technology")

    assert response.status_code == 200
    data = response.json()
    assert data[0]["reasonStatus"] == "generating"