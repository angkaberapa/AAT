import asyncio
from datetime import UTC, datetime, timedelta

import pytest

from app.models import AlertLevel, OutageMode, VolcanicReport, VolcanoId
from app.state import SEED_REPORT_COUNT, RuntimeState, build_seed_reports

FIXED_NOW = datetime(2026, 9, 28, 12, tzinfo=UTC)


@pytest.mark.asyncio
async def test_seeded_state_contains_required_historical_data() -> None:
    state = RuntimeState.seeded(FIXED_NOW)

    reports = await state.list_reports()

    assert len(reports) == SEED_REPORT_COUNT == 20
    assert len({report.report_id for report in reports}) == SEED_REPORT_COUNT
    assert all(report.reported_at < FIXED_NOW for report in reports)
    assert reports == sorted(reports, key=lambda report: report.reported_at)
    assert {report.volcano_id for report in reports} == set(VolcanoId)
    assert {report.alert_level for report in reports} == set(AlertLevel)
    assert all(report.confidence_level is None for report in reports)


def test_seed_builder_rejects_naive_reference_time() -> None:
    with pytest.raises(ValueError, match="timezone"):
        build_seed_reports(
            datetime(2026, 9, 28, 12),  # noqa: DTZ001 - deliberately naive
        )


@pytest.mark.asyncio
async def test_default_schema_and_outage_state() -> None:
    state = RuntimeState.seeded(FIXED_NOW)

    assert await state.get_schema_version() == 1
    assert await state.get_outage() == (False, OutageMode.ERROR)


@pytest.mark.asyncio
async def test_since_filter_is_exclusive() -> None:
    state = RuntimeState.seeded(FIXED_NOW)
    all_reports = await state.list_reports()
    boundary_index = 9

    filtered = await state.list_reports(all_reports[boundary_index].reported_at)

    assert filtered == all_reports[boundary_index + 1 :]


@pytest.mark.asyncio
async def test_since_filter_rejects_naive_datetime() -> None:
    state = RuntimeState.seeded(FIXED_NOW)

    with pytest.raises(ValueError, match="timezone"):
        await state.list_reports(
            datetime(2026, 9, 28, 12),  # noqa: DTZ001 - deliberately naive
        )


@pytest.mark.asyncio
async def test_add_report_preserves_order_and_rejects_duplicate_id() -> None:
    state = RuntimeState.seeded(FIXED_NOW)
    earlier_report = VolcanicReport(
        report_id="PVMBG-RPT-999999",
        volcano_id=VolcanoId.MERAPI,
        alert_level=AlertLevel.NORMAL,
        eruption_count_24h=0,
        ash_column_height_m=0,
        reported_at=FIXED_NOW - timedelta(hours=1),
    )

    await state.add_report(earlier_report)
    reports = await state.list_reports()

    assert reports[0] == earlier_report
    with pytest.raises(ValueError, match="duplicate report_id"):
        await state.add_report(earlier_report)


@pytest.mark.asyncio
async def test_returned_reports_cannot_mutate_internal_state() -> None:
    state = RuntimeState.seeded(FIXED_NOW)
    reports = await state.list_reports()
    original_id = reports[0].report_id
    reports[0].report_id = "CHANGED-OUTSIDE-STATE"

    fresh_reports = await state.list_reports()

    assert fresh_reports[0].report_id == original_id


@pytest.mark.asyncio
async def test_schema_and_outage_state_can_change_without_recreating_state() -> None:
    state = RuntimeState.seeded(FIXED_NOW)

    await state.set_schema_version(2)
    await state.set_outage(True, OutageMode.HANG)

    assert await state.get_schema_version() == 2
    assert await state.get_outage() == (True, OutageMode.HANG)

    await state.set_schema_version(1)
    await state.set_outage(False, OutageMode.ERROR)

    assert await state.get_schema_version() == 1
    assert await state.get_outage() == (False, OutageMode.ERROR)


@pytest.mark.asyncio
async def test_concurrent_report_id_reservations_are_unique() -> None:
    state = RuntimeState.seeded(FIXED_NOW)

    report_ids = await asyncio.gather(*(state.next_report_id() for _ in range(100)))

    assert len(report_ids) == len(set(report_ids)) == 100
    assert report_ids[0] == "PVMBG-RPT-000021"
    assert report_ids[-1] == "PVMBG-RPT-000120"
