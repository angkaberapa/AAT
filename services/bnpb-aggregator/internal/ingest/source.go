package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

type SourceClient interface {
	Source() domain.Source
	Fetch(ctx context.Context, since *time.Time) ([]domain.HazardEvent, error)
}

type FetchCommitter interface {
	CommitFetch()
}

type ErrorKind string

const (
	ErrorUnauthorized    ErrorKind = "unauthorized"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorInvalidResponse ErrorKind = "invalid_response"
)

type SourceError struct {
	Source     domain.Source
	Kind       ErrorKind
	StatusCode int
	Err        error
}

func (err *SourceError) Error() string {
	if err.StatusCode != 0 {
		return fmt.Sprintf("%s source %s (HTTP %d): %v", err.Source, err.Kind, err.StatusCode, err.Err)
	}
	return fmt.Sprintf("%s source %s: %v", err.Source, err.Kind, err.Err)
}

func (err *SourceError) Unwrap() error {
	return err.Err
}
