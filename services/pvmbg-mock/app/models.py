from datetime import UTC, datetime
from enum import Enum
from typing import Literal

from pydantic import AwareDatetime, BaseModel, ConfigDict, Field, StrictBool, field_validator


class VolcanoId(str, Enum):
    """Volcano identifiers shared with the BNPB reference table"""

    MERAPI = "MERAPI"
    SEMERU = "SEMERU"
    SINABUNG = "SINABUNG"
    KRAKATAU = "KRAKATAU"
    LEWOTOBI = "LEWOTOBI"


class AlertLevel(str, Enum):
    """PVMBG volcanic activity levels"""

    NORMAL = "Normal"
    WASPADA = "Waspada"
    SIAGA = "Siaga"
    AWAS = "Awas"


class OutageMode(str, Enum):
    """Supported failure behavior for the controlled outage scenario"""

    ERROR = "error"
    HANG = "hang"


SchemaVersion = Literal[1, 2]
HealthStatus = Literal["ok", "outage"]


class VolcanicReport(BaseModel):
    """A volcanic report returned by the PVMBG mock"""

    model_config = ConfigDict(str_strip_whitespace=True)
    report_id: str = Field(min_length=1)
    volcano_id: VolcanoId
    alert_level: AlertLevel
    eruption_count_24h: int = Field(ge=0)
    ash_column_height_m: float = Field(ge=0)
    reported_at: AwareDatetime
    confidence_level: float | None = Field(default=None, ge=0, le=1)

    @field_validator("reported_at", mode="after")
    @classmethod
    def normalize_reported_at_to_utc(cls, value: datetime) -> datetime:
        """Store timestamps in UTC so serialized responses use one time basis"""
        return value.astimezone(UTC)


class VolcanicReportsResponse(BaseModel):
    """Response envelope for the volcanic reports endpoint"""

    data: list[VolcanicReport]


class SchemaVersionRequest(BaseModel):
    """Request body for changing the active response schema"""

    version: SchemaVersion


class SchemaVersionResponse(BaseModel):
    """Current schema version returned by the admin endpoint"""

    schema_version: SchemaVersion


class OutageRequest(BaseModel):
    """Request body for enabling or disabling a controlled outage"""

    enabled: StrictBool
    mode: OutageMode = OutageMode.ERROR


class OutageResponse(BaseModel):
    """Current controlled-outage state"""

    outage: bool
    mode: OutageMode | None = None


class HealthResponse(BaseModel):
    """Health response exposed by the service"""

    status: HealthStatus
    service: Literal["pvmbg-mock"] = "pvmbg-mock"


class ErrorResponse(BaseModel):
    """Stable error envelope for explicit service errors"""

    error: str = Field(min_length=1)
    message: str = Field(min_length=1)
