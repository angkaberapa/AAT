import pytest
from pydantic import ValidationError

from app.config import Settings

ENVIRONMENT_KEYS = (
    "PVMBG_TOKEN",
    "RESPONSE_DELAY_MIN_MS",
    "RESPONSE_DELAY_MAX_MS",
    "NEW_EVENT_INTERVAL_SECONDS",
    "LOG_LEVEL",
)


def load_settings_from_environment() -> Settings:
    """Build settings from environment variables prepared by each test"""
    return Settings()  # pyright: ignore[reportCallIssue]


@pytest.fixture(autouse=True)
def clean_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    """Keep every configuration test independent from the host environment"""
    for key in ENVIRONMENT_KEYS:
        monkeypatch.delenv(key, raising=False)


def test_required_token_and_defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "pvmbg.v1.test-secret")

    settings = load_settings_from_environment()

    assert settings.pvmbg_token.get_secret_value() == "pvmbg.v1.test-secret"
    assert settings.response_delay_min_ms == 500
    assert settings.response_delay_max_ms == 3000
    assert settings.new_event_interval_seconds == 10
    assert settings.log_level == "INFO"
    assert "pvmbg.v1.test-secret" not in repr(settings)


def test_valid_custom_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "custom-token")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "750")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "2500")
    monkeypatch.setenv("NEW_EVENT_INTERVAL_SECONDS", "5")
    monkeypatch.setenv("LOG_LEVEL", "debug")

    settings = load_settings_from_environment()

    assert settings.response_delay_min_ms == 750
    assert settings.response_delay_max_ms == 2500
    assert settings.new_event_interval_seconds == 5
    assert settings.log_level == "DEBUG"


@pytest.mark.parametrize("token", ["", "   "])
def test_token_cannot_be_blank(monkeypatch: pytest.MonkeyPatch, token: str) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", token)

    with pytest.raises(ValidationError):
        load_settings_from_environment()


def test_token_is_required() -> None:
    with pytest.raises(ValidationError):
        load_settings_from_environment()


def test_minimum_delay_cannot_be_negative(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "test-token")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "-1")

    with pytest.raises(ValidationError):
        load_settings_from_environment()


def test_maximum_delay_cannot_be_lower_than_minimum(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "test-token")
    monkeypatch.setenv("RESPONSE_DELAY_MIN_MS", "3000")
    monkeypatch.setenv("RESPONSE_DELAY_MAX_MS", "500")

    with pytest.raises(ValidationError):
        load_settings_from_environment()


def test_event_interval_must_be_positive(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "test-token")
    monkeypatch.setenv("NEW_EVENT_INTERVAL_SECONDS", "0")

    with pytest.raises(ValidationError):
        load_settings_from_environment()


def test_log_level_must_be_known(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PVMBG_TOKEN", "test-token")
    monkeypatch.setenv("LOG_LEVEL", "verbose")

    with pytest.raises(ValidationError):
        load_settings_from_environment()
