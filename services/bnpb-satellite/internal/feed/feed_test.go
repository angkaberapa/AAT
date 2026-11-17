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

func TestParseRealNestedDetection(t *testing.T) {
	data := []byte(`{"offset":2011,"detection":{"version":"2.0NRT","scan":0.38,"acq_date":"2026-11-14","acq_time":324,"confidence":"h","frp":67.91,"latitude":-3.32633,"longitude":119.33397,"satellite":"N20"}}`)
	message, err := Parse("detection", "2011", data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if message.Detection.Key != "N20|2026-11-14|324|-3.32633|119.33397" {
		t.Fatalf("key = %q", message.Detection.Key)
	}
	if message.Detection.FRP == nil || *message.Detection.FRP != 67.91 {
		t.Fatalf("frp = %v", message.Detection.FRP)
	}
}

func TestParseRealUpdateAndRetract(t *testing.T) {
	update, err := Parse("update", "2018", []byte(`{"offset":2018,"key":"N|2026-11-14|240|-8.06482|109.04909","changes":{"bright_ti5":271.64,"confidence":"l"}}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if update.Kind != KindUpdate || update.Key == "" || update.Confidence == nil || *update.Confidence != "l" {
		t.Fatalf("update = %+v", update)
	}
	retract, err := Parse("retract", "2012", []byte(`{"offset":2012,"reason":"false_positive","key":"N20|2026-11-14|318|-8.06722|109.04917"}`))
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	if retract.Key != "N20|2026-11-14|318|-8.06722|109.04917" {
		t.Fatalf("retract key = %q", retract.Key)
	}
}

func TestParseUnknownType(t *testing.T) {
	if _, err := Parse("snapshot", "1", []byte(`{}`)); err == nil {
		t.Fatal("expected an error")
	}
}
