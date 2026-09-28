from collections.abc import Generator

import pytest
from httpx import ASGITransport, AsyncClient, Response

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


async def request_reports(
    *,
    headers: dict[str, str] | None = None,
    since: str | None = None,
) -> Response:
    """Call the reports endpoint through the real application lifespan"""
    application = create_app()
    params = {"since": since} if since is not None else None
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        return await client.get(
            "/volcanic-reports",
            headers=headers,
            params=params,
        )


@pytest.mark.asyncio
async def test_returns_seed_reports_in_contract_envelope() -> None:
    response = await request_reports(headers=AUTHORIZATION)

    assert response.status_code == 200
    payload = response.json()
    assert set(payload) == {"data"}
    assert len(payload["data"]) == 20
    assert all("confidence_level" not in report for report in payload["data"])


@pytest.mark.asyncio
async def test_reports_are_ordered_by_reported_at() -> None:
    response = await request_reports(headers=AUTHORIZATION)

    timestamps = [report["reported_at"] for report in response.json()["data"]]
    assert timestamps == sorted(timestamps)
    assert all(timestamp.endswith("Z") for timestamp in timestamps)


@pytest.mark.asyncio
async def test_since_filter_is_exclusive() -> None:
    application = create_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
            headers=AUTHORIZATION,
        ) as client,
    ):
        initial_response = await client.get("/volcanic-reports")
        all_reports = initial_response.json()["data"]
        boundary_index = 9
        filtered_response = await client.get(
            "/volcanic-reports",
            params={"since": all_reports[boundary_index]["reported_at"]},
        )

    assert filtered_response.status_code == 200
    assert filtered_response.json()["data"] == all_reports[boundary_index + 1 :]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "since",
    [
        "not-a-timestamp",
        "2026-09-25T10:00:00",
        "2026-09-25T10:00:00+07:00",
    ],
)
async def test_invalid_or_non_utc_since_is_bad_request(since: str) -> None:
    response = await request_reports(headers=AUTHORIZATION, since=since)

    assert response.status_code == 400


@pytest.mark.asyncio
async def test_explicit_utc_offset_is_accepted() -> None:
    response = await request_reports(
        headers=AUTHORIZATION,
        since="2020-01-01T00:00:00+00:00",
    )

    assert response.status_code == 200
    assert len(response.json()["data"]) == 20


@pytest.mark.asyncio
async def test_endpoint_requires_pvmbg_bearer_token() -> None:
    missing_response = await request_reports()
    invalid_response = await request_reports(
        headers={"Authorization": "Bearer wrong-token"},
    )

    assert missing_response.status_code == 401
    assert invalid_response.status_code == 403
