from collections.abc import Generator

import pytest
from httpx import ASGITransport, AsyncClient

from app.config import get_settings
from app.main import create_app
from app.models import OutageMode
from app.state import RuntimeState


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Provide required startup configuration without leaking cache between tests"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "0")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "0")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.mark.asyncio
async def test_health_is_public_and_reports_normal_state() -> None:
    application = create_app()

    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "pvmbg-mock"}


@pytest.mark.asyncio
async def test_health_tracks_outage_and_recovery() -> None:
    application = create_app()

    async with application.router.lifespan_context(application):
        runtime: RuntimeState = application.state.runtime
        async with AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client:
            await runtime.set_outage(True, OutageMode.ERROR)
            outage_response = await client.get("/health")

            await runtime.set_outage(False, OutageMode.ERROR)
            recovered_response = await client.get("/health")

    assert outage_response.status_code == 503
    assert outage_response.json() == {
        "status": "outage",
        "service": "pvmbg-mock",
    }
    assert recovered_response.status_code == 200
    assert recovered_response.json() == {
        "status": "ok",
        "service": "pvmbg-mock",
    }
