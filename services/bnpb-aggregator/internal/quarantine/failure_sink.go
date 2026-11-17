package quarantine

import (
	"context"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type FailureStore interface {
	StoreUnprocessed(context.Context, store.UnprocessedMessage) (bool, error)
}

type FailureSink struct{ store FailureStore }

func NewFailureSink(destination FailureStore) FailureSink { return FailureSink{store: destination} }

func (sink FailureSink) StoreRawFailure(ctx context.Context, failure ingest.RawFailure) error {
	_, err := sink.store.StoreUnprocessed(ctx, store.UnprocessedMessage{
		MessageID: failure.MessageID, Source: string(failure.Source), SourceRefID: failure.SourceRefID,
		ReceivedAt: failure.ReceivedAt, Reason: failure.Reason, Retryable: failure.Retryable,
		Status: failure.Status, RawPayload: failure.RawPayload, ContentType: failure.ContentType,
	})
	return err
}
