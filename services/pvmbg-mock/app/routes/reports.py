from datetime import UTC, datetime, timedelta
from typing import Annotated
from zlib import crc32

from fastapi import APIRouter, Depends, HTTPException, Query, Request, status

from ..auth import require_pvmbg_token
from ..models import SchemaVersion, VolcanicReport, VolcanicReportsResponse
from ..state import RuntimeState

router = APIRouter(tags=["volcanic reports"])


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
    },
)
async def list_volcanic_reports(
    request: Request,
    since: Annotated[datetime | None, Depends(parse_since)],
) -> VolcanicReportsResponse:
    """Return all reports newer than the optional exclusive UTC timestamp"""
    runtime: RuntimeState = request.app.state.runtime
    reports = await runtime.list_reports(since)
    schema_version = await runtime.get_schema_version()
    projected_reports = [project_report_for_schema(report, schema_version) for report in reports]
    return VolcanicReportsResponse(data=projected_reports)
