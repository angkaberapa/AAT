// Package core holds cross-cutting plumbing: correlation IDs, structured logging,
// the HTTP server with /health, outbound HTTP, and env parsing.
//
// This package is copied (not shared) into every BNPB service on purpose: services
// must not share libraries, and it contains no business logic. If you change it,
// change it in every service.
package core

import (
	"context"
	"crypto/rand"
	"fmt"
)

const CorrelationHeader = "X-Correlation-ID"

type correlationKey struct{}

// WithCorrelationID returns a context carrying id. Use one new ID per background
// cycle (e.g. a poll, a consumed message) so its logs and outbound calls line up.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the ID in ctx, or "-" when there is none.
func CorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationKey{}).(string); ok && id != "" {
		return id
	}
	return "-"
}

// NewCorrelationID returns a random UUIDv4 string.
func NewCorrelationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
