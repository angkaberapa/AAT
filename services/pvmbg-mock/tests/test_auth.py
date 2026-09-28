from collections.abc import Generator

import pytest
from fastapi import Depends, FastAPI
from httpx import ASGITransport, AsyncClient, Response

from app.auth import require_pvmbg_token
from app.config import get_settings
from app.main import create_app


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Provide an isolated credential for every authentication test"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "0")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "0")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


def create_auth_test_app() -> FastAPI:
    """Create the real application with one test-only protected endpoint"""
    application = create_app()

    @application.get("/_test/protected", dependencies=[Depends(require_pvmbg_token)])
    async def protected_endpoint() -> dict[str, str]:
        return {"status": "authorized"}

    return application


async def request_protected(headers: dict[str, str] | None = None) -> Response:
    """Call the test-only protected endpoint through the real app lifespan"""
    application = create_auth_test_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        return await client.get("/_test/protected", headers=headers)


@pytest.mark.asyncio
async def test_correct_bearer_token_is_accepted() -> None:
    response = await request_protected(
        {"Authorization": "Bearer pvmbg.v1.test-secret"},
    )

    assert response.status_code == 200
    assert response.json() == {"status": "authorized"}


@pytest.mark.asyncio
async def test_bearer_scheme_is_case_insensitive() -> None:
    response = await request_protected(
        {"Authorization": "bearer pvmbg.v1.test-secret"},
    )

    assert response.status_code == 200


@pytest.mark.asyncio
async def test_missing_authorization_is_unauthorized() -> None:
    response = await request_protected()

    assert response.status_code == 401
    assert response.headers["www-authenticate"] == "Bearer"


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "authorization",
    ["Basic dXNlcjpwYXNz", "Bearer", "", "Token pvmbg.v1.test-secret"],
)
async def test_malformed_authorization_is_unauthorized(authorization: str) -> None:
    response = await request_protected({"Authorization": authorization})

    assert response.status_code == 401
    assert response.headers["www-authenticate"] == "Bearer"


@pytest.mark.asyncio
async def test_wrong_bearer_token_is_forbidden() -> None:
    response = await request_protected(
        {"Authorization": "Bearer definitely-not-the-token"},
    )

    assert response.status_code == 403


@pytest.mark.asyncio
async def test_bmkg_credential_is_not_accepted() -> None:
    response = await request_protected({"X-BMKG-Key": "bmkg-key"})

    assert response.status_code == 401


@pytest.mark.asyncio
async def test_health_remains_public() -> None:
    application = create_auth_test_app()
    async with (
        application.router.lifespan_context(application),
        AsyncClient(
            transport=ASGITransport(app=application),
            base_url="http://test",
        ) as client,
    ):
        response = await client.get("/health")

    assert response.status_code == 200


def test_openapi_declares_http_bearer_security_scheme() -> None:
    schema = create_app().openapi()

    security_scheme = schema["components"]["securitySchemes"]["PVMBGBearer"]
    reports_operation = schema["paths"]["/volcanic-reports"]["get"]

    assert security_scheme["type"] == "http"
    assert security_scheme["scheme"] == "bearer"
    assert reports_operation["security"] == [{"PVMBGBearer": []}]
