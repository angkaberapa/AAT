import asyncio
from contextlib import suppress
from datetime import UTC, datetime

import pytest

import app.generator as generator_module
from app.generator import generate_report, run_report_generator
from app.models import AlertLevel, OutageMode, VolcanoId
from app.state import SEED_REPORT_COUNT, RuntimeState

FIXED_NOW = datetime(2026, 9, 28, 13, 30, tzinfo=UTC)


@pytest.mark.asyncio
async def test_generate_report_adds_varied_data_during_outage() -> None:
    runtime = RuntimeState.seeded(FIXED_NOW)
    await runtime.set_outage(True, OutageMode.ERROR)

    report = await generate_report(runtime, reported_at=FIXED_NOW)
    reports = await runtime.list_reports()

    assert len(reports) == SEED_REPORT_COUNT + 1
    assert reports[-1] == report
    assert report.report_id == "PVMBG-RPT-000021"
    assert report.reported_at == FIXED_NOW
    assert report.volcano_id in VolcanoId
    assert report.alert_level in AlertLevel
    assert report.confidence_level is None


@pytest.mark.asyncio
async def test_generator_uses_configured_interval_repeatedly(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    runtime = RuntimeState.seeded(FIXED_NOW)
    sleep_calls: list[int] = []
    second_sleep_started = asyncio.Event()
    block_second_sleep = asyncio.Event()

    async def controlled_sleep(seconds: int) -> None:
        sleep_calls.append(seconds)
        if len(sleep_calls) == 1:
            return
        second_sleep_started.set()
        await block_second_sleep.wait()

    monkeypatch.setattr(generator_module, "sleep", controlled_sleep)
    task = asyncio.create_task(run_report_generator(runtime, interval_seconds=4))

    await asyncio.wait_for(second_sleep_started.wait(), timeout=1)
    task.cancel()
    with suppress(asyncio.CancelledError):
        await task

    assert sleep_calls == [4, 4]
    assert len(await runtime.list_reports()) == SEED_REPORT_COUNT + 1
