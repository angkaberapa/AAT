from secrets import compare_digest
from typing import Annotated

from fastapi import Header, HTTPException, Request, status

from .config import Settings


def unauthorized(detail: str) -> HTTPException:
    """Build a Bearer authentication error with the required challenge header"""
    return HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail=detail,
        headers={"WWW-Authenticate": "Bearer"},
    )


async def require_pvmbg_token(
    request: Request,
    authorization: Annotated[str | None, Header(alias="Authorization")] = None,
) -> None:
    """Require the configured PVMBG Bearer token for a protected endpoint"""
    if authorization is None:
        raise unauthorized("Authorization Bearer token is required")

    parts = authorization.strip().split(maxsplit=1)
    if len(parts) != 2 or parts[0].lower() != "bearer" or not parts[1].strip():
        raise unauthorized("Authorization header must use the Bearer scheme")

    settings: Settings = request.app.state.settings
    provided_token = parts[1].strip()
    expected_token = settings.pvmbg_token.get_secret_value()

    if not compare_digest(provided_token, expected_token):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Invalid PVMBG token",
        )
