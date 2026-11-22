package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	unprocessedCollection              = "unprocessed_messages"
	unprocessedPayloadChunksCollection = "unprocessed_payload_chunks"
	maxInlineUnprocessedPayloadBytes = 8 << 20
	unprocessedPayloadChunkSize      = 1 << 20
)

const (
	UnprocessedRetrying = "retrying"
	UnprocessedParked   = "parked"
	UnprocessedResolved = "resolved"
)

type SourcePosition struct {
	Type  string `json:"type" bson:"type"`
	Value string `json:"value" bson:"value"`
}

type UnprocessedMessage struct {
	MessageID         string         `json:"message_id" bson:"message_id"`
	Source            string         `json:"source" bson:"source"`
	SourceRefID       string         `json:"source_ref_id,omitempty" bson:"source_ref_id,omitempty"`
	SourcePosition    SourcePosition `json:"source_position,omitempty" bson:"source_position,omitempty"`
	ReceivedAt        time.Time      `json:"received_at" bson:"received_at"`
	Reason            string         `json:"reason" bson:"reason"`
	Retryable         bool           `json:"retryable" bson:"retryable"`
	Attempts          int            `json:"attempts" bson:"attempts"`
	Status            string         `json:"status" bson:"status"`
	RawPayload        []byte         `json:"-" bson:"raw_payload,omitempty"`
	RawPayloadChunked bool           `json:"-" bson:"raw_payload_chunked,omitempty"`
	RawPayloadSize    int            `json:"-" bson:"raw_payload_size,omitempty"`
	RawPayloadSHA256  string         `json:"-" bson:"raw_payload_sha256,omitempty"`
	RawPayloadChunks  int            `json:"-" bson:"raw_payload_chunks,omitempty"`
	ContentType       string         `json:"content_type,omitempty" bson:"content_type,omitempty"`
	NextAttemptAt     *time.Time     `json:"next_attempt_at,omitempty" bson:"next_attempt_at,omitempty"`
	LeaseOwner        string         `json:"-" bson:"lease_owner,omitempty"`
	LeaseUntil        *time.Time     `json:"-" bson:"lease_until,omitempty"`
	ResolvedAt        *time.Time     `json:"-" bson:"resolved_at,omitempty"`
}

type unprocessedPayloadChunk struct {
	ID        string `bson:"_id"`
	MessageID string `bson:"message_id"`
	Index     int    `bson:"index"`
	Data      []byte `bson:"data"`
}

type UnprocessedFilter struct {
	Source string
	Limit  int
	Cursor *UnprocessedCursor
}

type UnprocessedCursor struct {
	ReceivedAt time.Time
	MessageID  string
}

type UnprocessedPage struct {
	Items []UnprocessedMessage
	Next  *UnprocessedCursor
}

func unprocessedIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "message_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "received_at", Value: 1}, {Key: "message_id", Value: 1}}},
		{Keys: bson.D{{Key: "source", Value: 1}, {Key: "received_at", Value: 1}, {Key: "message_id", Value: 1}}},
		{Keys: bson.D{{Key: "source", Value: 1}, {Key: "source_ref_id", Value: 1}, {Key: "received_at", Value: 1}}},
	}
}

func unprocessedPayloadChunkIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "message_id", Value: 1}, {Key: "index", Value: 1}}, Options: options.Index().SetUnique(true)},
	}
}

func (s *Store) StoreUnprocessed(ctx context.Context, message UnprocessedMessage) (bool, error) {
	if message.MessageID == "" || message.Source == "" || message.Reason == "" || message.ReceivedAt.IsZero() {
		return false, errors.New("unprocessed message_id, source, received_at, and reason are required")
	}
	if (message.Status != UnprocessedRetrying && message.Status != UnprocessedParked) || message.Retryable != (message.Status == UnprocessedRetrying) {
		return false, errors.New("unprocessed status and retryable are inconsistent")
	}
	message.ReceivedAt = message.ReceivedAt.UTC()
	setOnInsert := bson.D{
		{Key: "message_id", Value: message.MessageID}, {Key: "source", Value: message.Source},
		{Key: "source_ref_id", Value: message.SourceRefID}, {Key: "source_position", Value: message.SourcePosition},
		{Key: "received_at", Value: message.ReceivedAt}, {Key: "content_type", Value: message.ContentType},
	}
	if len(message.RawPayload) <= maxInlineUnprocessedPayloadBytes {
		setOnInsert = append(setOnInsert, bson.E{Key: "raw_payload", Value: message.RawPayload})
	} else {
		chunks := splitUnprocessedPayload(message.MessageID, message.RawPayload)
		if err := s.storeUnprocessedPayloadChunks(ctx, chunks); err != nil {
			return false, fmt.Errorf("store unprocessed %s payload chunks: %w", message.MessageID, err)
		}
		digest := sha256.Sum256(message.RawPayload)
		setOnInsert = append(setOnInsert,
			bson.E{Key: "raw_payload_chunked", Value: true},
			bson.E{Key: "raw_payload_size", Value: len(message.RawPayload)},
			bson.E{Key: "raw_payload_sha256", Value: hex.EncodeToString(digest[:])},
			bson.E{Key: "raw_payload_chunks", Value: len(chunks)},
		)
	}
	update := bson.D{
		{Key: "$setOnInsert", Value: setOnInsert},
		{Key: "$set", Value: bson.D{
			{Key: "reason", Value: message.Reason}, {Key: "retryable", Value: message.Retryable},
			{Key: "status", Value: message.Status}, {Key: "next_attempt_at", Value: message.NextAttemptAt},
		}},
		{Key: "$inc", Value: bson.D{{Key: "attempts", Value: 1}}},
	}
	result, err := s.unprocessed.UpdateOne(ctx, bson.D{{Key: "message_id", Value: message.MessageID}}, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, fmt.Errorf("store unprocessed %s: %w", message.MessageID, err)
	}
	return result.UpsertedCount == 1, nil
}

func (s *Store) AuditUnprocessed(ctx context.Context, filter UnprocessedFilter) (UnprocessedPage, error) {
	if filter.Limit <= 0 {
		return UnprocessedPage{}, errors.New("unprocessed page requires a positive limit")
	}
	query := bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{UnprocessedRetrying, UnprocessedParked}}}}}
	if filter.Source != "" {
		query = append(query, bson.E{Key: "source", Value: filter.Source})
	}
	if filter.Cursor != nil {
		query = append(query, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "received_at", Value: bson.D{{Key: "$gt", Value: filter.Cursor.ReceivedAt}}}},
			bson.D{{Key: "received_at", Value: filter.Cursor.ReceivedAt}, {Key: "message_id", Value: bson.D{{Key: "$gt", Value: filter.Cursor.MessageID}}}},
		}})
	}
	cur, err := s.unprocessed.Find(ctx, query, options.Find().
		SetProjection(bson.D{{Key: "raw_payload", Value: 0}, {Key: "lease_owner", Value: 0}, {Key: "lease_until", Value: 0}}).
		SetSort(bson.D{{Key: "received_at", Value: 1}, {Key: "message_id", Value: 1}}).
		SetLimit(int64(filter.Limit+1)))
	if err != nil {
		return UnprocessedPage{}, fmt.Errorf("find unprocessed: %w", err)
	}
	var items []UnprocessedMessage
	if err := cur.All(ctx, &items); err != nil {
		return UnprocessedPage{}, fmt.Errorf("decode unprocessed: %w", err)
	}
	if items == nil {
		items = []UnprocessedMessage{}
	}
	if len(items) <= filter.Limit {
		return UnprocessedPage{Items: items}, nil
	}
	items = items[:filter.Limit]
	last := items[len(items)-1]
	return UnprocessedPage{Items: items, Next: &UnprocessedCursor{ReceivedAt: last.ReceivedAt, MessageID: last.MessageID}}, nil
}

func (s *Store) GetUnprocessed(ctx context.Context, messageID string) (*UnprocessedMessage, error) {
	var message UnprocessedMessage
	err := s.unprocessed.FindOne(ctx, bson.D{{Key: "message_id", Value: messageID}}).Decode(&message)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get unprocessed: %w", err)
	}
	if err := s.hydrateUnprocessedPayload(ctx, &message); err != nil {
		return nil, fmt.Errorf("get unprocessed payload: %w", err)
	}
	return &message, nil
}

func (s *Store) ClaimUnprocessed(ctx context.Context, source, owner string, lease time.Duration, statuses ...string) (*UnprocessedMessage, error) {
	now := time.Now().UTC()
	query := bson.D{
		{Key: "source", Value: source},
		{Key: "status", Value: bson.D{{Key: "$in", Value: statuses}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "lease_until", Value: nil}},
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}},
		}},
	}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "lease_owner", Value: owner}, {Key: "lease_until", Value: now.Add(lease)}}}}
	var message UnprocessedMessage
	err := s.unprocessed.FindOneAndUpdate(ctx, query, update, options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "source_ref_id", Value: 1}, {Key: "received_at", Value: 1}, {Key: "message_id", Value: 1}}).
		SetReturnDocument(options.After)).Decode(&message)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim unprocessed: %w", err)
	}
	if err := s.hydrateUnprocessedPayload(ctx, &message); err != nil {
		return nil, fmt.Errorf("claim unprocessed payload: %w", err)
	}
	return &message, nil
}

func splitUnprocessedPayload(messageID string, payload []byte) []unprocessedPayloadChunk {
	chunks := make([]unprocessedPayloadChunk, 0, (len(payload)+unprocessedPayloadChunkSize-1)/unprocessedPayloadChunkSize)
	for start, index := 0, 0; start < len(payload); start, index = start+unprocessedPayloadChunkSize, index+1 {
		end := min(start+unprocessedPayloadChunkSize, len(payload))
		chunks = append(chunks, unprocessedPayloadChunk{
			ID: fmt.Sprintf("%s:%06d", messageID, index), MessageID: messageID, Index: index,
			Data: append([]byte(nil), payload[start:end]...),
		})
	}
	return chunks
}

func (s *Store) storeUnprocessedPayloadChunks(ctx context.Context, chunks []unprocessedPayloadChunk) error {
	for _, chunk := range chunks {
		_, err := s.unprocessedPayloadChunks.UpdateOne(ctx,
			bson.D{{Key: "_id", Value: chunk.ID}},
			bson.D{{Key: "$setOnInsert", Value: chunk}},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) hydrateUnprocessedPayload(ctx context.Context, message *UnprocessedMessage) error {
	if !message.RawPayloadChunked {
		return nil
	}
	cur, err := s.unprocessedPayloadChunks.Find(ctx,
		bson.D{{Key: "message_id", Value: message.MessageID}},
		options.Find().SetSort(bson.D{{Key: "index", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	var chunks []unprocessedPayloadChunk
	if err := cur.All(ctx, &chunks); err != nil {
		return err
	}
	payload, err := assembleUnprocessedPayload(message, chunks)
	if err != nil {
		return err
	}
	message.RawPayload = payload
	return nil
}

func assembleUnprocessedPayload(message *UnprocessedMessage, chunks []unprocessedPayloadChunk) ([]byte, error) {
	if message.RawPayloadSize < 0 || message.RawPayloadChunks <= 0 || message.RawPayloadSHA256 == "" {
		return nil, errors.New("invalid chunked payload metadata")
	}
	if len(chunks) != message.RawPayloadChunks {
		return nil, fmt.Errorf("payload chunk count = %d, want %d", len(chunks), message.RawPayloadChunks)
	}
	payload := make([]byte, 0, message.RawPayloadSize)
	for index, chunk := range chunks {
		if chunk.MessageID != message.MessageID || chunk.Index != index {
			return nil, fmt.Errorf("payload chunk %d is missing or out of order", index)
		}
		payload = append(payload, chunk.Data...)
	}
	if len(payload) != message.RawPayloadSize {
		return nil, fmt.Errorf("payload size = %d, want %d", len(payload), message.RawPayloadSize)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != message.RawPayloadSHA256 {
		return nil, errors.New("payload SHA-256 mismatch")
	}
	return payload, nil
}

func (s *Store) ResolveUnprocessed(ctx context.Context, messageID, owner string) error {
	now := time.Now().UTC()
	result, err := s.unprocessed.UpdateOne(ctx,
		bson.D{{Key: "message_id", Value: messageID}, {Key: "lease_owner", Value: owner}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: UnprocessedResolved}, {Key: "resolved_at", Value: now}}}, {Key: "$unset", Value: bson.D{{Key: "lease_owner", Value: ""}, {Key: "lease_until", Value: ""}}}},
	)
	if err != nil {
		return fmt.Errorf("resolve unprocessed: %w", err)
	}
	if result.MatchedCount != 1 {
		return errors.New("unprocessed lease lost")
	}
	return nil
}

func (s *Store) ReleaseUnprocessed(ctx context.Context, messageID, owner, reason string) error {
	_, err := s.unprocessed.UpdateOne(ctx,
		bson.D{{Key: "message_id", Value: messageID}, {Key: "lease_owner", Value: owner}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "reason", Value: reason}}}, {Key: "$unset", Value: bson.D{{Key: "lease_owner", Value: ""}, {Key: "lease_until", Value: ""}}}},
	)
	return err
}
