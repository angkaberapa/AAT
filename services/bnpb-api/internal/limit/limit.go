// Package limit protects bnpb-api from resource exhaustion (P2): a global cap on
// in-flight requests and a per-client token bucket. Both reject with HTTP 429 and
// Retry-After instead of queueing, so a burst degrades into fast, explicit
// rejections rather than piling up goroutines and timing out everyone.
package limit

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ClientKey extracts the identity used for per-client limiting (e.g. the JWT subject).
// ok=false means the request is not attributable; it is then only subject to the
// global concurrency cap.
type ClientKey func(r *http.Request) (key string, ok bool)

// Concurrency allows at most max requests in flight at once; the rest get 429
// immediately. max <= 0 disables the cap.
func Concurrency(max int) func(http.Handler) http.Handler {
	if max <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	slots := make(chan struct{}, max)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				next.ServeHTTP(w, r)
			default:
				reject(w, time.Second, "server is at its concurrent request limit")
			}
		})
	}
}

// PerClient applies a token bucket of rps requests/second with the given burst to
// every client separately, so one noisy client cannot starve the others.
// rps <= 0 disables it.
type PerClient struct {
	rps   rate.Limit
	burst int
	key   ClientKey

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func NewPerClient(rps float64, burst int, key ClientKey) *PerClient {
	if burst < 1 {
		burst = 1
	}
	return &PerClient{
		rps:      rate.Limit(rps),
		burst:    burst,
		key:      key,
		limiters: make(map[string]*rate.Limiter),
	}
}

func (p *PerClient) Middleware(next http.Handler) http.Handler {
	if p.rps <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := p.key(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		reservation := p.limiter(key).Reserve()
		if delay := reservation.Delay(); delay > 0 {
			// Give the token back: the request is rejected, not delayed.
			reservation.Cancel()
			reject(w, delay, "client request rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The number of distinct keys is bounded by the configured client identities
// (keys come from verified tokens), so the map does not need eviction.
func (p *PerClient) limiter(key string) *rate.Limiter {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.limiters[key]
	if !ok {
		l = rate.NewLimiter(p.rps, p.burst)
		p.limiters[key] = l
	}
	return l
}

func reject(w http.ResponseWriter, retryAfter time.Duration, description string) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             "too_many_requests",
		"error_description": description,
	})
}
