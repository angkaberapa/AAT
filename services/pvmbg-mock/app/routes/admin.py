from fastapi import APIRouter, Request

from ..models import (
    OutageRequest,
    OutageResponse,
    SchemaVersionRequest,
    SchemaVersionResponse,
)
from ..state import RuntimeState

router = APIRouter(prefix="/admin", tags=["admin"])


@router.post("/schema-version", response_model=SchemaVersionResponse)
async def set_schema_version(
    payload: SchemaVersionRequest,
    request: Request,
) -> SchemaVersionResponse:
    """Change the report response schema without restarting the service"""
    runtime: RuntimeState = request.app.state.runtime
    await runtime.set_schema_version(payload.version)
    return SchemaVersionResponse(schema_version=payload.version)


@router.post(
    "/outage",
    response_model=OutageResponse,
    response_model_exclude_none=True,
)
async def set_outage(payload: OutageRequest, request: Request) -> OutageResponse:
    """Enable or disable controlled failure behavior without restarting"""
    runtime: RuntimeState = request.app.state.runtime
    await runtime.set_outage(payload.enabled, payload.mode)
    return OutageResponse(
        outage=payload.enabled,
        mode=payload.mode if payload.enabled else None,
    )
