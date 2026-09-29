import asyncio
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager, suppress

from fastapi import FastAPI

from .config import get_settings
from .generator import run_report_generator
from .observability import configure_logging, observe_request
from .routes.admin import router as admin_router
from .routes.health import router as health_router
from .routes.reports import router as reports_router
from .state import RuntimeState


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Validate configuration before the service starts accepting requests"""
    settings = get_settings()
    configure_logging(settings.log_level)
    app.state.settings = settings
    app.state.runtime = RuntimeState.seeded()
    generator_task = asyncio.create_task(
        run_report_generator(
            app.state.runtime,
            settings.new_event_interval_seconds,
        ),
        name="pvmbg-report-generator",
    )
    app.state.generator_task = generator_task

    try:
        yield
    finally:
        generator_task.cancel()
        with suppress(asyncio.CancelledError):
            await generator_task


def create_app() -> FastAPI:
    """Create the FastAPI application"""
    application = FastAPI(
        title="PVMBG Mock Service",
        description="Mock PVMBG service for the IF4031 distributed application project.",
        version="0.1.0",
        lifespan=lifespan,
    )
    application.middleware("http")(observe_request)
    application.include_router(admin_router)
    application.include_router(health_router)
    application.include_router(reports_router)
    return application


app = create_app()
