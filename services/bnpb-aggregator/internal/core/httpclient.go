package core

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Connection reuse matters under load: Go's default transport keeps only 2 idle
// connections per host, so with dozens of concurrent requests most calls open a new
// TCP connection and close it again. The TIME_WAIT sockets pile up until the
// ephemeral ports run out ("cannot assign requested address").
const (
	maxIdleConns        = 256
	maxIdleConnsPerHost = 128
	maxDrainBytes       = 64 << 10
)

// HTTPClient forwards the correlation ID and logs the latency of every outbound
// call. Use it instead of http.DefaultClient.
type HTTPClient struct {
	client *http.Client
	logger *slog.Logger
}

func NewHTTPClient(logger *slog.Logger, timeout time.Duration) *HTTPClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = maxIdleConns
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return &HTTPClient{
		client: &http.Client{Timeout: timeout, Transport: transport},
		logger: logger.With("component", "http.outbound"),
	}
}

// drainOnClose reads what is left of a response body before closing it. A body
// that is closed before EOF (e.g. json.Decoder stops before the trailing newline)
// makes the transport discard the connection instead of reusing it.
type drainOnClose struct{ io.ReadCloser }

func (b drainOnClose) Close() error {
	_, _ = io.CopyN(io.Discard, b.ReadCloser, maxDrainBytes)
	return b.ReadCloser.Close()
}

// Do sends req. `target` is a short label for logs, e.g. "bmkg" or "aggregator".
// The correlation ID is taken from req.Context().
func (c *HTTPClient) Do(req *http.Request, target string) (*http.Response, error) {
	ctx := req.Context()
	req.Header.Set(CorrelationHeader, CorrelationID(ctx))

	start := time.Now()
	resp, err := c.client.Do(req)
	attrs := []any{
		"target", target,
		"method", req.Method,
		"url", req.URL.String(),
		"latency_ms", millis(time.Since(start)),
	}
	if err != nil {
		c.logger.WarnContext(ctx, "outbound call failed", append(attrs, "error", err.Error())...)
		return nil, err
	}
	c.logger.InfoContext(ctx, "outbound call", append(attrs, "status", resp.StatusCode)...)
	resp.Body = drainOnClose{resp.Body}
	return resp, nil
}
