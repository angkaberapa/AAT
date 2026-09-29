from functools import lru_cache
from typing import Literal, Self

from pydantic import Field, SecretStr, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

LogLevel = Literal["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"]


class Settings(BaseSettings):
    """Validated service configuration loaded exclusively from the environment"""

    model_config = SettingsConfigDict(
        case_sensitive=True,
        env_file=None,
        extra="ignore",
        hide_input_in_errors=True,
    )

    pvmbg_token: SecretStr = Field(validation_alias="PVMBG_TOKEN")
    response_delay_min_ms: int = Field(
        default=500,
        ge=0,
        validation_alias="RESPONSE_DELAY_MIN_MS",
    )
    response_delay_max_ms: int = Field(
        default=3000,
        ge=0,
        validation_alias="RESPONSE_DELAY_MAX_MS",
    )
    new_event_interval_seconds: int = Field(
        default=10,
        gt=0,
        le=10,
        validation_alias="NEW_EVENT_INTERVAL_SECONDS",
    )
    log_level: LogLevel = Field(default="INFO", validation_alias="LOG_LEVEL")

    @field_validator("pvmbg_token", mode="before")
    @classmethod
    def token_must_not_be_blank(cls, value: object) -> object:
        """Reject empty and whitespace-only credentials"""
        if not isinstance(value, str) or not value.strip():
            raise ValueError("PVMBG_TOKEN must not be blank")
        return value

    @field_validator("log_level", mode="before")
    @classmethod
    def normalize_log_level(cls, value: object) -> object:
        """Accept conventional case-insensitive log-level values"""
        return value.strip().upper() if isinstance(value, str) else value

    @model_validator(mode="after")
    def delay_range_must_be_ordered(self) -> Self:
        """Ensure the configured random-delay range is valid"""
        if self.response_delay_max_ms < self.response_delay_min_ms:
            raise ValueError(
                "RESPONSE_DELAY_MAX_MS must be greater than or equal to RESPONSE_DELAY_MIN_MS"
            )
        return self


@lru_cache
def get_settings() -> Settings:
    """Load and cache one immutable-by-convention configuration instance"""
    return Settings()  # pyright: ignore[reportCallIssue]
