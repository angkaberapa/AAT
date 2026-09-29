from secrets import compare_digest
from typing import Annotated

from fastapi import Depends, HTTPException, Request, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from .config import Settings

bearer_scheme = HTTPBearer(
    bearerFormat="opaque PVMBG token",
    scheme_name="PVMBGBearer",
    description="PVMBG service credential supplied through Authorization: Bearer <token>",
    auto_error=False,
)


def unauthorized(detail: str) -> HTTPException:
    """Build a Bearer authentication error with the required challenge header"""
    return HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail=detail,
        headers={"WWW-Authenticate": "Bearer"},
    )


async def require_pvmbg_token(
    request: Request,
    credentials: Annotated[
        HTTPAuthorizationCredentials | None,
        Depends(bearer_scheme),
    ],
) -> None:
    """Require the configured PVMBG Bearer token for a protected endpoint"""
    if credentials is None:
        raise unauthorized("Authorization Bearer token is required")
    if credentials.scheme.lower() != "bearer" or not credentials.credentials.strip():
        raise unauthorized("Authorization header must use the Bearer scheme")

    settings: Settings = request.app.state.settings
    provided_token = credentials.credentials.strip()
    expected_token = settings.pvmbg_token.get_secret_value()

    if not compare_digest(provided_token, expected_token):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Invalid PVMBG token",
        )
