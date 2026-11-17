package store

import (
	"context"
	"fmt"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

func (s *Store) ImportBatch(ctx context.Context, events []domain.HazardEvent) (map[SaveResult]int, error) {
	if len(events) == 0 {
		return map[SaveResult]int{}, nil
	}
	session, err := s.client.StartSession()
	if err != nil {
		return nil, fmt.Errorf("start catalogue import session: %w", err)
	}
	defer session.EndSession(ctx)

	var counts map[SaveResult]int
	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		counts = map[SaveResult]int{}
		for _, event := range events {
			result, err := s.save(txCtx, event)
			if err != nil {
				return nil, err
			}
			counts[result]++
		}
		return nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("import historical catalogue batch: %w", err)
	}
	return counts, nil
}
