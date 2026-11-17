package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// IngestHazard stores one event from an internal producer. Unlike SaveBatch it
// does not use the poller's fencing token: the write may land on any Aggregator
// instance. The transaction keeps the event and its outbox marker atomic.
func (s *Store) IngestHazard(ctx context.Context, event domain.HazardEvent) (SaveResult, error) {
	session, err := s.client.StartSession()
	if err != nil {
		return "", fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(ctx)

	var result SaveResult
	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		result = ""
		saved, err := s.save(ctx, event)
		if err != nil {
			return nil, err
		}
		result = saved
		return nil, nil
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

// DeleteHazard removes one canonical event. Deleting a missing event is not an
// error because producers retry deletes after reconnects.
func (s *Store) DeleteHazard(ctx context.Context, hazardID string) (bool, error) {
	result, err := s.events.DeleteOne(ctx, bson.D{{Key: "hazard_id", Value: hazardID}})
	if err != nil {
		return false, fmt.Errorf("delete hazard %s: %w", hazardID, err)
	}
	return result.DeletedCount > 0, nil
}
