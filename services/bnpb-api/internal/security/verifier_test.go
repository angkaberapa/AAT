package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type staticKeyProvider struct {
	kid string
	key ed25519.PublicKey
}

func (provider staticKeyProvider) Key(_ context.Context, kid string) (ed25519.PublicKey, error) {
	if kid != provider.kid {
		return nil, ErrInvalidToken
	}
	return provider.key, nil
}

func TestVerifierAcceptsValidTokenAndRejectsExpiredToken(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	base := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)
	verifier, err := NewVerifier(staticKeyProvider{kid: "test-key", key: publicKey}, "bnpb-auth", "bnpb-api")
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	verifier.clock = func() time.Time { return base }
	raw := signTestToken(t, privateKey, "test-key", base.Add(time.Minute))

	principal, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.ClientID != "media" || !principal.HasScope(ScopeHazardsSummary) {
		t.Fatalf("unexpected principal: %+v", principal)
	}

	verifier.clock = func() time.Time { return base.Add(61 * time.Second) }
	if _, err := verifier.Verify(context.Background(), raw); err != ErrInvalidToken {
		t.Fatalf("expired Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestMiddlewareRequiresBearerToken(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	verifier, err := NewVerifier(staticKeyProvider{kid: "test-key", key: publicKey}, "bnpb-auth", "bnpb-api")
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	handler := verifier.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler should not be called")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hazards", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

type standardHTTPClient struct{ client *http.Client }

func (client standardHTTPClient) Do(request *http.Request, _ string) (*http.Response, error) {
	return client.client.Do(request)
}

func TestJWKSProviderFetchesEd25519Key(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/.well-known/jwks.json" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA",
			"kid": "test-key", "x": base64.RawURLEncoding.EncodeToString(publicKey),
		}}})
	}))
	defer server.Close()

	provider, err := NewJWKSProvider(server.URL, standardHTTPClient{client: server.Client()}, time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error = %v", err)
	}
	got, err := provider.Key(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("Key() error = %v", err)
	}
	if !got.Equal(publicKey) {
		t.Fatal("JWKS public key does not match")
	}
}

func signTestToken(t *testing.T, privateKey ed25519.PrivateKey, kid string, expires time.Time) string {
	t.Helper()
	claims := Claims{
		Scope:     ScopeHazardsSummary,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "bnpb-auth",
			Subject:   "media",
			Audience:  jwt.ClaimStrings{"bnpb-api"},
			ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt:  jwt.NewNumericDate(expires.Add(-time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = kid
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}
