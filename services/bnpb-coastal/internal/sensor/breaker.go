package sensor

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen means the breaker is refusing calls to the sensor.
var ErrCircuitOpen = errors.New("sensor circuit breaker is open")

type BreakerState int

const (
	BreakerClosed BreakerState = iota
	BreakerHalfOpen
	BreakerOpen
)

func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerHalfOpen:
		return "half_open"
	case BreakerOpen:
		return "open"
	default:
		return "unknown"
	}
}

type BreakerConfig struct {
	FailureThreshold int
	OpenTimeout      time.Duration
	HalfOpenMax      int
	Now              func() time.Time
}

// Breaker is a small circuit breaker tailored to the sensor client. It opens
// after repeated retryable failures, allows one probe after OpenTimeout, and
// closes again on a successful probe.
type Breaker struct {
	mu               sync.Mutex
	state            BreakerState
	failures         int
	openedAt         time.Time
	halfOpenInFlight int
	config           BreakerConfig
}

func NewBreaker(config BreakerConfig) *Breaker {
	if config.FailureThreshold < 1 {
		config.FailureThreshold = 5
	}
	if config.OpenTimeout <= 0 {
		config.OpenTimeout = 30 * time.Second
	}
	if config.HalfOpenMax < 1 {
		config.HalfOpenMax = 1
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Breaker{state: BreakerClosed, config: config}
}

func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if b.config.Now().Sub(b.openedAt) < b.config.OpenTimeout {
			return false
		}
		b.state = BreakerHalfOpen
		b.halfOpenInFlight = 0
		fallthrough
	case BreakerHalfOpen:
		if b.halfOpenInFlight >= b.config.HalfOpenMax {
			return false
		}
		b.halfOpenInFlight++
		return true
	default:
		return false
	}
}

func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = BreakerClosed
	b.failures = 0
	b.halfOpenInFlight = 0
}

func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerHalfOpen:
		b.state = BreakerOpen
		b.openedAt = b.config.Now()
		b.halfOpenInFlight = 0
	case BreakerClosed:
		b.failures++
		if b.failures >= b.config.FailureThreshold {
			b.state = BreakerOpen
			b.openedAt = b.config.Now()
		}
	}
}

func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

type Fetcher interface {
	FetchPage(ctx context.Context, cursor string, limit int) (Page, error)
}

// BreakerClient wraps a sensor fetcher with the circuit breaker. Terminal
// client errors (4xx other than 429) count as reachable, so they do not open
// the circuit; transport errors, 429, and 5xx do.
type BreakerClient struct {
	next    Fetcher
	breaker *Breaker
}

func NewBreakerClient(next Fetcher, breaker *Breaker) *BreakerClient {
	return &BreakerClient{next: next, breaker: breaker}
}

func (c *BreakerClient) FetchPage(ctx context.Context, cursor string, limit int) (Page, error) {
	if !c.breaker.Allow() {
		return Page{}, ErrCircuitOpen
	}
	page, err := c.next.FetchPage(ctx, cursor, limit)
	if err == nil {
		c.breaker.Success()
		return page, nil
	}
	if IsRetryable(err) {
		c.breaker.Failure()
	} else {
		c.breaker.Success()
	}
	return page, err
}

func (c *BreakerClient) State() BreakerState {
	return c.breaker.State()
}
