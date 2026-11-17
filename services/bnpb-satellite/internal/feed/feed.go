// Package feed turns Feed Satelit SSE frames into domain detections.
package feed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

var ErrLowConfidence = errors.New("confidence low is excluded")

type kind int

const (
	KindDetection kind = iota
	KindUpdate
	KindRetract
	KindHeartbeat
)

// Message is one parsed SSE frame. Offset is the feed position to checkpoint
// after the frame has been applied.
type Message struct {
	Kind      kind
	Type      string
	Offset    string
	Detection domain.Detection
	Key       string
}

type rawDetection struct {
	Offset       flexString `json:"offset"`
	DetectionKey string     `json:"detection_key"`
	Satellite    string     `json:"satellite"`
	AcqDate      string     `json:"acq_date"`
	AcqTime      flexString `json:"acq_time"`
	Latitude     flexString `json:"latitude"`
	Longitude    flexString `json:"longitude"`
	Confidence   string     `json:"confidence"`
	FRP          *flexFloat `json:"frp"`
}

// Parse decodes one SSE frame. An empty event id is filled from the JSON
// offset field. Unknown types are rejected so the processor can count them.
func Parse(eventType, id string, data []byte) (Message, error) {
	switch eventType {
	case "detection", "update", "retract", "heartbeat":
	default:
		return Message{}, fmt.Errorf("unknown event type %q", eventType)
	}

	var raw rawDetection
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return Message{Offset: strings.TrimSpace(id)}, fmt.Errorf("decode %s: %w", eventType, err)
		}
	}
	offset := strings.TrimSpace(id)
	if offset == "" {
		offset = strings.TrimSpace(string(raw.Offset))
	}

	message := Message{Type: eventType, Offset: offset}
	switch eventType {
	case "heartbeat":
		message.Kind = KindHeartbeat
		return message, nil
	case "retract":
		key := strings.TrimSpace(raw.DetectionKey)
		if key == "" {
			built, err := detectionKey(raw)
			if err != nil {
				return Message{}, fmt.Errorf("retract: %w", err)
			}
			key = built
		}
		message.Kind = KindRetract
		message.Key = key
		return message, nil
	}

	detection, err := toDetection(raw, data)
	if err != nil {
		message.Kind = KindDetection
		return message, err
	}
	message.Kind = KindDetection
	if eventType == "update" {
		message.Kind = KindUpdate
	}
	message.Detection = detection
	return message, nil
}

func toDetection(raw rawDetection, data []byte) (domain.Detection, error) {
	key, err := detectionKey(raw)
	if err != nil {
		return domain.Detection{}, err
	}
	if strings.EqualFold(strings.TrimSpace(raw.Confidence), "low") ||
		strings.EqualFold(strings.TrimSpace(raw.Confidence), "l") {
		return domain.Detection{Key: key}, ErrLowConfidence
	}
	observedAt, err := observedAt(raw.AcqDate, string(raw.AcqTime))
	if err != nil {
		return domain.Detection{}, err
	}
	detection := domain.Detection{
		Key:        key,
		Satellite:  strings.TrimSpace(raw.Satellite),
		AcqDate:    strings.TrimSpace(raw.AcqDate),
		AcqTime:    strings.TrimSpace(string(raw.AcqTime)),
		Latitude:   strings.TrimSpace(string(raw.Latitude)),
		Longitude:  strings.TrimSpace(string(raw.Longitude)),
		Confidence: strings.TrimSpace(raw.Confidence),
		ObservedAt: observedAt,
		Active:     true,
		RawJSON:    string(data),
	}
	if raw.FRP != nil {
		value := float64(*raw.FRP)
		detection.FRP = &value
	}
	return detection, nil
}

func detectionKey(raw rawDetection) (string, error) {
	parts := []string{
		strings.TrimSpace(raw.Satellite),
		strings.TrimSpace(raw.AcqDate),
		strings.TrimSpace(string(raw.AcqTime)),
		strings.TrimSpace(string(raw.Latitude)),
		strings.TrimSpace(string(raw.Longitude)),
	}
	for i, part := range parts {
		if part == "" {
			return "", fmt.Errorf("detection is missing field %d", i)
		}
	}
	return strings.Join(parts, "|"), nil
}

// observedAt combines acq_date and acq_time in UTC. acq_time is accepted as
// "243", "0243", "02:43", or "02:43:00" because the feed is not consistent.
func observedAt(date, clock string) (time.Time, error) {
	date = strings.TrimSpace(date)
	clock = strings.TrimSpace(clock)
	if date == "" || clock == "" {
		return time.Time{}, fmt.Errorf("acq_date and acq_time are required")
	}
	var hour, minute, second int
	if strings.Contains(clock, ":") {
		fields := strings.Split(clock, ":")
		if len(fields) < 2 || len(fields) > 3 {
			return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
		}
		var err error
		if hour, err = strconv.Atoi(fields[0]); err != nil {
			return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
		}
		if minute, err = strconv.Atoi(fields[1]); err != nil {
			return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
		}
		if len(fields) == 3 {
			if second, err = strconv.Atoi(fields[2]); err != nil {
				return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
			}
		}
	} else {
		digits := strings.TrimLeft(clock, "+")
		for _, r := range digits {
			if r < '0' || r > '9' {
				return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
			}
		}
		if len(digits) < 3 || len(digits) > 6 {
			return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
		}
		for len(digits) < 4 {
			digits = "0" + digits
		}
		hour, _ = strconv.Atoi(digits[0:2])
		minute, _ = strconv.Atoi(digits[2:4])
		if len(digits) >= 6 {
			second, _ = strconv.Atoi(digits[4:6])
		}
	}
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, fmt.Errorf("invalid acq_time %q", clock)
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid acq_date %q", date)
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), hour, minute, second, 0, time.UTC), nil
}

type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = ""
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*f = flexString(value)
		return nil
	}
	*f = flexString(string(data))
	return nil
}

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q", value)
		}
		*f = flexFloat(parsed)
		return nil
	}
	parsed, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return fmt.Errorf("invalid number %q", string(data))
	}
	*f = flexFloat(parsed)
	return nil
}
