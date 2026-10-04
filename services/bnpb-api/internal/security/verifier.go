package security

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ScopeHazardsSummary = "hazards:summary"
	ScopeHazardsRead    = "hazards:read"
)

var ErrInvalidToken = errors.New("invalid access token")

type HTTPClient interface {
	Do(request *http.Request, target string) (*http.Response, error)
}

type Claims struct {
	Scope     string `json:"scope"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

type Principal struct {
	ClientID string
	Scopes   map[string]struct{}
}

func (principal Principal) HasScope(scope string) bool {
	_, exists := principal.Scopes[scope]
	return exists
}

type KeyProvider interface {
	Key(ctx context.Context, kid string) (ed25519.PublicKey, error)
}

type Verifier struct {
	keys     KeyProvider
	issuer   string
	audience string
	clock    func() time.Time
}

func NewVerifier(keys KeyProvider, issuer, audience string) (*Verifier, error) {
	if keys == nil || issuer == "" || audience == "" {
		return nil, errors.New("key provider, issuer, and audience are required")
	}
	return &Verifier{keys: keys, issuer: issuer, audience: audience, clock: time.Now}, nil
}

func (verifier *Verifier) Verify(ctx context.Context, raw string) (Principal, error) {
	claims := Claims{}
	parsed, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodEdDSA {
				return nil, fmt.Errorf("unexpected signing algorithm %q", token.Method.Alg())
			}
			kid, ok := token.Header["kid"].(string)
			if !ok || kid == "" {
				return nil, errors.New("token is missing kid")
			}
			return verifier.keys.Key(ctx, kid)
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(verifier.issuer),
		jwt.WithAudience(verifier.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(verifier.clock),
	)
	if err != nil || !parsed.Valid || claims.TokenType != "access" || claims.Subject == "" {
		return Principal{}, ErrInvalidToken
	}

	scopes := make(map[string]struct{})
	for _, scope := range strings.Fields(claims.Scope) {
		scopes[scope] = struct{}{}
	}
	if len(scopes) == 0 {
		return Principal{}, ErrInvalidToken
	}
	return Principal{ClientID: claims.Subject, Scopes: scopes}, nil
}

type JWKSProvider struct {
	url       string
	client    HTTPClient
	cacheTTL  time.Duration
	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	fetchedAt time.Time
	now       func() time.Time
}

func NewJWKSProvider(baseURL string, client HTTPClient, cacheTTL time.Duration) (*JWKSProvider, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid auth base URL %q", baseURL)
	}
	if client == nil || cacheTTL <= 0 {
		return nil, errors.New("HTTP client and positive JWKS cache TTL are required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/.well-known/jwks.json"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &JWKSProvider{
		url:      parsed.String(),
		client:   client,
		cacheTTL: cacheTTL,
		keys:     make(map[string]ed25519.PublicKey),
		now:      time.Now,
	}, nil
}

func (provider *JWKSProvider) Key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if key, exists := provider.keys[kid]; exists && provider.now().Before(provider.fetchedAt.Add(provider.cacheTTL)) {
		return append(ed25519.PublicKey(nil), key...), nil
	}
	if err := provider.refresh(ctx); err != nil {
		return nil, err
	}
	key, exists := provider.keys[kid]
	if !exists {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

type jwksDocument struct {
	Keys []struct {
		KeyType string `json:"kty"`
		Curve   string `json:"crv"`
		Use     string `json:"use"`
		Alg     string `json:"alg"`
		KeyID   string `json:"kid"`
		X       string `json:"x"`
	} `json:"keys"`
}

func (provider *JWKSProvider) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.url, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request, "auth")
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: unexpected status %d", response.StatusCode)
	}
	var document jwksDocument
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}
	keys := make(map[string]ed25519.PublicKey, len(document.Keys))
	for _, encoded := range document.Keys {
		if encoded.KeyType != "OKP" || encoded.Curve != "Ed25519" || encoded.Alg != jwt.SigningMethodEdDSA.Alg() || encoded.Use != "sig" || encoded.KeyID == "" {
			continue
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(encoded.X)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			continue
		}
		keys[encoded.KeyID] = ed25519.PublicKey(publicKey)
	}
	if len(keys) == 0 {
		return errors.New("JWKS contains no supported signing keys")
	}
	provider.keys = keys
	provider.fetchedAt = provider.now()
	return nil
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func (verifier *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeUnauthorized(w, "Bearer token is required")
			return
		}
		principal, err := verifier.Verify(request.Context(), parts[1])
		if err != nil {
			writeUnauthorized(w, "invalid or expired access token")
			return
		}
		next.ServeHTTP(w, request.WithContext(WithPrincipal(request.Context(), principal)))
	})
}

func writeUnauthorized(w http.ResponseWriter, description string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="bnpb-api", error="invalid_token"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             "invalid_token",
		"error_description": description,
	})
}
