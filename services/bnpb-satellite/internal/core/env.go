package core

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Env reads environment variables and collects every problem, so a service can
// report all missing/invalid settings at once and exit.
type Env struct{ errs []error }

func (e *Env) String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (e *Env) Required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		e.errs = append(e.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (e *Env) Int(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

// Seconds parses a (possibly fractional) number of seconds, e.g. POLL_INTERVAL_SECONDS=2.5.
func (e *Env) Seconds(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		e.errs = append(e.errs, fmt.Errorf("%s must be a positive number of seconds, got %q", key, v))
		return def
	}
	return time.Duration(f * float64(time.Second))
}

// Millis parses an integer number of milliseconds, e.g. UPSTREAM_TIMEOUT_MS=5000.
func (e *Env) Millis(key string, def time.Duration) time.Duration {
	n := e.Int(key, int(def/time.Millisecond))
	if n <= 0 {
		e.errs = append(e.errs, fmt.Errorf("%s must be positive", key))
		return def
	}
	return time.Duration(n) * time.Millisecond
}

// Check adds a custom validation error when ok is false.
func (e *Env) Check(ok bool, msg string) {
	if !ok {
		e.errs = append(e.errs, errors.New(msg))
	}
}

func (e *Env) Err() error { return errors.Join(e.errs...) }
