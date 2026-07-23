package auth

import (
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Audience is the only API resource audience accepted by delibase.
	Audience = "https://delibase.deli.dev"

	// ForwardedUserTokenHeader carries the end-user access token on M2M usage
	// calls. Its value must never be logged or copied into diagnostics.
	ForwardedUserTokenHeader = "X-Delibase-Forwarded-User-Token"
)

// TokenType identifies the authenticated Logto principal. It deliberately
// does not represent a delibase role or permission.
type TokenType uint8

const (
	TokenTypeUnknown TokenType = iota
	TokenTypeUser
	TokenTypeM2M
)

// TokenClaims is the typed, security-relevant subset of a Logto access token.
// Delibase-owned organization, team, billing, and authorization state does not
// belong in these claims.
type TokenClaims struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	JWTID     string
	ClientID  string
	Scopes    []string
	Type      TokenType
}

// HasScopes reports whether all required scopes are present.
func (c TokenClaims) HasScopes(required ...string) bool {
	for _, scope := range required {
		if scope == "" || !slices.Contains(c.Scopes, scope) {
			return false
		}
	}
	return true
}

// UserClaims describes an authenticated end user. Subject is a Logto user ID,
// not a local delibase user, organization, or role.
type UserClaims struct {
	TokenClaims
	UserID string
}

// M2MClaims describes an authenticated service. ServiceID is the Logto client
// ID; meter allowlists and all local authorization remain consumer-owned.
type M2MClaims struct {
	TokenClaims
	ServiceID string
}

// rawClaims mirrors only the JWT fields needed for validation.
type rawClaims struct {
	jwt.RegisteredClaims
	Scope     string `json:"scope"`
	ClientID  string `json:"client_id"`
	GrantType string `json:"gty"`
	TokenUse  string `json:"token_use"`
}

func (c *rawClaims) typed(tokenType TokenType) TokenClaims {
	claims := TokenClaims{
		Issuer:   c.Issuer,
		Subject:  c.Subject,
		Audience: append([]string(nil), c.Audience...),
		JWTID:    c.ID,
		ClientID: c.ClientID,
		Scopes:   strings.Fields(c.Scope),
		Type:     tokenType,
	}
	if c.ExpiresAt != nil {
		claims.ExpiresAt = c.ExpiresAt.Time
	}
	if c.IssuedAt != nil {
		claims.IssuedAt = c.IssuedAt.Time
	}
	return claims
}
