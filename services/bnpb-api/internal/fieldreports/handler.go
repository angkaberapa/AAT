package fieldreports

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

const maxRequestBytes = 1 << 20

type HTTPClient interface {
	Do(request *http.Request, target string) (*http.Response, error)
}

type Handler struct {
	url    string
	token  string
	client HTTPClient
}

func New(baseURL, token string, client HTTPClient) (*Handler, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.TrimSpace(token) == "" || client == nil {
		return nil, errors.New("field report aggregator URL, token, and HTTP client are required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/internal/field-reports"
	parsed.RawQuery, parsed.Fragment = "", ""
	return &Handler{url: parsed.String(), token: token, client: client}, nil
}

func (h *Handler) Register(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("POST /field-reports", authenticate(http.HandlerFunc(h.serve)))
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	principal, ok := security.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authenticated principal is missing")
		return
	}
	if !principal.HasScope(security.ScopeFieldReportsWrite) {
		writeError(w, http.StatusForbidden, "insufficient_scope", "field-reports:write scope is required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is required and must not exceed 200 characters")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "field report exceeds the 1 MiB limit")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_failed", "cannot create internal request")
		return
	}
	request.Header.Set("Authorization", "Bearer "+h.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-Client-ID", principal.ClientID)
	response, err := h.client.Do(request, "aggregator")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "aggregator_unavailable", "field report could not be durably accepted")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "invalid_upstream_response", "cannot read Aggregator response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
