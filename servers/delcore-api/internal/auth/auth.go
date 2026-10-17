// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
)

type Identity struct {
	SubjectHash, Email, ClientID string
	Operator                     bool
	Scopes                       []string
}
type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}
type Logto struct {
	verify *oidc.IDTokenVerifier
	config config.Config
}

func New(ctx context.Context, c config.Config) (*Logto, error) {
	client := &http.Client{Timeout: c.Timeout()}
	ctx = oidc.ClientContext(ctx, client)
	p, e := oidc.NewProvider(ctx, c.Issuer)
	if e != nil {
		return nil, e
	}
	return &Logto{p.Verifier(&oidc.Config{ClientID: c.Audience, SupportedSigningAlgs: []string{"RS256", "ES256"}}), c}, nil
}
func (l *Logto) Verify(ctx context.Context, header string) (Identity, error) {
	var id Identity
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return id, errors.New("invalid authorization")
	}
	ctx, cancel := context.WithTimeout(ctx, l.config.Timeout())
	defer cancel()
	token, e := l.verify.Verify(ctx, parts[1])
	if e != nil {
		return id, errors.New("token verification failed")
	}
	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		ClientID      string   `json:"client_id"`
		Scope         string   `json:"scope"`
		Roles         []string `json:"roles"`
	}
	if e = token.Claims(&claims); e != nil || token.Subject == "" {
		return id, errors.New("invalid claims")
	}
	id.Scopes = strings.Fields(claims.Scope)
	// Logto client-credentials subjects are application IDs. A delegated user
	// token may also carry client_id; it must not acquire the service's identity.
	if claims.ClientID != "" && token.Subject == claims.ClientID {
		id.ClientID = claims.ClientID
		return id, nil
	}
	if !claims.EmailVerified || claims.Email == "" {
		return id, errors.New("verified email required")
	}
	mac := hmac.New(sha256.New, []byte(l.config.IdentityKey))
	mac.Write([]byte(token.Issuer + "\x00" + token.Subject))
	id.SubjectHash = hex.EncodeToString(mac.Sum(nil))
	id.Email = strings.ToLower(claims.Email)
	id.Operator = slices.Contains(claims.Roles, l.config.OperatorRole)
	return id, nil
}
