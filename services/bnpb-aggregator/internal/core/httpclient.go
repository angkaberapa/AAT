package core

import (
	"log/slog"
	"net/http"
	"time"
)

// HTTPClient forwards the correlation ID and logs the latency of every outbound
// call. Use it instead of http.DefaultClient.
type HTTPClient struct {
	client *http.Client
	logger *slog.Logger
}

func NewHTTPClient(logger *slog.Logger, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{Timeout: timeout},
		logger: logger.With("component", "http.outbound"),
	}
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
	return resp, nil
}
