package feed

import (
	"errors"
	"testing"
	"time"
)

func TestParseDetectionUsesRawKeyFields(t *testing.T) {
	data := []byte(`{
		"satellite":"N20","acq_date":"2026-11-12","acq_time":243,
		"latitude":-2.51230,"longitude":113.80440,"confidence":"h","frp":12.5
	}`)
	message, err := Parse("detection", "1872", data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if message.Offset != "1872" {
		t.Fatalf("offset = %q", message.Offset)
	}
	wantKey := "N20|2026-11-12|243|-2.51230|113.80440"
	if message.Detection.Key != wantKey {
		t.Fatalf("key = %q, want %q", message.Detection.Key, wantKey)
	}
	wantTime := time.Date(2026, 11, 12, 2, 43, 0, 0, time.UTC)
	if !message.Detection.ObservedAt.Equal(wantTime) {
		t.Fatalf("observed_at = %v, want %v", message.Detection.ObservedAt, wantTime)
	}
	if message.Detection.FRP == nil || *message.Detection.FRP != 12.5 {
		t.Fatalf("frp = %v", message.Detection.FRP)
	}
}

func TestParseDetectionAcceptsClockWithColon(t *testing.T) {
	data := []byte(`{"satellite":"N21","acq_date":"2026-11-12","acq_time":"02:43","latitude":"-2.5","longitude":"113.8","confidence":"n"}`)
	message, err := Parse("detection", "", data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if message.Detection.ObservedAt.Hour() != 2 || message.Detection.ObservedAt.Minute() != 43 {
		t.Fatalf("observed_at = %v", message.Detection.ObservedAt)
	}
}

func TestParseLowConfidenceIsExcluded(t *testing.T) {
	data := []byte(`{"satellite":"N20","acq_date":"2026-11-12","acq_time":"0243","latitude":-2.5,"longitude":113.8,"confidence":"low"}`)
	if _, err := Parse("detection", "1", data); !errors.Is(err, ErrLowConfidence) {
		t.Fatalf("err = %v, want ErrLowConfidence", err)
	}
}

func TestParseRetractUsesDetectionKey(t *testing.T) {
	message, err := Parse("retract", "1900", []byte(`{"detection_key":"N20|2026-11-12|243|-2.5|113.8"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if message.Kind != KindRetract || message.Key != "N20|2026-11-12|243|-2.5|113.8" {
		t.Fatalf("message = %+v", message)
	}
}

func TestParseHeartbeatTakesOffset(t *testing.T) {
	message, err := Parse("heartbeat", "", []byte(`{"offset":1901}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if message.Kind != KindHeartbeat || message.Offset != "1901" {
		t.Fatalf("message = %+v", message)
	}
}

func TestParseRejectsUnknownType(t *testing.T) {
	if _, err := Parse("snapshot", "1", []byte(`{}`)); err == nil {
		t.Fatal("expected an error")
	}
}
