package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	ErrInvalidCredentials  = errors.New("invalid client credentials")
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
)

type Client struct {
	ID     string
	Secret string
	Scope  string
}

type Config struct {
	SigningSecret  string
	Issuer         string
	Audience       string
	AccessTokenTTL time.Duration
	RefreshTTL     time.Duration
	Clients        []Client
}

type Claims struct {
	Scope     string `json:"scope"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

type Pair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type refreshRecord struct {
	clientID string
	scope    string
	expires  time.Time
}

type storedClient struct {
	id           string
	secretDigest [sha256.Size]byte
	scope        string
}

type Service struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	kid        string
	issuer     string
	audience   string
	accessTTL  time.Duration
	refreshTTL time.Duration
	clients    map[string]storedClient

	mu      sync.Mutex
	refresh map[[sha256.Size]byte]refreshRecord
	now     func() time.Time
	random  io.Reader
}

func NewService(config Config) (*Service, error) {
	if len(config.SigningSecret) < 32 {
		return nil, errors.New("signing secret must contain at least 32 characters")
	}
	if config.Issuer == "" || config.Audience == "" {
		return nil, errors.New("issuer and audience are required")
	}
	if config.AccessTokenTTL <= 0 || config.RefreshTTL <= 0 {
		return nil, errors.New("token TTL values must be positive")
	}
	if config.RefreshTTL <= config.AccessTokenTTL {
		return nil, errors.New("refresh token TTL must exceed access token TTL")
	}

	clients := make(map[string]storedClient, len(config.Clients))
	secretDigests := make(map[[sha256.Size]byte]struct{}, len(config.Clients))
	for _, client := range config.Clients {
		if strings.TrimSpace(client.ID) == "" || strings.TrimSpace(client.Secret) == "" || strings.TrimSpace(client.Scope) == "" {
			return nil, errors.New("client ID, secret, and scope are required")
		}
		if _, exists := clients[client.ID]; exists {
			return nil, fmt.Errorf("duplicate client ID %q", client.ID)
		}
		digest := sha256.Sum256([]byte(client.Secret))
		if _, exists := secretDigests[digest]; exists {
			return nil, errors.New("downstream client secrets must be distinct")
		}
		secretDigests[digest] = struct{}{}
		clients[client.ID] = storedClient{id: client.ID, secretDigest: digest, scope: client.Scope}
	}
	if len(clients) != 3 {
		return nil, fmt.Errorf("exactly three downstream client identities are required, got %d", len(clients))
	}

	seed := sha256.Sum256([]byte(config.SigningSecret))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyDigest := sha256.Sum256(publicKey)

	return &Service{
		privateKey: privateKey,
		publicKey:  publicKey,
		kid:        hex.EncodeToString(keyDigest[:8]),
		issuer:     config.Issuer,
		audience:   config.Audience,
		accessTTL:  config.AccessTokenTTL,
		refreshTTL: config.RefreshTTL,
		clients:    clients,
		refresh:    make(map[[sha256.Size]byte]refreshRecord),
		now:        time.Now,
		random:     rand.Reader,
	}, nil
}

func (service *Service) Issue(clientID, clientSecret string) (Pair, error) {
	client, exists := service.clients[clientID]
	providedDigest := sha256.Sum256([]byte(clientSecret))
	if !exists || subtle.ConstantTimeCompare(client.secretDigest[:], providedDigest[:]) != 1 {
		return Pair{}, ErrInvalidCredentials
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	service.deleteExpiredRefreshTokens(service.now().UTC())
	return service.issuePair(client.id, client.scope)
}

func (service *Service) Refresh(rawToken string) (Pair, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Pair{}, ErrInvalidRefreshToken
	}
	digest := sha256.Sum256([]byte(rawToken))

	service.mu.Lock()
	defer service.mu.Unlock()
	service.deleteExpiredRefreshTokens(service.now().UTC())
	record, exists := service.refresh[digest]
	if !exists {
		return Pair{}, ErrInvalidRefreshToken
	}
	if !service.now().Before(record.expires) {
		delete(service.refresh, digest)
		return Pair{}, ErrInvalidRefreshToken
	}

	pair, err := service.issuePair(record.clientID, record.scope)
	if err != nil {
		return Pair{}, err
	}
	delete(service.refresh, digest)
	return pair, nil
}

func (service *Service) deleteExpiredRefreshTokens(now time.Time) {
	for digest, record := range service.refresh {
		if !now.Before(record.expires) {
			delete(service.refresh, digest)
		}
	}
}

func (service *Service) issuePair(clientID, scope string) (Pair, error) {
	now := service.now().UTC()
	jti, err := randomString(service.random, 16)
	if err != nil {
		return Pair{}, fmt.Errorf("generate access token ID: %w", err)
	}
	claims := Claims{
		Scope:     scope,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    service.issuer,
			Subject:   clientID,
			Audience:  jwt.ClaimStrings{service.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(service.accessTTL)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
	}
	access := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	access.Header["kid"] = service.kid
	signedAccess, err := access.SignedString(service.privateKey)
	if err != nil {
		return Pair{}, fmt.Errorf("sign access token: %w", err)
	}

	refreshToken, err := randomString(service.random, 32)
	if err != nil {
		return Pair{}, fmt.Errorf("generate refresh token: %w", err)
	}
	service.refresh[sha256.Sum256([]byte(refreshToken))] = refreshRecord{
		clientID: clientID,
		scope:    scope,
		expires:  now.Add(service.refreshTTL),
	}

	return Pair{
		AccessToken:  signedAccess,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(service.accessTTL / time.Second),
		Scope:        scope,
	}, nil
}

func (service *Service) JWKS() map[string]any {
	return map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP",
			"crv": "Ed25519",
			"use": "sig",
			"alg": jwt.SigningMethodEdDSA.Alg(),
			"kid": service.kid,
			"x":   base64.RawURLEncoding.EncodeToString(service.publicKey),
		}},
	}
}

func randomString(reader io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
