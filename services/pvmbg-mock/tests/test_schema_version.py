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
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.mark.asyncio
async def test_switches_report_schema_without_restart() -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        version_one = await client.get("/volcanic-reports", headers=AUTHORIZATION)
        switch_to_two = await client.post("/admin/schema-version", json={"version": 2})
        version_two = await client.get("/volcanic-reports", headers=AUTHORIZATION)
        version_two_again = await client.get("/volcanic-reports", headers=AUTHORIZATION)
        switch_to_one = await client.post("/admin/schema-version", json={"version": 1})
        version_one_again = await client.get("/volcanic-reports", headers=AUTHORIZATION)

    assert version_one.status_code == 200
    assert all("confidence_level" not in report for report in version_one.json()["data"])

    assert switch_to_two.status_code == 200
    assert switch_to_two.json() == {"schema_version": 2}
    version_two_reports = version_two.json()["data"]
    assert version_two.status_code == 200
    assert all(0 <= report["confidence_level"] <= 1 for report in version_two_reports)
    assert version_two_again.json()["data"] == version_two_reports

    assert switch_to_one.status_code == 200
    assert switch_to_one.json() == {"schema_version": 1}
    assert all("confidence_level" not in report for report in version_one_again.json()["data"])


@pytest.mark.asyncio
@pytest.mark.parametrize("payload", [{}, {"version": 0}, {"version": 3}, {"version": "2"}])
async def test_rejects_invalid_schema_versions(payload: dict[str, object]) -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.post("/admin/schema-version", json=payload)

    assert response.status_code == 422
