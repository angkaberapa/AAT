from asyncio import sleep
from datetime import UTC, datetime, timedelta
from random import randint
from typing import Annotated
from zlib import crc32

from fastapi import APIRouter, Depends, HTTPException, Query, Request, status

from ..auth import require_pvmbg_token
from ..config import Settings
from ..models import OutageMode, SchemaVersion, VolcanicReport, VolcanicReportsResponse
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
) -> VolcanicReport:
    """Project one stored report into the currently active response schema"""
    if schema_version == 1:
        return report.model_copy(update={"confidence_level": None})
    if report.confidence_level is not None:
        return report

    confidence_bucket = crc32(report.report_id.encode("utf-8")) % 5000
    confidence_level = round(0.5 + confidence_bucket / 10_000, 4)
    return report.model_copy(update={"confidence_level": confidence_level})


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
    response_model=VolcanicReportsResponse,
    response_model_exclude_none=True,
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
) -> VolcanicReportsResponse:
    """Return all reports newer than the optional exclusive UTC timestamp"""
    runtime: RuntimeState = request.app.state.runtime
    await enforce_outage_behavior(runtime)
    await apply_response_delay(request.app.state.settings)
    reports = await runtime.list_reports(since)
    schema_version = await runtime.get_schema_version()
    projected_reports = [project_report_for_schema(report, schema_version) for report in reports]
    return VolcanicReportsResponse(data=projected_reports)
