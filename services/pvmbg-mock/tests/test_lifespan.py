import asyncio
from collections.abc import Generator

import pytest

import app.main as main_module
from app.config import get_settings
from app.main import create_app
from app.state import RuntimeState


@pytest.fixture(autouse=True)
def configured_environment(monkeypatch: pytest.MonkeyPatch) -> Generator[None, None, None]:
    """Provide valid configuration for lifespan tests"""
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "0")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "0")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.mark.asyncio
async def test_lifespan_starts_and_stops_generator_task(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    started = asyncio.Event()
    stopped = asyncio.Event()

    async def controlled_generator(
        runtime: RuntimeState,
        interval_seconds: int,
    ) -> None:
        assert interval_seconds == 10
        started.set()
        try:
            await asyncio.Event().wait()
        finally:
            stopped.set()

    monkeypatch.setattr(main_module, "run_report_generator", controlled_generator)
    application = create_app()

    async with application.router.lifespan_context(application):
        await asyncio.wait_for(started.wait(), timeout=1)
        generator_task: asyncio.Task[None] = application.state.generator_task
        assert not generator_task.done()

    assert generator_task.cancelled()
    assert stopped.is_set()
