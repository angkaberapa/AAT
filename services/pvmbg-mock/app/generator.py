import logging
from asyncio import sleep
from datetime import UTC, datetime

from .models import AlertLevel, VolcanicReport, VolcanoId
from .observability import log_event
from .state import RuntimeState


def build_generated_report(report_id: str, reported_at: datetime) -> VolcanicReport:
    """Build a varied report deterministically from its reserved sequence number"""
    sequence = int(report_id.rsplit("-", maxsplit=1)[-1])
    volcanoes = list(VolcanoId)
    alert_levels = list(AlertLevel)
    return VolcanicReport(
        report_id=report_id,
        volcano_id=volcanoes[(sequence - 1) % len(volcanoes)],
        alert_level=alert_levels[(sequence - 1) % len(alert_levels)],
        eruption_count_24h=(sequence * 3) % 12,
        ash_column_height_m=float((sequence % 8) * 250),
        reported_at=reported_at,
        confidence_level=None,
    )


async def generate_report(
    runtime: RuntimeState,
    *,
    reported_at: datetime | None = None,
) -> VolcanicReport:
    """Generate and store one report independently of controlled-outage state"""
    report_id = await runtime.next_report_id()
    report = build_generated_report(report_id, reported_at or datetime.now(UTC))
    await runtime.add_report(report)
    log_event(
        logging.INFO,
        "volcanic report generated",
        correlation_id=f"generator-{report_id}",
        report_id=report_id,
        volcano_id=report.volcano_id.value,
        alert_level=report.alert_level.value,
    )
    return report


async def run_report_generator(runtime: RuntimeState, interval_seconds: int) -> None:
    """Continuously add one report after each configured interval"""
    while True:
        await sleep(interval_seconds)
        await generate_report(runtime)
