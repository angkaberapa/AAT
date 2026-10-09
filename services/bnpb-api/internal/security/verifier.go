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

var (
	ErrInvalidToken           = errors.New("invalid access token")
	ErrKeyProviderUnavailable = errors.New("signing key provider unavailable")
)

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
	if errors.Is(err, ErrKeyProviderUnavailable) {
		return Principal{}, ErrKeyProviderUnavailable
	}
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
	url             string
	client          HTTPClient
	cacheTTL        time.Duration
	mu              sync.RWMutex
	refreshMu       sync.Mutex
	keys            map[string]ed25519.PublicKey
	fetchedAt       time.Time
	nextRefresh     time.Time
	refreshFailures uint
	now             func() time.Time
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
	key, exists, fresh := provider.cachedKey(kid)
	if exists && fresh {
		return append(ed25519.PublicKey(nil), key...), nil
	}

	if exists {
		if provider.refreshAllowed() && provider.refreshMu.TryLock() {
			err := provider.refresh(ctx)
			provider.refreshMu.Unlock()
			if err != nil {
				return append(ed25519.PublicKey(nil), key...), nil
			}
			key, exists, _ = provider.cachedKey(kid)
			if !exists {
				return nil, fmt.Errorf("unknown signing key %q", kid)
			}
			return append(ed25519.PublicKey(nil), key...), nil
		}
		return append(ed25519.PublicKey(nil), key...), nil
	}

	provider.refreshMu.Lock()
	defer provider.refreshMu.Unlock()
	if key, exists, fresh = provider.cachedKey(kid); exists && fresh {
		return append(ed25519.PublicKey(nil), key...), nil
	}
	if !provider.refreshAllowed() {
		return nil, ErrKeyProviderUnavailable
	}
	if err := provider.refresh(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyProviderUnavailable, err)
	}
	key, exists, _ = provider.cachedKey(kid)
	if !exists {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func (provider *JWKSProvider) cachedKey(kid string) (ed25519.PublicKey, bool, bool) {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	key, exists := provider.keys[kid]
	fresh := exists && provider.now().Before(provider.fetchedAt.Add(provider.cacheTTL))
	return append(ed25519.PublicKey(nil), key...), exists, fresh
}

func (provider *JWKSProvider) refreshAllowed() bool {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	return !provider.now().Before(provider.nextRefresh)
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
		provider.recordRefreshFailure()
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		provider.recordRefreshFailure()
		return fmt.Errorf("fetch JWKS: unexpected status %d", response.StatusCode)
	}
	var document jwksDocument
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		provider.recordRefreshFailure()
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
		provider.recordRefreshFailure()
		return errors.New("JWKS contains no supported signing keys")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.keys = keys
	provider.fetchedAt = provider.now()
	provider.nextRefresh = time.Time{}
	provider.refreshFailures = 0
	return nil
}

func (provider *JWKSProvider) recordRefreshFailure() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.refreshFailures++
	shift := provider.refreshFailures - 1
	if shift > 5 {
		shift = 5
	}
	backoff := time.Second * time.Duration(1<<shift)
	provider.nextRefresh = provider.now().Add(backoff)
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
			if errors.Is(err, ErrKeyProviderUnavailable) {
				writeServiceUnavailable(w)
				return
			}
			writeUnauthorized(w, "invalid or expired access token")
			return
		}
		next.ServeHTTP(w, request.WithContext(WithPrincipal(request.Context(), principal)))
	})
}

func writeServiceUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             "temporarily_unavailable",
		"error_description": "token verification service is temporarily unavailable",
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
