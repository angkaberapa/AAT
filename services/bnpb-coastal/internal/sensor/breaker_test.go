package sensor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeFetcher struct {
	page  Page
	err   error
	calls int
}

func (f *fakeFetcher) FetchPage(ctx context.Context, cursor string, limit int) (Page, error) {
	f.calls++
	if f.err != nil {
		return Page{}, f.err
	}
	return f.page, nil
}

func TestBreakerOpensAfterThresholdAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	breaker := NewBreaker(BreakerConfig{
		FailureThreshold: 2,
		OpenTimeout:      30 * time.Second,
		Now:              func() time.Time { return now },
	})

	if !breaker.Allow() {
		t.Fatal("closed breaker must allow")
	}
	breaker.Failure()
	if breaker.State() != BreakerClosed {
		t.Fatalf("state after one failure = %v", breaker.State())
	}
	breaker.Failure()
	if breaker.State() != BreakerOpen {
		t.Fatalf("state after threshold = %v", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("open breaker must reject")
	}

	now = now.Add(31 * time.Second)
	if !breaker.Allow() {
		t.Fatal("half-open breaker must allow one probe")
	}
	if breaker.State() != BreakerHalfOpen {
		t.Fatalf("state = %v", breaker.State())
	}
	breaker.Success()
	if breaker.State() != BreakerClosed {
		t.Fatalf("state after success = %v", breaker.State())
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	breaker := NewBreaker(BreakerConfig{
		FailureThreshold: 1,
		OpenTimeout:      time.Second,
		Now:              func() time.Time { return now },
	})
	breaker.Failure()
	now = now.Add(2 * time.Second)
	if !breaker.Allow() {
		t.Fatal("expected half-open probe")
	}
	breaker.Failure()
	if breaker.State() != BreakerOpen {
		t.Fatalf("state = %v", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("open breaker must reject")
	}
}

func TestBreakerClientIgnoresTerminalError(t *testing.T) {
	fetcher := &fakeFetcher{err: &APIError{StatusCode: 400, Code: "bad_request"}}
	breaker := NewBreaker(BreakerConfig{FailureThreshold: 1})
	client := NewBreakerClient(fetcher, breaker)

	if _, err := client.FetchPage(context.Background(), "", 10); err == nil {
		t.Fatal("expected terminal error")
	}
	if breaker.State() != BreakerClosed {
		t.Fatalf("terminal 4xx must not open breaker, state = %v", breaker.State())
	}
	if !breaker.Allow() {
		t.Fatal("breaker should still allow")
	}
}

func TestBreakerClientOpensOnRetryableError(t *testing.T) {
	fetcher := &fakeFetcher{err: &APIError{StatusCode: 503, Code: "unavailable"}}
	breaker := NewBreaker(BreakerConfig{FailureThreshold: 2})
	client := NewBreakerClient(fetcher, breaker)

	for i := 0; i < 2; i++ {
		if _, err := client.FetchPage(context.Background(), "", 10); err == nil {
			t.Fatal("expected retryable error")
		}
	}
	if breaker.State() != BreakerOpen {
		t.Fatalf("state = %v", breaker.State())
	}
	calls := fetcher.calls
	if _, err := client.FetchPage(context.Background(), "", 10); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("error = %v, want ErrCircuitOpen", err)
	}
	if fetcher.calls != calls {
		t.Fatal("open breaker must not call the sensor")
	}
}

func TestBreakerClientSuccessResetsFailures(t *testing.T) {
	fetcher := &fakeFetcher{page: Page{NextCursor: "cursor-1"}}
	breaker := NewBreaker(BreakerConfig{FailureThreshold: 2})
	client := NewBreakerClient(fetcher, breaker)

	fetcher.err = &APIError{StatusCode: 503}
	_, _ = client.FetchPage(context.Background(), "", 10)
	fetcher.err = nil
	_, _ = client.FetchPage(context.Background(), "", 10)
	if breaker.State() != BreakerClosed {
		t.Fatalf("state = %v", breaker.State())
	}
	fetcher.err = &APIError{StatusCode: 503}
	_, _ = client.FetchPage(context.Background(), "", 10)
	if breaker.State() != BreakerClosed {
		t.Fatalf("one failure after reset should keep closed, state = %v", breaker.State())
	}
}
