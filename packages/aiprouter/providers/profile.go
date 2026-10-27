// SPDX-License-Identifier: Apache-2.0
// Protocol constants adapted from pinned upstream; see snapshot/NOTICE.
package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"regexp"
	"strings"
	"time"
)

var profilePlan = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// AccountProfile is PRIVATE, bearer-bound provider evidence. It is not a service
// principal and must never be used to merge personal account slots automatically.
type AccountProfile struct {
	Scope                                          core.Scope
	Provider, AccountID, Email, Plan, BearerSHA256 string
	ObservedAt                                     time.Time
}

func (p AccountProfile) Matches(c Credential) bool {
	h := sha256.Sum256([]byte(c.AccessToken))
	return p.Scope == c.Scope && p.Provider == c.Provider && p.BearerSHA256 == hex.EncodeToString(h[:])
}
func (a *Auth) Profile(ctx context.Context, s core.Scope, c Credential) (AccountProfile, error) {
	if !validScope(s) || c.Scope != s {
		return AccountProfile{}, ErrScope
	}
	if !safeString(c.AccessToken) {
		return AccountProfile{}, ErrReauthenticate
	}
	h := bearer(c.AccessToken)
	endpoint := ""
	switch c.Provider {
	case "anthropic", "anthropic2":
		endpoint = "https://api.anthropic.com/api/oauth/profile"
		h.Set("Anthropic-Beta", "oauth-2025-04-20")
		h.Set("Cache-Control", "no-cache")
		h.Set("User-Agent", "claude-cli/2.1.280 (external, cli)")
	case "google-antigravity":
		endpoint = "https://www.googleapis.com/oauth2/v2/userinfo"
	default:
		return AccountProfile{}, ErrProfileUnavailable
	}
	m, err := a.send(ctx, s, "GET", endpoint, nil, h)
	if err != nil {
		return AccountProfile{}, err
	}
	p := AccountProfile{Scope: s, Provider: c.Provider, ObservedAt: a.now()}
	switch c.Provider {
	case "anthropic", "anthropic2":
		account := obj(m, "account")
		p.AccountID = str(account, "uuid")
		p.Email = str(account, "email")
		org := obj(m, "organization")
		p.Plan = str(org, "rate_limit_tier")
		if p.Plan == "" {
			p.Plan = str(org, "organization_type")
		}
		p.Plan = strings.TrimPrefix(strings.TrimPrefix(p.Plan, "default_claude_"), "claude_")
	case "google-antigravity":
		p.AccountID = str(m, "id")
		p.Email = str(m, "email")
	}
	if !modelID(p.AccountID) || len(p.AccountID) > 128 {
		return AccountProfile{}, ErrResponse
	}
	if c.AccountID != "" && c.AccountID != p.AccountID {
		return AccountProfile{}, ErrReauthenticate
	}
	if !safeString(p.Email) || len(p.Email) > 320 {
		p.Email = ""
	}
	if !profilePlan.MatchString(p.Plan) {
		p.Plan = ""
	}
	digest := sha256.Sum256([]byte(c.AccessToken))
	p.BearerSHA256 = hex.EncodeToString(digest[:])
	return p, nil
}

// EnrichCredential returns a copy with authenticated provider identity and plan.
// Persist using the same private-version CAS as the token that was probed. Profile
// unavailability is returned separately so callers can retain a usable token while
// leaving identity unresolved; never treat grant/JWT hints as a profile proof.
func (a *Auth) EnrichCredential(ctx context.Context, s core.Scope, c Credential) (Credential, AccountProfile, error) {
	p, err := a.Profile(ctx, s, c)
	if err != nil {
		return c, AccountProfile{}, err
	}
	meta := make(map[string]string, len(c.Metadata)+4)
	for k, v := range c.Metadata {
		meta[k] = v
	}
	meta["profileBearerSHA256"] = p.BearerSHA256
	meta["profileAccountId"] = p.AccountID
	meta["profileEmail"] = p.Email
	meta["profilePlan"] = p.Plan
	c.Metadata = meta
	c.AccountID = p.AccountID
	return c, p, nil
}
