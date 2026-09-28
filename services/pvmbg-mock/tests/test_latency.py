from collections.abc import Generator

import pytest
from httpx import ASGITransport, AsyncClient

import app.routes.reports as reports_route
from app.config import get_settings
from app.main import create_app

AUTHORIZATION = {"Authorization": "Bearer pvmbg.v1.test-secret"}


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Configure a deterministic non-zero range for latency tests"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "750")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "1250")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.mark.asyncio
async def test_report_endpoint_applies_random_configured_delay(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    selected_bounds: list[tuple[int, int]] = []
    slept_seconds: list[float] = []

    def select_delay(minimum: int, maximum: int) -> int:
        selected_bounds.append((minimum, maximum))
        return 900

    async def record_sleep(seconds: float) -> None:
        slept_seconds.append(seconds)

    monkeypatch.setattr(reports_route, "randint", select_delay)
    monkeypatch.setattr(reports_route, "sleep", record_sleep)

    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get("/volcanic-reports", headers=AUTHORIZATION)

    assert response.status_code == 200
    assert selected_bounds == [(750, 1250)]
    assert slept_seconds == [0.9]


@pytest.mark.asyncio
async def test_invalid_credentials_are_rejected_before_delay(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    slept_seconds: list[float] = []

    async def record_sleep(seconds: float) -> None:
        slept_seconds.append(seconds)

    monkeypatch.setattr(reports_route, "sleep", record_sleep)

    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get(
            "/volcanic-reports",
            headers={"Authorization": "Bearer wrong-token"},
        )

    assert response.status_code == 403
    assert slept_seconds == []


@pytest.mark.asyncio
async def test_error_outage_is_returned_before_delay(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    slept_seconds: list[float] = []

    async def record_sleep(seconds: float) -> None:
        slept_seconds.append(seconds)

    monkeypatch.setattr(reports_route, "sleep", record_sleep)

    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        await client.post(
            "/admin/outage",
            json={"enabled": True, "mode": "error"},
        )
        response = await client.get("/volcanic-reports", headers=AUTHORIZATION)

    assert response.status_code == 503
    assert slept_seconds == []
