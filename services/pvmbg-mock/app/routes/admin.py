import logging

from fastapi import APIRouter, Request

from ..models import (
    OutageRequest,
    OutageResponse,
    PayloadModeRequest,
    PayloadModeResponse,
    SchemaVersionRequest,
    SchemaVersionResponse,
)
from ..observability import log_event
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
    log_event(
        logging.INFO,
        "schema version changed",
        schema_version=payload.version,
    )
    return SchemaVersionResponse(schema_version=payload.version)


@router.post("/payload-mode", response_model=PayloadModeResponse)
async def set_payload_mode(payload: PayloadModeRequest, request: Request) -> PayloadModeResponse:
    """Select a malformed wire response used by P3 robustness tests."""
    runtime: RuntimeState = request.app.state.runtime
    await runtime.set_payload_mode(payload.mode)
    log_event(logging.INFO, "payload mode changed", payload_mode=payload.mode.value)
    return PayloadModeResponse(mode=payload.mode)


@router.post(
    "/outage",
    response_model=OutageResponse,
    response_model_exclude_none=True,
)
async def set_outage(payload: OutageRequest, request: Request) -> OutageResponse:
    """Enable or disable controlled failure behavior without restarting"""
    runtime: RuntimeState = request.app.state.runtime
    await runtime.set_outage(payload.enabled, payload.mode)
    log_event(
        logging.INFO,
        "outage state changed",
        outage=payload.enabled,
        mode=payload.mode.value if payload.enabled else None,
    )
    return OutageResponse(
        outage=payload.enabled,
        mode=payload.mode if payload.enabled else None,
    )
