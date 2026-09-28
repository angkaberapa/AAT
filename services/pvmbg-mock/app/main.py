from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from fastapi import FastAPI

from .config import get_settings
from .routes.health import router as health_router
from .state import RuntimeState


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Validate configuration before the service starts accepting requests"""
    app.state.settings = get_settings()
    app.state.runtime = RuntimeState.seeded()
    yield


def create_app() -> FastAPI:
    """Create the FastAPI application"""
    application = FastAPI(
        title="PVMBG Mock Service",
        description="Mock PVMBG service for the IF4031 distributed application project.",
        version="0.1.0",
        lifespan=lifespan,
    )
    application.include_router(health_router)
    return application


app = create_app()
