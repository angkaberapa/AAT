import json
from collections.abc import Generator
from uuid import UUID

import pytest
from httpx import ASGITransport, AsyncClient

from app.config import get_settings
from app.main import create_app


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Provide fast valid configuration for observability tests"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "0")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "0")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


def parsed_stdout(capsys: pytest.CaptureFixture[str]) -> list[dict[str, object]]:
    """Parse all non-empty captured stdout lines as JSON logs"""
    return [json.loads(line) for line in capsys.readouterr().out.splitlines() if line.strip()]


@pytest.mark.asyncio
async def test_request_log_and_response_propagate_supplied_correlation_id(
    capsys: pytest.CaptureFixture[str],
) -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get(
            "/health",
            headers={"X-Correlation-ID": "demo-correlation-123"},
        )

    request_log = next(item for item in parsed_stdout(capsys) if item["msg"] == "request completed")
    assert response.headers["X-Correlation-ID"] == "demo-correlation-123"
    assert request_log["correlation_id"] == "demo-correlation-123"
    assert request_log["method"] == "GET"
    assert request_log["path"] == "/health"
    assert request_log["status"] == 200
    assert isinstance(request_log["latency_ms"], float)
    assert {"ts", "level", "service", "correlation_id", "msg"} <= request_log.keys()
    assert request_log["service"] == "pvmbg-mock"


@pytest.mark.asyncio
async def test_missing_correlation_id_is_generated(
    capsys: pytest.CaptureFixture[str],
) -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get("/health")

    generated_id = response.headers["X-Correlation-ID"]
    UUID(generated_id)
    request_log = next(item for item in parsed_stdout(capsys) if item["msg"] == "request completed")
    assert request_log["correlation_id"] == generated_id


@pytest.mark.asyncio
async def test_admin_state_change_is_logged_with_request_correlation(
    capsys: pytest.CaptureFixture[str],
) -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.post(
            "/admin/schema-version",
            json={"version": 2},
            headers={"X-Correlation-ID": "admin-operation-456"},
        )

    audit_log = next(
        item for item in parsed_stdout(capsys) if item["msg"] == "schema version changed"
    )
    assert response.status_code == 200
    assert audit_log["correlation_id"] == "admin-operation-456"
    assert audit_log["schema_version"] == 2
