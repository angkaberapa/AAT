package pvmbg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
)

const maxResponseBytes = 4 << 20

type HTTPClient interface {
	Do(req *http.Request, target string) (*http.Response, error)
}

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient HTTPClient
	mapper     *mapping.Mapper
}

var _ ingest.SourceClient = (*Client)(nil)

func NewClient(baseURL, token string, httpClient HTTPClient, mapper *mapping.Mapper) (*Client, error) {
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid PVMBG base URL %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("PVMBG token is required")
	}
	if httpClient == nil {
		return nil, errors.New("PVMBG HTTP client is required")
	}
	if mapper == nil {
		return nil, errors.New("PVMBG mapper is required")
	}
	return &Client{
		baseURL:    parsedBaseURL,
		token:      token,
		httpClient: httpClient,
		mapper:     mapper,
	}, nil
}

func (client *Client) Source() domain.Source {
	return domain.SourcePVMBG
}

func (client *Client) Fetch(
	ctx context.Context,
	since *time.Time,
) ([]domain.HazardEvent, error) {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/volcanic-reports"
	if since != nil {
		query := endpoint.Query()
		query.Set("since", since.UTC().Format(time.RFC3339Nano))
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create PVMBG request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request, "pvmbg")
	if err != nil {
		return nil, &ingest.SourceError{
			Source: domain.SourcePVMBG,
			Kind:   ingest.ErrorUnavailable,
			Err:    err,
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}

	var envelope mapping.Envelope[mapping.VolcanicReport]
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, invalidResponse(fmt.Errorf("decode PVMBG response: %w", err))
	}

	events := make([]domain.HazardEvent, 0, len(envelope.Data))
	for index, report := range envelope.Data {
		event, err := client.mapper.MapVolcanicReport(report)
		if err != nil {
			return nil, invalidResponse(fmt.Errorf("map PVMBG report at index %d: %w", index, err))
		}
		events = append(events, event)
	}
	return events, nil
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = response.Status
	}
	kind := ingest.ErrorInvalidResponse
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		kind = ingest.ErrorUnauthorized
	} else if response.StatusCode >= 500 {
		kind = ingest.ErrorUnavailable
	}
	return &ingest.SourceError{
		Source:     domain.SourcePVMBG,
		Kind:       kind,
		StatusCode: response.StatusCode,
		Err:        errors.New(detail),
	}
}

func invalidResponse(err error) error {
	return &ingest.SourceError{
		Source: domain.SourcePVMBG,
		Kind:   ingest.ErrorInvalidResponse,
		Err:    err,
	}
}
