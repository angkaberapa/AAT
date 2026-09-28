from fastapi import APIRouter, Request

from ..models import SchemaVersionRequest, SchemaVersionResponse
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
