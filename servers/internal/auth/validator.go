package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Clock makes expiry validation deterministic in tests.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// KeySource resolves public signing keys. Implementations may use local test
// keys, an HTTP JWKS cache, or another provider-controlled source.
type KeySource interface {
	Key(context.Context, string, string) (any, error)
}

// Config defines strict Logto access-token validation.
type Config struct {
	Issuer             string
	Audience           string
	KeySource          KeySource
	Clock              Clock
	Leeway             time.Duration
	AllowedAlgorithms  []string
	AllowedHeaderTypes []string
}

// Validator validates user and M2M access tokens without making local
// authorization decisions.
type Validator struct {
	issuer      string
	audience    string
	keys        KeySource
	clock       Clock
	leeway      time.Duration
	algorithms  []string
	headerTypes map[string]struct{}
}

// NewValidator creates a fail-closed validator. The audience is configurable
// at construction time so services can load typed configuration, but this
// shared delibase contract rejects any value other than the canonical origin.
func NewValidator(config Config) (*Validator, error) {
	if config.Issuer == "" {
		return nil, errors.New("auth: issuer is required")
	}
	if config.Audience != Audience {
		return nil, fmt.Errorf("auth: audience must equal %s", Audience)
	}
	if config.KeySource == nil {
		return nil, errors.New("auth: key source is required")
	}
	if config.Clock == nil {
		config.Clock = systemClock{}
	}
	if config.Leeway < 0 {
		return nil, errors.New("auth: leeway cannot be negative")
	}
	if len(config.AllowedAlgorithms) == 0 {
		config.AllowedAlgorithms = []string{"RS256"}
	}
	for _, algorithm := range config.AllowedAlgorithms {
		if !isAsymmetricAlgorithm(algorithm) {
			return nil, fmt.Errorf("auth: unsupported signing algorithm %q", algorithm)
		}
	}
	if len(config.AllowedHeaderTypes) == 0 {
		config.AllowedHeaderTypes = []string{"JWT", "at+jwt"}
	}

	headerTypes := make(map[string]struct{}, len(config.AllowedHeaderTypes))
	for _, value := range config.AllowedHeaderTypes {
		if value == "" {
			return nil, errors.New("auth: empty JWT header type")
		}
		headerTypes[value] = struct{}{}
	}
	return &Validator{
		issuer:      config.Issuer,
		audience:    config.Audience,
		keys:        config.KeySource,
		clock:       config.Clock,
		leeway:      config.Leeway,
		algorithms:  append([]string(nil), config.AllowedAlgorithms...),
		headerTypes: headerTypes,
	}, nil
}

func isAsymmetricAlgorithm(algorithm string) bool {
	switch algorithm {
	case "RS256", "RS384", "RS512", "ES256", "ES384", "ES512":
		return true
	default:
		return false
	}
}

// ValidateUser validates a user access token and required Logto API scopes.
func (v *Validator) ValidateUser(ctx context.Context, token string, scopes ...string) (*UserClaims, error) {
	raw, tokenType, err := v.validate(ctx, token, scopes)
	if err != nil {
		return nil, err
	}
	if tokenType != TokenTypeUser {
		return nil, authError(ErrorTokenType)
	}
	typed := raw.typed(tokenType)
	return &UserClaims{TokenClaims: typed, UserID: typed.Subject}, nil
}

// ValidateM2M validates a client-credentials access token and required Logto
// API scopes. It does not validate local service-to-meter allowlists.
func (v *Validator) ValidateM2M(ctx context.Context, token string, scopes ...string) (*M2MClaims, error) {
	raw, tokenType, err := v.validate(ctx, token, scopes)
	if err != nil {
		return nil, err
	}
	if tokenType != TokenTypeM2M {
		return nil, authError(ErrorTokenType)
	}
	typed := raw.typed(tokenType)
	return &M2MClaims{TokenClaims: typed, ServiceID: typed.ClientID}, nil
}

func (v *Validator) validate(ctx context.Context, serialized string, scopes []string) (*rawClaims, TokenType, error) {
	if strings.TrimSpace(serialized) == "" {
		return nil, TokenTypeUnknown, authError(ErrorMissingToken)
	}
	claims := &rawClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(v.algorithms),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
		jwt.WithTimeFunc(v.clock.Now),
	)
	token, err := parser.ParseWithClaims(serialized, claims, func(token *jwt.Token) (any, error) {
		headerType, ok := token.Header["typ"].(string)
		if !ok {
			return nil, errInvalidHeaderType
		}
		if _, ok := v.headerTypes[headerType]; !ok {
			return nil, errInvalidHeaderType
		}
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" {
			return nil, errMissingKeyID
		}
		key, keyErr := v.keys.Key(ctx, keyID, token.Method.Alg())
		if keyErr != nil {
			return nil, errKeyLookup
		}
		return key, nil
	})
	if err != nil || token == nil || !token.Valid {
		return nil, TokenTypeUnknown, classifyJWTError(err)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != v.audience {
		return nil, TokenTypeUnknown, authError(ErrorAudience)
	}
	if claims.Subject == "" || claims.ClientID == "" {
		return nil, TokenTypeUnknown, authError(ErrorMalformedToken)
	}

	tokenType := classifyTokenType(claims)
	if tokenType == TokenTypeUnknown {
		return nil, TokenTypeUnknown, authError(ErrorTokenType)
	}
	typed := claims.typed(tokenType)
	if !typed.HasScopes(scopes...) {
		return nil, TokenTypeUnknown, authError(ErrorScope)
	}
	return claims, tokenType, nil
}

var (
	errInvalidHeaderType = errors.New("invalid JWT header type")
	errMissingKeyID      = errors.New("missing JWT key id")
	errKeyLookup         = errors.New("JWT key lookup failed")
)

func classifyTokenType(claims *rawClaims) TokenType {
	if claims.TokenUse != "" && claims.TokenUse != "access_token" {
		return TokenTypeUnknown
	}
	if claims.GrantType == "client_credentials" || claims.Subject == claims.ClientID {
		if claims.Subject != claims.ClientID {
			return TokenTypeUnknown
		}
		return TokenTypeM2M
	}
	return TokenTypeUser
}

func classifyJWTError(err error) error {
	switch {
	case errors.Is(err, errKeyLookup):
		return authError(ErrorKeyUnavailable)
	case errors.Is(err, errInvalidHeaderType):
		return authError(ErrorTokenType)
	case errors.Is(err, jwt.ErrTokenExpired):
		return authError(ErrorExpired)
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return authError(ErrorIssuer)
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return authError(ErrorAudience)
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return authError(ErrorSignature)
	default:
		return authError(ErrorMalformedToken)
	}
}
