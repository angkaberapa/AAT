package bmkg

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
	apiKey     string
	httpClient HTTPClient
	mapper     *mapping.Mapper
}

var _ ingest.SourceClient = (*Client)(nil)

func NewClient(baseURL, apiKey string, httpClient HTTPClient, mapper *mapping.Mapper) (*Client, error) {
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid BMKG base URL %q", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("BMKG API key is required")
	}
	if httpClient == nil {
		return nil, errors.New("BMKG HTTP client is required")
	}
	if mapper == nil {
		return nil, errors.New("BMKG mapper is required")
	}
	return &Client{
		baseURL:    parsedBaseURL,
		apiKey:     apiKey,
		httpClient: httpClient,
		mapper:     mapper,
	}, nil
}

func (client *Client) Source() domain.Source {
	return domain.SourceBMKG
}

func (client *Client) Fetch(
	ctx context.Context,
	since *time.Time,
) ([]domain.HazardEvent, error) {
	events, err := fetch[mapping.SeismicEvent](client, ctx, "/seismic-events", since)
	if err != nil {
		return nil, err
	}
	warnings, err := fetch[mapping.TsunamiWarning](client, ctx, "/tsunami-warnings", since)
	if err != nil {
		return nil, err
	}

	warningsByEvent := make(map[string]mapping.TsunamiWarning, len(warnings))
	for _, warning := range warnings {
		warningsByEvent[warning.RelatedEventID] = warning
	}

	mapped := make([]domain.HazardEvent, 0, len(events))
	for index, event := range events {
		var warning *mapping.TsunamiWarning
		if related, ok := warningsByEvent[event.EventID]; ok {
			warningCopy := related
			warning = &warningCopy
		}
		hazard, err := client.mapper.MapSeismicEvent(event, warning)
		if err != nil {
			return nil, invalidResponse(fmt.Errorf("map BMKG event at index %d: %w", index, err))
		}
		mapped = append(mapped, hazard)
	}
	return mapped, nil
}

func fetch[T any](
	client *Client,
	ctx context.Context,
	path string,
	since *time.Time,
) ([]T, error) {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	if since != nil {
		query := endpoint.Query()
		query.Set("since", since.UTC().Format(time.RFC3339Nano))
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create BMKG request: %w", err)
	}
	request.Header.Set("X-BMKG-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request, "bmkg")
	if err != nil {
		return nil, &ingest.SourceError{
			Source: domain.SourceBMKG,
			Kind:   ingest.ErrorUnavailable,
			Err:    err,
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}

	var envelope mapping.Envelope[T]
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, invalidResponse(fmt.Errorf("decode BMKG response: %w", err))
	}
	return envelope.Data, nil
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
		Source:     domain.SourceBMKG,
		Kind:       kind,
		StatusCode: response.StatusCode,
		Err:        errors.New(detail),
	}
}

func invalidResponse(err error) error {
	return &ingest.SourceError{
		Source: domain.SourceBMKG,
		Kind:   ingest.ErrorInvalidResponse,
		Err:    err,
	}
}
