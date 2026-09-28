import asyncio
import json
import logging
import sys
from contextvars import ContextVar
from datetime import UTC, datetime
from time import perf_counter
from typing import Any
from uuid import uuid4

from fastapi import Request, Response

SERVICE_NAME = "pvmbg-mock"
CORRELATION_HEADER = "X-Correlation-ID"
correlation_id_context: ContextVar[str] = ContextVar(
    "correlation_id",
    default="-",
)


class JsonFormatter(logging.Formatter):
    """Serialize application log records as one-line JSON objects"""

    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "ts": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
            "level": record.levelname,
            "service": SERVICE_NAME,
            "correlation_id": getattr(
                record,
                "correlation_id",
                correlation_id_context.get(),
            ),
            "msg": record.getMessage(),
        }
        event_fields = getattr(record, "event_fields", {})
        payload.update((key, value) for key, value in event_fields.items() if key not in payload)
        if record.exc_info:
            payload["exception"] = self.formatException(record.exc_info)
        return json.dumps(payload, ensure_ascii=False, separators=(",", ":"))


def configure_logging(level: str) -> None:
    """Configure service and Uvicorn logs to use the JSON stdout format"""
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JsonFormatter())

    for logger_name in ("pvmbg_mock", "uvicorn", "uvicorn.error", "uvicorn.access"):
        configured_logger = logging.getLogger(logger_name)
        configured_logger.handlers.clear()
        configured_logger.addHandler(handler)
        configured_logger.setLevel(level)
        configured_logger.propagate = False


logger = logging.getLogger("pvmbg_mock")


def log_event(
    level: int,
    message: str,
    *,
    correlation_id: str | None = None,
    **event_fields: object,
) -> None:
    """Emit a structured service event with optional correlation override"""
    extra: dict[str, object] = {"event_fields": event_fields}
    if correlation_id is not None:
        extra["correlation_id"] = correlation_id
    logger.log(level, message, extra=extra)


async def observe_request(request: Request, call_next: Any) -> Response:
    """Propagate correlation IDs and emit one structured access log per request"""
    correlation_id = request.headers.get(CORRELATION_HEADER, "").strip() or str(uuid4())
    context_token = correlation_id_context.set(correlation_id)
    started_at = perf_counter()

    try:
        response = await call_next(request)
    except asyncio.CancelledError:
        log_event(
            logging.WARNING,
            "request cancelled",
            method=request.method,
            path=request.url.path,
            status=499,
            latency_ms=round((perf_counter() - started_at) * 1000, 3),
        )
        raise
    except Exception:
        log_event(
            logging.ERROR,
            "request failed",
            method=request.method,
            path=request.url.path,
            status=500,
            latency_ms=round((perf_counter() - started_at) * 1000, 3),
        )
        raise
    else:
        response.headers[CORRELATION_HEADER] = correlation_id
        log_event(
            logging.INFO,
            "request completed",
            method=request.method,
            path=request.url.path,
            status=response.status_code,
            latency_ms=round((perf_counter() - started_at) * 1000, 3),
        )
        return response
    finally:
        correlation_id_context.reset(context_token)
