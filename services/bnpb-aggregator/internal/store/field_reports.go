package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const fieldReportsCollection = "field_reports"

const (
	FieldReportPending    = "pending"
	FieldReportProcessing = "processing"
	FieldReportCompleted  = "completed"
)

type FieldReport struct {
	ReportID       string         `json:"report_id" bson:"report_id"`
	IdempotencyKey string         `json:"-" bson:"idempotency_key"`
	ClientID       string         `json:"-" bson:"client_id"`
	OccurredAt     time.Time      `json:"occurred_at" bson:"occurred_at"`
	ReceivedAt     time.Time      `json:"received_at" bson:"received_at"`
	AreaName       string         `json:"area_name" bson:"area_name"`
	Latitude       float64        `json:"latitude" bson:"latitude"`
	Longitude      float64        `json:"longitude" bson:"longitude"`
	Severity       string         `json:"severity" bson:"severity"`
	Description    string         `json:"description" bson:"description"`
	Attributes     map[string]any `json:"attributes,omitempty" bson:"attributes,omitempty"`
	Status         string         `json:"status" bson:"status"`
	Attempts       int            `json:"attempts" bson:"attempts"`
	ProcessCount   int            `json:"process_count" bson:"process_count"`
	PayloadHash    string         `json:"-" bson:"payload_hash"`
	LeaseOwner     string         `json:"-" bson:"lease_owner,omitempty"`
	LeaseUntil     *time.Time     `json:"-" bson:"lease_until,omitempty"`
	ProcessedAt    *time.Time     `json:"processed_at,omitempty" bson:"processed_at,omitempty"`
}

func fieldReportIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "report_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "client_id", Value: 1}, {Key: "idempotency_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "received_at", Value: 1}}},
	}
}

func FieldReportPayloadHash(report FieldReport) (string, error) {
	report.ReportID, report.IdempotencyKey, report.ClientID = "", "", ""
	report.ReceivedAt, report.Status, report.Attempts, report.ProcessCount = time.Time{}, "", 0, 0
	report.PayloadHash, report.LeaseOwner, report.LeaseUntil, report.ProcessedAt = "", "", nil, nil
	raw, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) AcceptFieldReport(ctx context.Context, report FieldReport) (FieldReport, bool, error) {
	if report.ReportID == "" || report.IdempotencyKey == "" || report.ClientID == "" || report.OccurredAt.IsZero() || report.AreaName == "" || report.Description == "" {
		return FieldReport{}, false, errors.New("field report identity, occurrence, area, and description are required")
	}
	hash, err := FieldReportPayloadHash(report)
	if err != nil {
		return FieldReport{}, false, fmt.Errorf("hash field report: %w", err)
	}
	report.OccurredAt = report.OccurredAt.UTC()
	report.ReceivedAt = time.Now().UTC()
	report.Status = FieldReportPending
	report.PayloadHash = hash
	report.Attempts = 0
	report.ProcessCount = 0

	result, err := s.fieldReports.UpdateOne(ctx,
		bson.D{{Key: "client_id", Value: report.ClientID}, {Key: "idempotency_key", Value: report.IdempotencyKey}},
		bson.D{{Key: "$setOnInsert", Value: report}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return FieldReport{}, false, fmt.Errorf("accept field report: %w", err)
	}
	if result.UpsertedCount == 1 {
		return report, true, nil
	}
	var existing FieldReport
	if err := s.fieldReports.FindOne(ctx, bson.D{{Key: "client_id", Value: report.ClientID}, {Key: "idempotency_key", Value: report.IdempotencyKey}}).Decode(&existing); err != nil {
		return FieldReport{}, false, fmt.Errorf("read existing field report: %w", err)
	}
	if existing.PayloadHash != hash {
		return FieldReport{}, false, ErrIdempotencyConflict
	}
	return existing, false, nil
}

var ErrIdempotencyConflict = errors.New("idempotency key was already used for a different field report")

func (s *Store) ClaimFieldReport(ctx context.Context, owner string, lease time.Duration) (*FieldReport, error) {
	now := time.Now().UTC()
	filter := bson.D{
		{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{FieldReportPending, FieldReportProcessing}}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "lease_until", Value: nil}},
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}},
		}},
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "status", Value: FieldReportProcessing}, {Key: "lease_owner", Value: owner}, {Key: "lease_until", Value: now.Add(lease)}}},
		{Key: "$inc", Value: bson.D{{Key: "attempts", Value: 1}}},
	}
	var report FieldReport
	err := s.fieldReports.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "received_at", Value: 1}, {Key: "report_id", Value: 1}}).
		SetReturnDocument(options.After)).Decode(&report)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim field report: %w", err)
	}
	return &report, nil
}

func (s *Store) CompleteFieldReport(ctx context.Context, reportID, owner string) error {
	now := time.Now().UTC()
	result, err := s.fieldReports.UpdateOne(ctx,
		bson.D{{Key: "report_id", Value: reportID}, {Key: "status", Value: FieldReportProcessing}, {Key: "lease_owner", Value: owner}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: FieldReportCompleted}, {Key: "processed_at", Value: now}}}, {Key: "$inc", Value: bson.D{{Key: "process_count", Value: 1}}}, {Key: "$unset", Value: bson.D{{Key: "lease_owner", Value: ""}, {Key: "lease_until", Value: ""}}}},
	)
	if err != nil {
		return fmt.Errorf("complete field report: %w", err)
	}
	if result.MatchedCount != 1 {
		return errors.New("field report lease lost")
	}
	return nil
}

func (s *Store) GetFieldReport(ctx context.Context, reportID string) (*FieldReport, error) {
	var report FieldReport
	err := s.fieldReports.FindOne(ctx, bson.D{{Key: "report_id", Value: reportID}}).Decode(&report)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get field report: %w", err)
	}
	return &report, nil
}
