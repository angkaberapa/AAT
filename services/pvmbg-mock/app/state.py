import asyncio
from collections.abc import Iterable
from datetime import UTC, datetime, timedelta

from .models import AlertLevel, OutageMode, SchemaVersion, VolcanicReport, VolcanoId

SEED_REPORT_COUNT = 20


def build_seed_reports(now: datetime | None = None) -> list[VolcanicReport]:
    """Create deterministic historical reports for repeatable demos and tests"""
    reference_time = now or datetime.now(UTC)
    if reference_time.tzinfo is None or reference_time.utcoffset() is None:
        raise ValueError("seed reference time must include timezone information")
    reference_time = reference_time.astimezone(UTC)

    volcanoes = list(VolcanoId)
    alert_levels = list(AlertLevel)
    reports: list[VolcanicReport] = []

    for index in range(SEED_REPORT_COUNT):
        reports.append(
            VolcanicReport(
                report_id=f"PVMBG-RPT-{index + 1:06d}",
                volcano_id=volcanoes[index % len(volcanoes)],
                alert_level=alert_levels[index % len(alert_levels)],
                eruption_count_24h=(index * 3) % 12,
                ash_column_height_m=float((index % 8) * 250),
                reported_at=reference_time - timedelta(minutes=SEED_REPORT_COUNT - index),
                confidence_level=None,
            )
        )

    return reports


class RuntimeState:
    """Concurrency-safe mutable state shared by routes and background tasks"""

    def __init__(
        self,
        reports: Iterable[VolcanicReport],
        *,
        next_report_sequence: int,
    ) -> None:
        copied_reports = [report.model_copy(deep=True) for report in reports]
        report_ids = [report.report_id for report in copied_reports]
        if len(report_ids) != len(set(report_ids)):
            raise ValueError("report_id values must be unique")
        if next_report_sequence < 1:
            raise ValueError("next report sequence must be positive")

        self._reports = sorted(copied_reports, key=lambda report: report.reported_at)
        self._schema_version: SchemaVersion = 1
        self._outage_enabled = False
        self._outage_mode = OutageMode.ERROR
        self._next_report_sequence = next_report_sequence
        self._lock = asyncio.Lock()

    @classmethod
    def seeded(cls, now: datetime | None = None) -> "RuntimeState":
        """Create initial runtime state with the required historical records"""
        return cls(
            build_seed_reports(now),
            next_report_sequence=SEED_REPORT_COUNT + 1,
        )

    async def list_reports(self, since: datetime | None = None) -> list[VolcanicReport]:
        """Return timestamp-ordered copies, optionally newer than an exclusive bound"""
        normalized_since: datetime | None = None
        if since is not None:
            if since.tzinfo is None or since.utcoffset() is None:
                raise ValueError("since must include timezone information")
            normalized_since = since.astimezone(UTC)

        async with self._lock:
            reports = self._reports
            if normalized_since is not None:
                reports = [report for report in reports if report.reported_at > normalized_since]
            return [report.model_copy(deep=True) for report in reports]

    async def add_report(self, report: VolcanicReport) -> None:
        """Store a report while preserving unique IDs and timestamp order"""
        async with self._lock:
            if any(existing.report_id == report.report_id for existing in self._reports):
                raise ValueError(f"duplicate report_id: {report.report_id}")
            self._reports.append(report.model_copy(deep=True))
            self._reports.sort(key=lambda item: item.reported_at)

    async def next_report_id(self) -> str:
        """Atomically reserve and return the next report identifier"""
        async with self._lock:
            sequence = self._next_report_sequence
            self._next_report_sequence += 1
        return f"PVMBG-RPT-{sequence:06d}"

    async def get_schema_version(self) -> SchemaVersion:
        """Return the active response schema version"""
        async with self._lock:
            return self._schema_version

    async def set_schema_version(self, version: SchemaVersion) -> None:
        """Change the active response schema version"""
        if version not in (1, 2):
            raise ValueError("schema version must be 1 or 2")
        async with self._lock:
            self._schema_version = version

    async def get_outage(self) -> tuple[bool, OutageMode]:
        """Return the current controlled-outage state"""
        async with self._lock:
            return self._outage_enabled, self._outage_mode

    async def set_outage(self, enabled: bool, mode: OutageMode) -> None:
        """Enable or disable controlled outage behavior"""
        async with self._lock:
            self._outage_enabled = enabled
            self._outage_mode = mode
