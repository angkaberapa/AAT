from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from fastapi import FastAPI

from .config import get_settings


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Validate configuration before the service starts accepting requests"""
    app.state.settings = get_settings()
    yield


def create_app() -> FastAPI:
    """Create the FastAPI application"""
    return FastAPI(
        title="PVMBG Mock Service",
        description="Mock PVMBG service for the IF4031 distributed application project.",
        version="0.1.0",
        lifespan=lifespan,
    )


app = create_app()
