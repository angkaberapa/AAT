package token

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueUsesIdentitySpecificScopes(t *testing.T) {
	service := newTestService(t)
	tests := []struct {
		id     string
		secret string
		scope  string
	}{
		{id: "media", secret: "media-secret", scope: ScopeHazardsSummary},
		{id: "field", secret: "field-secret", scope: ScopeHazardsRead},
		{id: "ops", secret: "ops-secret", scope: ScopeHazardsRead},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			pair, err := service.Issue(test.id, test.secret)
			if err != nil {
				t.Fatalf("Issue() error = %v", err)
			}
			if pair.Scope != test.scope {
				t.Fatalf("scope = %q, want %q", pair.Scope, test.scope)
			}
			claims := parseAccessToken(t, service, pair.AccessToken)
			if claims.Subject != test.id || claims.Scope != test.scope || claims.TokenType != "access" {
				t.Fatalf("unexpected claims: %+v", claims)
			}
		})
	}
}

func TestIssueRejectsCredentialFromAnotherIdentity(t *testing.T) {
	service := newTestService(t)
	_, err := service.Issue("media", "field-secret")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Issue() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestNewServiceRejectsSharedClientSecret(t *testing.T) {
	_, err := NewService(Config{
		SigningSecret:  "test-signing-secret-at-least-32-characters",
		Issuer:         "bnpb-auth",
		Audience:       "bnpb-api",
		AccessTokenTTL: time.Minute,
		RefreshTTL:     10 * time.Minute,
		Clients: []Client{
			{ID: "media", Secret: "shared-secret", Scope: ScopeHazardsSummary},
			{ID: "field", Secret: "shared-secret", Scope: ScopeHazardsRead},
			{ID: "ops", Secret: "ops-secret", Scope: ScopeHazardsRead},
		},
	})
	if err == nil {
		t.Fatal("NewService() accepted a secret shared by two client identities")
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	service := newTestService(t)
	first, err := service.Issue("field", "field-secret")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	second, err := service.Refresh(first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatal("refresh did not rotate both tokens")
	}
	if _, err := service.Refresh(first.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("reusing old refresh token error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestExpiredRefreshTokenIsRejected(t *testing.T) {
	service := newTestService(t)
	base := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	pair, err := service.Issue("field", "field-secret")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	service.now = func() time.Time { return base.Add(11 * time.Minute) }
	if _, err := service.Refresh(pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh() error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestIssueRemovesExpiredRefreshTokens(t *testing.T) {
	service := newTestService(t)
	base := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	if _, err := service.Issue("field", "field-secret"); err != nil {
		t.Fatalf("first Issue() error = %v", err)
	}
	service.now = func() time.Time { return base.Add(11 * time.Minute) }
	if _, err := service.Issue("field", "field-secret"); err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	if got := len(service.refresh); got != 1 {
		t.Fatalf("stored refresh tokens = %d, want only the new token", got)
	}
}

func TestHandlerRejectsInvalidClientAndExposesJWKS(t *testing.T) {
	service := newTestService(t)
	handler := NewHandler(service)
	mux := http.NewServeMux()
	handler.Register(mux)

	body := bytes.NewBufferString(`{"client_id":"media","client_secret":"wrong"}`)
	request := httptest.NewRequest(http.MethodPost, "/token", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST /token status = %d, want 401", response.Code)
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET JWKS status = %d, want 200", response.Code)
	}
	var document struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	if len(document.Keys) != 1 || document.Keys[0]["kid"] == "" || document.Keys[0]["x"] == "" {
		t.Fatalf("invalid JWKS: %+v", document)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(Config{
		SigningSecret:  "test-signing-secret-at-least-32-characters",
		Issuer:         "bnpb-auth",
		Audience:       "bnpb-api",
		AccessTokenTTL: time.Minute,
		RefreshTTL:     10 * time.Minute,
		Clients: []Client{
			{ID: "media", Secret: "media-secret", Scope: ScopeHazardsSummary},
			{ID: "field", Secret: "field-secret", Scope: ScopeHazardsRead},
			{ID: "ops", Secret: "ops-secret", Scope: ScopeHazardsRead},
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func parseAccessToken(t *testing.T, service *Service, raw string) Claims {
	t.Helper()
	claims := Claims{}
	parsed, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(*jwt.Token) (any, error) { return service.publicKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer("bnpb-auth"),
		jwt.WithAudience("bnpb-api"),
	)
	if err != nil || !parsed.Valid {
		t.Fatalf("parse access token: valid=%v err=%v", parsed.Valid, err)
	}
	return claims
}
