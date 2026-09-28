from fastapi import APIRouter, Request, Response, status

from ..models import HealthResponse
from ..state import RuntimeState

router = APIRouter(tags=["health"])


@router.get(
    "/health",
    response_model=HealthResponse,
    responses={status.HTTP_503_SERVICE_UNAVAILABLE: {"model": HealthResponse}},
)
async def health(request: Request, response: Response) -> HealthResponse:
    """Report normal or controlled-outage service health"""
    runtime: RuntimeState = request.app.state.runtime
    outage_enabled, _ = await runtime.get_outage()

    if outage_enabled:
        response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
        return HealthResponse(status="outage")

    return HealthResponse(status="ok")
