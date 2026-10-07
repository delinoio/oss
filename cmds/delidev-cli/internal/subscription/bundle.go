// Package subscription validates the pinned Codex managed-login file. It never
// imports a user's login or treats unsigned JWT claims as authentication proof.
package subscription

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const MaxBundle = 64 << 10

type Bundle struct {
	AuthMode string  `json:"auth_mode,omitempty"`
	APIKey   *string `json:"OPENAI_API_KEY"`
	Tokens   struct {
		ID      string `json:"id_token"`
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Account string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh time.Time `json:"last_refresh"`
}

type Identity struct{ Account, User, Email, Plan, DisplayName string }

func Invalid() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed Codex authentication evidence is invalid or incomplete.", "Retain exclusive ownership and reconcile the original operation; never import or redistribute an older login.")
}

func Parse(raw []byte) (Bundle, Identity, error) {
	var b Bundle
	var identity Identity
	if len(raw) == 0 || len(raw) > MaxBundle || domain.Decode(raw, &b) != nil || (b.AuthMode != "" && b.AuthMode != "chatgpt") || b.APIKey != nil || b.LastRefresh.IsZero() || b.LastRefresh.After(time.Now().UTC().Add(time.Minute)) {
		return b, identity, Invalid()
	}
	for _, token := range []string{b.Tokens.ID, b.Tokens.Access, b.Tokens.Refresh, b.Tokens.Account} {
		if len(token) == 0 || len(token) > 24<<10 || strings.ContainsAny(token, "\x00\r\n\t ") {
			return b, identity, Invalid()
		}
	}
	for i, token := range []string{b.Tokens.ID, b.Tokens.Access} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
			return b, identity, Invalid()
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return b, identity, Invalid()
		}
		var claims struct {
			Email   string `json:"email"`
			Name    string `json:"name"`
			Profile struct {
				Email string `json:"email"`
				Name  string `json:"name"`
			} `json:"https://api.openai.com/profile"`
			Auth struct {
				Account string `json:"chatgpt_account_id"`
				User    string `json:"chatgpt_user_id"`
				UserID  string `json:"user_id"`
				Plan    string `json:"chatgpt_plan_type"`
			} `json:"https://api.openai.com/auth"`
		}
		err = json.Unmarshal(payload, &claims)
		clear(payload)
		if err != nil || claims.Auth.Account != b.Tokens.Account {
			return b, identity, Invalid()
		}
		user := claims.Auth.User
		if user == "" {
			user = claims.Auth.UserID
		}
		if user == "" {
			return b, identity, Invalid()
		}
		if i == 0 {
			email := claims.Email
			if email == "" {
				email = claims.Profile.Email
			}
			name := claims.Name
			if name == "" {
				name = claims.Profile.Name
			}
			identity = Identity{Account: b.Tokens.Account, User: user, Email: email, Plan: claims.Auth.Plan, DisplayName: name}
		} else if identity.User != user {
			return b, identity, Invalid()
		}
	}
	return b, identity, nil
}

func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// Native account/read can succeed after a failed refresh. Require the native
// file's independently changed refresh timestamp and token material as well.
func Refreshed(before, after []byte) error {
	a, ai, err := Parse(before)
	if err != nil {
		return err
	}
	b, bi, err := Parse(after)
	if err != nil {
		return err
	}
	if ai.Account != bi.Account || ai.User != bi.User || !b.LastRefresh.After(a.LastRefresh) || bytes.Equal(before, after) || (a.Tokens.Access == b.Tokens.Access && a.Tokens.Refresh == b.Tokens.Refresh) {
		return Invalid()
	}
	return nil
}
