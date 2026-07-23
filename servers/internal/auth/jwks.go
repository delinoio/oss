package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultMaxJWKSBytes int64 = 1 << 20

// HTTPClient is injectable for deterministic JWKS tests.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// JWKSConfig configures a concurrency-safe remote JWKS cache.
type JWKSConfig struct {
	URL              string
	Client           HTTPClient
	Clock            Clock
	CacheTTL         time.Duration
	MaxStale         time.Duration
	MaxResponseBytes int64
}

// JWKS caches public keys and refreshes immediately for an unknown key ID so
// Logto signing-key rotation does not have to wait for the cache TTL.
type JWKS struct {
	url      string
	client   HTTPClient
	clock    Clock
	ttl      time.Duration
	maxStale time.Duration
	maxBytes int64

	refreshMu  sync.Mutex
	mu         sync.RWMutex
	keys       map[string]jwkEntry
	fetchedAt  time.Time
	generation uint64
}

type jwkEntry struct {
	key any
	alg string
}

// NewJWKS creates a remote key source without performing network I/O.
func NewJWKS(config JWKSConfig) (*JWKS, error) {
	parsed, err := url.Parse(config.URL)
	if err != nil || !validJWKSURL(parsed) {
		return nil, errors.New("auth: JWKS URL must be an HTTPS URL without credentials, query, or fragment")
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 5 * time.Second}
	}
	if config.Clock == nil {
		config.Clock = systemClock{}
	}
	if config.CacheTTL <= 0 {
		config.CacheTTL = 15 * time.Minute
	}
	if config.MaxStale < 0 {
		return nil, errors.New("auth: JWKS max stale cannot be negative")
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxJWKSBytes
	}
	return &JWKS{
		url:      config.URL,
		client:   config.Client,
		clock:    config.Clock,
		ttl:      config.CacheTTL,
		maxStale: config.MaxStale,
		maxBytes: config.MaxResponseBytes,
		keys:     make(map[string]jwkEntry),
	}, nil
}

// Key implements KeySource.
func (s *JWKS) Key(ctx context.Context, keyID, algorithm string) (any, error) {
	if keyID == "" || algorithm == "" {
		return nil, errors.New("auth: invalid key lookup")
	}

	now := s.clock.Now()
	entry, found, fetchedAt, generation := s.snapshot(keyID)
	fresh := !fetchedAt.IsZero() && now.Sub(fetchedAt) < s.ttl
	if found && fresh {
		return matchAlgorithm(entry, algorithm)
	}

	// An unknown kid always forces a refresh, even while the cache is fresh.
	refreshErr := s.refresh(ctx, now, generation)
	if refreshErr == nil {
		entry, found, _, _ = s.snapshot(keyID)
		if !found {
			return nil, errors.New("auth: signing key not found")
		}
		return matchAlgorithm(entry, algorithm)
	}

	// A known public key can remain usable briefly during a provider outage.
	// Unknown keys and keys beyond MaxStale always fail closed.
	if found && s.maxStale > 0 && now.Sub(fetchedAt) <= s.ttl+s.maxStale {
		return matchAlgorithm(entry, algorithm)
	}
	return nil, ErrKeyUnavailable
}

func (s *JWKS) snapshot(keyID string) (jwkEntry, bool, time.Time, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, found := s.keys[keyID]
	return entry, found, s.fetchedAt, s.generation
}

func (s *JWKS) refresh(ctx context.Context, now time.Time, observedGeneration uint64) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	s.mu.RLock()
	currentGeneration := s.generation
	s.mu.RUnlock()
	if currentGeneration != observedGeneration {
		return nil
	}

	keys, err := s.fetch(ctx)
	if err != nil {
		return ErrKeyUnavailable
	}
	s.mu.Lock()
	s.keys = keys
	s.fetchedAt = now
	s.generation++
	s.mu.Unlock()
	return nil
}

func (s *JWKS) fetch(ctx context.Context) (map[string]jwkEntry, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, errors.New("auth: could not create JWKS request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, errors.New("auth: JWKS request failed")
	}
	defer response.Body.Close()
	if response.Request == nil || !validJWKSURL(response.Request.URL) {
		return nil, errors.New("auth: JWKS endpoint redirected to an unsafe URL")
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("auth: JWKS endpoint returned an error")
	}

	limited := io.LimitReader(response.Body, s.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > s.maxBytes {
		return nil, errors.New("auth: invalid JWKS response")
	}
	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, errors.New("auth: invalid JWKS response")
	}
	keys, err := parseJWKS(document)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func validJWKSURL(candidate *url.URL) bool {
	return candidate != nil &&
		candidate.Scheme == "https" &&
		candidate.Host != "" &&
		candidate.User == nil &&
		candidate.RawQuery == "" &&
		candidate.Fragment == ""
}

func matchAlgorithm(entry jwkEntry, algorithm string) (any, error) {
	if entry.alg != "" && entry.alg != algorithm {
		return nil, errors.New("auth: signing key algorithm mismatch")
	}
	switch key := entry.key.(type) {
	case *rsa.PublicKey:
		if algorithm != "RS256" && algorithm != "RS384" && algorithm != "RS512" {
			return nil, errors.New("auth: signing key type mismatch")
		}
	case *ecdsa.PublicKey:
		if !matchesECDSACurve(key, algorithm) {
			return nil, errors.New("auth: signing key type mismatch")
		}
	default:
		return nil, errors.New("auth: unsupported signing key")
	}
	return entry.key, nil
}

func matchesECDSACurve(key *ecdsa.PublicKey, algorithm string) bool {
	if key == nil || key.Curve == nil || key.Curve.Params() == nil {
		return false
	}
	params := key.Curve.Params()
	switch algorithm {
	case "ES256":
		return params.Name == "P-256" && params.BitSize == 256
	case "ES384":
		return params.Name == "P-384" && params.BitSize == 384
	case "ES512":
		return params.Name == "P-521" && params.BitSize == 521
	default:
		return false
	}
}

type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	KeyID string `json:"kid"`
	Type  string `json:"kty"`
	Use   string `json:"use"`
	Alg   string `json:"alg"`
	N     string `json:"n"`
	E     string `json:"e"`
	X     string `json:"x"`
	Y     string `json:"y"`
	Curve string `json:"crv"`
}

func parseJWKS(document jwksDocument) (map[string]jwkEntry, error) {
	if len(document.Keys) == 0 {
		return nil, errors.New("auth: JWKS contains no keys")
	}
	result := make(map[string]jwkEntry, len(document.Keys))
	for _, raw := range document.Keys {
		if raw.KeyID == "" || (raw.Use != "" && raw.Use != "sig") {
			continue
		}
		if _, duplicate := result[raw.KeyID]; duplicate {
			return nil, errors.New("auth: JWKS contains duplicate key IDs")
		}
		key, err := parseJWK(raw)
		if err != nil {
			return nil, err
		}
		result[raw.KeyID] = jwkEntry{key: key, alg: raw.Alg}
	}
	if len(result) == 0 {
		return nil, errors.New("auth: JWKS contains no signing keys")
	}
	return result, nil
}

func parseJWK(raw jsonWebKey) (any, error) {
	switch raw.Type {
	case "RSA":
		modulus, err := decodeBigInt(raw.N)
		if err != nil || modulus.Sign() <= 0 {
			return nil, errors.New("auth: invalid RSA signing key")
		}
		exponent, err := decodeBigInt(raw.E)
		if err != nil || !exponent.IsInt64() || exponent.Int64() < 3 {
			return nil, errors.New("auth: invalid RSA signing key")
		}
		return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
	case "EC":
		curve := curveByName(raw.Curve)
		if curve == nil {
			return nil, errors.New("auth: unsupported EC signing key")
		}
		x, errX := decodeBigInt(raw.X)
		y, errY := decodeBigInt(raw.Y)
		if errX != nil || errY != nil || !curve.IsOnCurve(x, y) {
			return nil, errors.New("auth: invalid EC signing key")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return nil, errors.New("auth: unsupported signing key type")
	}
}

func decodeBigInt(value string) (*big.Int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 {
		return nil, errors.New("invalid base64url integer")
	}
	return new(big.Int).SetBytes(decoded), nil
}

func curveByName(name string) elliptic.Curve {
	switch name {
	case "P-256":
		return elliptic.P256()
	case "P-384":
		return elliptic.P384()
	case "P-521":
		return elliptic.P521()
	default:
		return nil
	}
}
