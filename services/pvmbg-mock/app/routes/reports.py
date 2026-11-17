from asyncio import sleep
from datetime import UTC, datetime, timedelta
from random import randint
from typing import Annotated
from zlib import crc32

from fastapi import APIRouter, Depends, HTTPException, Query, Request, Response, status
from fastapi.responses import JSONResponse

from ..auth import require_pvmbg_token
from ..config import Settings
from ..models import OutageMode, PayloadMode, SchemaVersion, VolcanicReport
from ..state import RuntimeState

router = APIRouter(tags=["volcanic reports"])


async def apply_response_delay(settings: Settings) -> None:
    """Wait for a random configured duration without blocking the event loop"""
    delay_ms = randint(
        settings.response_delay_min_ms,
        settings.response_delay_max_ms,
    )
    if delay_ms > 0:
        await sleep(delay_ms / 1000)


async def enforce_outage_behavior(runtime: RuntimeState) -> None:
    """Fail immediately or wait for recovery according to the active outage mode"""
    outage_enabled, outage_mode = await runtime.get_outage()
    if not outage_enabled:
        return
    if outage_mode == OutageMode.ERROR:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="PVMBG service is in a controlled outage",
        )

    await runtime.wait_until_outage_clears()


def project_report_for_schema(
    report: VolcanicReport,
    schema_version: SchemaVersion,
) -> dict[str, object]:
    """Project one stored report into the currently active response schema"""
    projected = report.model_dump(mode="json", exclude_none=True)
    if schema_version >= 2:
        confidence_bucket = crc32(report.report_id.encode("utf-8")) % 5000
        projected["confidence_level"] = report.confidence_level or round(0.5 + confidence_bucket / 10_000, 4)
    if schema_version == 3:
        projected["level"] = projected.pop("alert_level")
    elif schema_version == 4:
        projected["eruption_count_24h"] = str(projected["eruption_count_24h"])
    elif schema_version == 5:
        projected.pop("eruption_count_24h")
    return projected


def parse_since(
    since: Annotated[
        str | None,
        Query(description="Exclusive ISO 8601 UTC lower bound for reported_at"),
    ] = None,
) -> datetime | None:
    """Parse the team-contract timestamp format and report invalid input as HTTP 400"""
    if since is None:
        return None

    try:
        parsed = datetime.fromisoformat(since)
    except ValueError as error:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="since must be a valid ISO 8601 UTC timestamp",
        ) from error

    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="since must include UTC timezone information",
        )
    if parsed.utcoffset() != timedelta(0):
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="since must use UTC timezone",
        )

    return parsed.astimezone(UTC)


@router.get(
    "/volcanic-reports",
    dependencies=[Depends(require_pvmbg_token)],
    responses={
        status.HTTP_400_BAD_REQUEST: {"description": "Invalid since timestamp"},
        status.HTTP_401_UNAUTHORIZED: {"description": "Bearer token is required"},
        status.HTTP_403_FORBIDDEN: {"description": "Bearer token is invalid"},
        status.HTTP_503_SERVICE_UNAVAILABLE: {"description": "Controlled outage is active"},
    },
)
async def list_volcanic_reports(
    request: Request,
    since: Annotated[datetime | None, Depends(parse_since)],
) -> Response:
    """Return all reports newer than the optional exclusive UTC timestamp"""
    runtime: RuntimeState = request.app.state.runtime
    await enforce_outage_behavior(runtime)
    await apply_response_delay(request.app.state.settings)
    reports = await runtime.list_reports(since)
    schema_version = await runtime.get_schema_version()
    projected_reports = [project_report_for_schema(report, schema_version) for report in reports]
    mode = await runtime.get_payload_mode()
    if mode == PayloadMode.TRUNCATED:
        return Response(content=b'{"data":[{"report_id":"truncated"}', media_type="application/json")
    if mode == PayloadMode.INVALID_UTF8:
        return Response(content=b'{"data":["\xff"]}', media_type="application/json")
    if mode == PayloadMode.DEEP:
        deep = b'{"data":[' + (b'{"nested":' * 150) + b'null' + (b'}' * 150) + b']}'
        return Response(content=deep, media_type="application/json")
    if mode == PayloadMode.OVERSIZED:
        return JSONResponse(content={"data": projected_reports, "padding": "x" * (17 * 1024 * 1024)})
    return JSONResponse(content={"data": projected_reports})
