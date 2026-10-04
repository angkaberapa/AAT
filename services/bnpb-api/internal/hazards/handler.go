package hazards

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

const maxUpstreamResponseBytes = 8 << 20

var summaryFields = map[string]struct{}{
	"hazard_id":   {},
	"source":      {},
	"hazard_type": {},
	"severity":    {},
	"area_name":   {},
	"occurred_at": {},
	"ingested_at": {},
}

var rawFields = map[string]struct{}{
	"source_ref_id": {},
	"latitude":      {},
	"longitude":     {},
	"attributes":    {},
}

type HTTPClient interface {
	Do(request *http.Request, target string) (*http.Response, error)
}

type Handler struct {
	baseURL *url.URL
	client  HTTPClient
}

func NewHandler(baseURL string, client HTTPClient) (*Handler, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid aggregator base URL %q", baseURL)
	}
	if client == nil {
		return nil, errors.New("aggregator HTTP client is required")
	}
	return &Handler{baseURL: parsed, client: client}, nil
}

func (handler *Handler) Register(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("GET /hazards", authenticate(http.HandlerFunc(handler.serve)))
	mux.Handle("GET /hazards/{id}", authenticate(http.HandlerFunc(handler.serve)))
}

func (handler *Handler) serve(w http.ResponseWriter, request *http.Request) {
	principal, ok := security.PrincipalFromContext(request.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authenticated principal is missing")
		return
	}
	selection, summaryOnly, err := authorizeFields(principal, request.URL.Query().Get("fields"))
	if err != nil {
		var denied *fieldDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, "insufficient_scope", denied.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_fields", err.Error())
		return
	}

	payload, err := handler.fetch(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if selection != nil {
		payload, err = filterPayload(payload, selection)
		if err != nil {
			writeError(w, http.StatusBadGateway, "invalid_upstream_response", err.Error())
			return
		}
	}
	if summaryOnly {
		w.Header().Set("X-Data-Scope", "summary")
	} else {
		w.Header().Set("X-Data-Scope", "full")
	}
	writeJSON(w, http.StatusOK, payload)
}

func (handler *Handler) fetch(incoming *http.Request) (any, error) {
	endpoint := *handler.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + incoming.URL.Path
	query := incoming.URL.Query()
	query.Del("fields")
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""

	request, err := http.NewRequestWithContext(incoming.Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create aggregator request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := handler.client.Do(request, "aggregator")
	if err != nil {
		return nil, fmt.Errorf("request aggregator: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("aggregator returned status %d", response.StatusCode)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxUpstreamResponseBytes))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode aggregator response: %w", err)
	}
	return payload, nil
}

type fieldDeniedError struct{ field string }

func (err *fieldDeniedError) Error() string {
	return fmt.Sprintf("scope does not permit access to raw field %q", err.field)
}

func authorizeFields(principal security.Principal, requested string) (map[string]struct{}, bool, error) {
	fullAccess := principal.HasScope(security.ScopeHazardsRead)
	summaryAccess := fullAccess || principal.HasScope(security.ScopeHazardsSummary)
	if !summaryAccess {
		return nil, false, &fieldDeniedError{field: "hazards"}
	}
	if strings.TrimSpace(requested) == "" {
		if fullAccess {
			return nil, false, nil
		}
		return cloneSet(summaryFields), true, nil
	}

	selection := make(map[string]struct{})
	for _, field := range strings.Split(requested, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, !fullAccess, errors.New("fields contains an empty field name")
		}
		if _, known := summaryFields[field]; !known {
			if _, raw := rawFields[field]; !raw {
				return nil, !fullAccess, fmt.Errorf("unknown field %q", field)
			}
			if !fullAccess {
				return nil, true, &fieldDeniedError{field: field}
			}
		}
		selection[field] = struct{}{}
	}
	return selection, !fullAccess, nil
}

func filterPayload(payload any, fields map[string]struct{}) (any, error) {
	switch value := payload.(type) {
	case []any:
		filtered := make([]any, 0, len(value))
		for _, item := range value {
			event, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("hazard list contains a non-object item")
			}
			filtered = append(filtered, filterEvent(event, fields))
		}
		return filtered, nil
	case map[string]any:
		if data, exists := value["data"]; exists {
			filteredData, err := filterPayload(data, fields)
			if err != nil {
				return nil, err
			}
			copy := make(map[string]any, len(value))
			for key, item := range value {
				copy[key] = item
			}
			copy["data"] = filteredData
			return copy, nil
		}
		if _, isHazard := value["hazard_id"]; !isHazard {
			return nil, errors.New("upstream response does not contain canonical hazard data")
		}
		return filterEvent(value, fields), nil
	default:
		return nil, errors.New("upstream response has an unsupported shape")
	}
}

func filterEvent(event map[string]any, fields map[string]struct{}) map[string]any {
	filtered := make(map[string]any, len(fields))
	for field := range fields {
		if value, exists := event[field]; exists {
			filtered[field] = value
		}
	}
	return filtered
}

func cloneSet(source map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(source))
	for value := range source {
		result[value] = struct{}{}
	}
	return result
}

func KnownFields() []string {
	fields := make([]string, 0, len(summaryFields)+len(rawFields))
	for field := range summaryFields {
		fields = append(fields, field)
	}
	for field := range rawFields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func writeError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
