package p1tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
)

type Result struct {
	Received  int `json:"received"`
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

type Client struct {
	url   string
	token string
	http  *http.Client
}

func NewClient(url, token string, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("catalogue endpoint URL is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("audit token is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	return &Client{url: url, token: token, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Import(ctx context.Context, events []mapping.SeismicEvent) (Result, error) {
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return Result{}, fmt.Errorf("encode catalogue batch: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("create catalogue request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("send catalogue batch: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read catalogue response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("catalogue endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return Result{}, fmt.Errorf("decode catalogue response: %w", err)
	}
	return result, nil
}
