from datetime import UTC, datetime, timedelta, timezone

import pytest
from pydantic import ValidationError

from app.models import (
    AlertLevel,
    OutageMode,
    OutageRequest,
    SchemaVersionRequest,
    VolcanicReport,
    VolcanicReportsResponse,
    VolcanoId,
)


def valid_report_data() -> dict[str, object]:
    """Return a fresh valid payload for model tests"""
    return {
        "report_id": "PVMBG-RPT-000001",
        "volcano_id": "MERAPI",
        "alert_level": "Siaga",
        "eruption_count_24h": 7,
        "ash_column_height_m": 1500.0,
        "reported_at": "2026-09-25T10:00:00Z",
    }


def test_valid_report_and_response_envelope() -> None:
    report = VolcanicReport.model_validate(valid_report_data())
    response = VolcanicReportsResponse(data=[report])

    assert report.volcano_id is VolcanoId.MERAPI
    assert report.alert_level is AlertLevel.SIAGA
    assert report.reported_at == datetime(2026, 9, 25, 10, tzinfo=UTC)
    assert response.data == [report]


def test_optional_confidence_level_is_omitted_from_old_schema_json() -> None:
    report = VolcanicReport.model_validate(valid_report_data())

    serialized = report.model_dump(mode="json", exclude_none=True)

    assert "confidence_level" not in serialized
    assert serialized["reported_at"] == "2026-09-25T10:00:00Z"


def test_confidence_level_is_included_for_new_schema_json() -> None:
    payload = valid_report_data()
    payload["confidence_level"] = 0.82

    report = VolcanicReport.model_validate(payload)

    assert report.model_dump(mode="json", exclude_none=True)["confidence_level"] == 0.82


@pytest.mark.parametrize("volcano_id", ["UNKNOWN", "merapi", ""])
def test_unknown_volcano_id_is_rejected(volcano_id: str) -> None:
    payload = valid_report_data()
    payload["volcano_id"] = volcano_id

    with pytest.raises(ValidationError):
        VolcanicReport.model_validate(payload)


@pytest.mark.parametrize("alert_level", ["Bahaya", "siaga", ""])
def test_unknown_alert_level_is_rejected(alert_level: str) -> None:
    payload = valid_report_data()
    payload["alert_level"] = alert_level

    with pytest.raises(ValidationError):
        VolcanicReport.model_validate(payload)


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("eruption_count_24h", -1),
        ("ash_column_height_m", -0.1),
        ("confidence_level", -0.01),
        ("confidence_level", 1.01),
    ],
)
def test_out_of_range_numeric_values_are_rejected(field: str, value: object) -> None:
    payload = valid_report_data()
    payload[field] = value

    with pytest.raises(ValidationError):
        VolcanicReport.model_validate(payload)


def test_blank_report_id_is_rejected() -> None:
    payload = valid_report_data()
    payload["report_id"] = "   "

    with pytest.raises(ValidationError):
        VolcanicReport.model_validate(payload)


def test_naive_reported_at_is_rejected() -> None:
    payload = valid_report_data()
    payload["reported_at"] = datetime(2026, 9, 25, 10)  # noqa: DTZ001 - deliberately naive

    with pytest.raises(ValidationError):
        VolcanicReport.model_validate(payload)


def test_reported_at_is_normalized_to_utc() -> None:
    source_timezone = timezone(timedelta(hours=7))
    payload = valid_report_data()
    payload["reported_at"] = datetime(2026, 9, 25, 17, tzinfo=source_timezone)

    report = VolcanicReport.model_validate(payload)

    assert report.reported_at == datetime(2026, 9, 25, 10, tzinfo=UTC)
    assert report.model_dump(mode="json")["reported_at"] == "2026-09-25T10:00:00Z"


@pytest.mark.parametrize("version", [0, 3, "2"])
def test_invalid_schema_version_is_rejected(version: object) -> None:
    with pytest.raises(ValidationError):
        SchemaVersionRequest.model_validate({"version": version})


def test_outage_mode_defaults_to_error() -> None:
    request = OutageRequest.model_validate({"enabled": False})

    assert request.enabled is False
    assert request.mode is OutageMode.ERROR


def test_invalid_outage_mode_and_non_boolean_enabled_are_rejected() -> None:
    with pytest.raises(ValidationError):
        OutageRequest.model_validate({"enabled": True, "mode": "offline"})

    with pytest.raises(ValidationError):
        OutageRequest.model_validate({"enabled": "true"})
