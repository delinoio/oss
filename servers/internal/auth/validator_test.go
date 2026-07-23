package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type staticKeySource struct {
	key any
	err error
}

func (s staticKeySource) Key(context.Context, string, string) (any, error) {
	return s.key, s.err
}

func TestValidatorAcceptsUserAndM2MClaims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	key := mustRSAKey(t)
	validator := mustValidator(t, now, &key.PublicKey)

	userToken := signToken(t, key, now, map[string]any{
		"sub":       "logto-user-1",
		"client_id": "delidev-spa",
		"scope":     "account:read organization:write",
	})
	user, err := validator.ValidateUser(context.Background(), userToken, "account:read")
	if err != nil {
		t.Fatalf("ValidateUser() error = %v", err)
	}
	if user.UserID != "logto-user-1" || user.ClientID != "delidev-spa" || user.Type != TokenTypeUser {
		t.Fatalf("unexpected user claims: %#v", user)
	}
	if !user.HasScopes("account:read", "organization:write") {
		t.Fatalf("user scopes = %#v", user.Scopes)
	}

	m2mToken := signToken(t, key, now, map[string]any{
		"sub":       "usage-service",
		"client_id": "usage-service",
		"gty":       "client_credentials",
		"scope":     "usage:reserve usage:commit",
	})
	service, err := validator.ValidateM2M(context.Background(), m2mToken, "usage:reserve")
	if err != nil {
		t.Fatalf("ValidateM2M() error = %v", err)
	}
	if service.ServiceID != "usage-service" || service.Type != TokenTypeM2M {
		t.Fatalf("unexpected M2M claims: %#v", service)
	}
}

func TestValidatorRejectsInvalidClaims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	key := mustRSAKey(t)
	otherKey := mustRSAKey(t)
	validator := mustValidator(t, now, &key.PublicKey)

	tests := []struct {
		name   string
		claims map[string]any
		key    *rsa.PrivateKey
		typ    string
		call   func(string) error
		want   ErrorKind
	}{
		{
			name:   "issuer",
			claims: map[string]any{"iss": "https://attacker.example/oidc"},
			key:    key,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorIssuer,
		},
		{
			name:   "audience",
			claims: map[string]any{"aud": "https://other.example"},
			key:    key,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorAudience,
		},
		{
			name:   "expiry",
			claims: map[string]any{"exp": now.Add(-time.Minute).Unix()},
			key:    key,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorExpired,
		},
		{
			name:   "scope",
			claims: map[string]any{"scope": "account:read"},
			key:    key,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token, "organization:write")
				return err
			},
			want: ErrorScope,
		},
		{
			name: "user token passed as M2M",
			claims: map[string]any{
				"sub":       "logto-user-1",
				"client_id": "delidev-spa",
			},
			key: key,
			call: func(token string) error {
				_, err := validator.ValidateM2M(context.Background(), token)
				return err
			},
			want: ErrorTokenType,
		},
		{
			name: "M2M token passed as user",
			claims: map[string]any{
				"sub":       "usage-service",
				"client_id": "usage-service",
				"gty":       "client_credentials",
			},
			key: key,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorTokenType,
		},
		{
			name:   "signature",
			claims: map[string]any{},
			key:    otherKey,
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorSignature,
		},
		{
			name:   "header token type",
			claims: map[string]any{},
			key:    key,
			typ:    "ID",
			call: func(token string) error {
				_, err := validator.ValidateUser(context.Background(), token)
				return err
			},
			want: ErrorTokenType,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			token := signTokenWithType(t, test.key, now, test.claims, test.typ)
			err := test.call(token)
			var authFailure *Error
			if !errors.As(err, &authFailure) || authFailure.Kind != test.want {
				t.Fatalf("error = %v, want auth kind %s", err, test.want)
			}
		})
	}
}

func TestValidatorConfigurationFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := NewValidator(Config{
		Issuer:    "https://tenant.logto.app/oidc",
		Audience:  "https://wrong.example",
		KeySource: staticKeySource{},
	})
	if err == nil {
		t.Fatal("NewValidator() accepted non-canonical audience")
	}
	_, err = NewValidator(Config{
		Issuer:            "https://tenant.logto.app/oidc",
		Audience:          Audience,
		KeySource:         staticKeySource{},
		AllowedAlgorithms: []string{"HS256"},
	})
	if err == nil {
		t.Fatal("NewValidator() accepted symmetric signing algorithm")
	}
}

func mustValidator(t *testing.T, now time.Time, key *rsa.PublicKey) *Validator {
	t.Helper()
	validator, err := NewValidator(Config{
		Issuer:    "https://tenant.logto.app/oidc",
		Audience:  Audience,
		KeySource: staticKeySource{key: key},
		Clock:     ClockFunc(func() time.Time { return now }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signToken(t *testing.T, key *rsa.PrivateKey, now time.Time, overrides map[string]any) string {
	t.Helper()
	return signTokenWithType(t, key, now, overrides, "")
}

func signTokenWithType(
	t *testing.T,
	key *rsa.PrivateKey,
	now time.Time,
	overrides map[string]any,
	headerType string,
) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":       "https://tenant.logto.app/oidc",
		"aud":       Audience,
		"sub":       "logto-user-1",
		"client_id": "delidev-spa",
		"iat":       now.Add(-time.Minute).Unix(),
		"exp":       now.Add(time.Hour).Unix(),
		"jti":       "jwt-id-1",
		"scope":     "account:read",
	}
	for name, value := range overrides {
		claims[name] = value
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "key-1"
	if headerType == "" {
		headerType = "JWT"
	}
	token.Header["typ"] = headerType
	serialized, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}
