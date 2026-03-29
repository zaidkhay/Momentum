import pytest
from httpx import AsyncClient, ASGITransport
from unittest.mock import AsyncMock, MagicMock
from main import app

@pytest.mark.asyncio
async def test_health_always_200():
    mock_redis = AsyncMock()
    mock_redis.ping.return_value = True
    mock_supabase = MagicMock()
    mock_supabase.table.return_value\
        .select.return_value\
        .limit.return_value\
        .execute.return_value = MagicMock()
    app.state.redis = mock_redis
    app.state.supabase = mock_supabase

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/signals/health")

    assert response.status_code == 200
    assert response.json()["status"] == "ok"

@pytest.mark.asyncio
async def test_health_redis_false_when_down():
    mock_redis = AsyncMock()
    mock_redis.ping.side_effect = Exception("connection refused")
    app.state.redis = mock_redis
    app.state.supabase = MagicMock()

    async with AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test"
    ) as client:
        response = await client.get("/signals/health")

    assert response.status_code == 200
    assert response.json()["redis"] == False