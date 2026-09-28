import asyncio
from collections.abc import Generator

import pytest
from httpx import ASGITransport, AsyncClient

from app.config import get_settings
from app.main import create_app

AUTHORIZATION = {"Authorization": "Bearer pvmbg.v1.test-secret"}


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Provide an isolated credential and settings cache for every endpoint test"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "0")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "0")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.mark.asyncio
async def test_error_outage_is_immediate_and_recovery_needs_no_restart() -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        enabled = await client.post(
            "/admin/outage",
            json={"enabled": True, "mode": "error"},
        )
        health_during_outage = await client.get("/health")
        reports_during_outage = await client.get(
            "/volcanic-reports",
            headers=AUTHORIZATION,
        )
        disabled = await client.post("/admin/outage", json={"enabled": False})
        health_after_recovery = await client.get("/health")
        reports_after_recovery = await client.get(
            "/volcanic-reports",
            headers=AUTHORIZATION,
        )

    assert enabled.status_code == 200
    assert enabled.json() == {"outage": True, "mode": "error"}
    assert health_during_outage.status_code == 503
    assert reports_during_outage.status_code == 503
    assert reports_during_outage.json() == {"detail": "PVMBG service is in a controlled outage"}

    assert disabled.status_code == 200
    assert disabled.json() == {"outage": False}
    assert health_after_recovery.status_code == 200
    assert reports_after_recovery.status_code == 200


@pytest.mark.asyncio
async def test_hang_outage_waits_until_operator_disables_it() -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        enabled = await client.post(
            "/admin/outage",
            json={"enabled": True, "mode": "hang"},
        )
        hanging_request = asyncio.create_task(
            client.get("/volcanic-reports", headers=AUTHORIZATION)
        )
        _, pending = await asyncio.wait({hanging_request}, timeout=0.05)

        disabled = await client.post("/admin/outage", json={"enabled": False})
        recovered_response = await asyncio.wait_for(hanging_request, timeout=1)

    assert enabled.json() == {"outage": True, "mode": "hang"}
    assert pending == {hanging_request}
    assert disabled.json() == {"outage": False}
    assert recovered_response.status_code == 200


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "payload",
    [
        {},
        {"enabled": "true"},
        {"enabled": True, "mode": "unsupported"},
    ],
)
async def test_rejects_invalid_outage_configuration(payload: dict[str, object]) -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.post("/admin/outage", json=payload)

    assert response.status_code == 422
