// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// MaxClaudeUsageEnvelope bounds the vault object for one Claude usage login.
const MaxClaudeUsageEnvelope = 16 << 10

const (
	claudeUsageEnvelopeVersion = 1
	claudeUsageEnvelopeKind    = "claude-usage"
	// ClaudeUsageClientID is the pinned first-party client the original login
	// binds. The original CLI alone performs every exchange with it.
	ClaudeUsageClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// ClaudeUsageRequiredScopes must all be granted for the usage read.
var ClaudeUsageRequiredScopes = []string{"user:inference", "user:profile"}

var claudeUsageAllowedScopes = []string{"org:create_api_key", "user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}

var (
	claudeUsageAccessToken  = regexp.MustCompile(`^sk-ant-oat01-[A-Za-z0-9_-]{8,1000}$`)
	claudeUsageRefreshToken = regexp.MustCompile(`^sk-ant-ort01-[A-Za-z0-9_-]{8,1000}$`)
)

// ClaudeUsageBundle is the decoded OAuth bundle of the usage login. Callers
// must call Clear after the bundle has been used.
type ClaudeUsageBundle struct {
	AccessToken  []byte
	RefreshToken []byte
	ExpiresAt    time.Time
	Scopes       []string
	ClientID     string
}

// Clear overwrites the decoded token bytes.
func (b *ClaudeUsageBundle) Clear() {
	clear(b.AccessToken)
	clear(b.RefreshToken)
	b.AccessToken, b.RefreshToken = nil, nil
}

// Expiring reports whether the access token is inside the renewal margin.
func (b ClaudeUsageBundle) Expiring(now time.Time, margin time.Duration) bool {
	return !now.Add(margin).Before(b.ExpiresAt)
}

// ClaudeUsageInvalid is the secret-free failure for every malformed, foreign or
// rebound usage credential.
func ClaudeUsageInvalid() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The Claude usage credential evidence is invalid or incomplete.", "Reconnect the usage account; never import another profile's credential.")
}

// Validate applies the bounded native rules.
func (b ClaudeUsageBundle) Validate(now time.Time) error {
	if !claudeUsageAccessToken.Match(b.AccessToken) || !claudeUsageRefreshToken.Match(b.RefreshToken) {
		return ClaudeUsageInvalid()
	}
	if b.ExpiresAt.Unix() <= 1_500_000_000 || b.ExpiresAt.After(now.AddDate(1, 0, 0)) {
		return ClaudeUsageInvalid()
	}
	if b.ClientID != ClaudeUsageClientID || len(b.Scopes) == 0 || len(b.Scopes) > len(claudeUsageAllowedScopes) {
		return ClaudeUsageInvalid()
	}
	seen := map[string]bool{}
	for _, scope := range b.Scopes {
		if !slices.Contains(claudeUsageAllowedScopes, scope) || seen[scope] {
			return ClaudeUsageInvalid()
		}
		seen[scope] = true
	}
	for _, required := range ClaudeUsageRequiredScopes {
		if !seen[required] {
			return ClaudeUsageInvalid()
		}
	}
	return nil
}

// ParseClaudeUsageNative decodes the native `claudeAiOauth` credential record.
// The record is the content of the profile-owned credential file or Keychain
// item; unrelated top-level entries are ignored and never retained. The pinned
// client binding is attached because the original login URL was validated
// against it.
func ParseClaudeUsageNative(raw []byte, now time.Time) (ClaudeUsageBundle, error) {
	var top map[string]json.RawMessage
	if len(raw) == 0 || len(raw) > MaxClaudeUsageEnvelope*4 || json.Unmarshal(raw, &top) != nil {
		return ClaudeUsageBundle{}, ClaudeUsageInvalid()
	}
	oauth, present := top["claudeAiOauth"]
	if !present {
		return ClaudeUsageBundle{}, ClaudeUsageInvalid()
	}
	var native struct {
		AccessToken  string   `json:"accessToken"`
		RefreshToken string   `json:"refreshToken"`
		ExpiresAt    *int64   `json:"expiresAt"`
		Scopes       []string `json:"scopes"`
	}
	if json.Unmarshal(oauth, &native) != nil || native.ExpiresAt == nil {
		return ClaudeUsageBundle{}, ClaudeUsageInvalid()
	}
	bundle := ClaudeUsageBundle{AccessToken: []byte(native.AccessToken), RefreshToken: []byte(native.RefreshToken), ExpiresAt: time.UnixMilli(*native.ExpiresAt).UTC(), Scopes: native.Scopes, ClientID: ClaudeUsageClientID}
	if err := bundle.Validate(now); err != nil {
		bundle.Clear()
		return ClaudeUsageBundle{}, err
	}
	return bundle, nil
}

type claudeUsageEnvelope struct {
	Version      int       `json:"version"`
	Kind         string    `json:"kind"`
	Server       domain.ID `json:"server"`
	Account      domain.ID `json:"account"`
	Generation   domain.ID `json:"generation"`
	Credential   domain.ID `json:"credential"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes"`
	ClientID     string    `json:"client_id"`
}

// SealClaudeUsage builds the distinct, versioned envelope stored under the
// claude-usage vault purpose, bound to server, account, usage generation and
// the immutable credential reference. The caller owns both buffers.
func SealClaudeUsage(server, account, generation, credential domain.ID, bundle ClaudeUsageBundle, now time.Time) ([]byte, error) {
	for _, id := range []domain.ID{server, account, generation, credential} {
		if id.Validate() != nil {
			return nil, ClaudeUsageInvalid()
		}
	}
	if err := bundle.Validate(now); err != nil {
		return nil, err
	}
	envelope := claudeUsageEnvelope{Version: claudeUsageEnvelopeVersion, Kind: claudeUsageEnvelopeKind, Server: server, Account: account, Generation: generation, Credential: credential, AccessToken: string(bundle.AccessToken), RefreshToken: string(bundle.RefreshToken), ExpiresAt: bundle.ExpiresAt.UTC(), Scopes: bundle.Scopes, ClientID: bundle.ClientID}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > MaxClaudeUsageEnvelope {
		clear(raw)
		return nil, ClaudeUsageInvalid()
	}
	return raw, nil
}

// OpenClaudeUsage validates the closed envelope and its exact ownership binding.
// An execution token, another account, server, generation or reference fails.
func OpenClaudeUsage(raw []byte, server, account, generation, credential domain.ID, now time.Time) (ClaudeUsageBundle, error) {
	var envelope claudeUsageEnvelope
	if len(raw) == 0 || len(raw) > MaxClaudeUsageEnvelope || domain.DecodeWithLimit(raw, &envelope, MaxClaudeUsageEnvelope) != nil {
		return ClaudeUsageBundle{}, ClaudeUsageInvalid()
	}
	if envelope.Version != claudeUsageEnvelopeVersion || envelope.Kind != claudeUsageEnvelopeKind || envelope.Server != server || envelope.Account != account || envelope.Generation != generation || envelope.Credential != credential {
		return ClaudeUsageBundle{}, ClaudeUsageInvalid()
	}
	bundle := ClaudeUsageBundle{AccessToken: []byte(envelope.AccessToken), RefreshToken: []byte(envelope.RefreshToken), ExpiresAt: envelope.ExpiresAt.UTC(), Scopes: envelope.Scopes, ClientID: envelope.ClientID}
	if err := bundle.Validate(now); err != nil {
		bundle.Clear()
		return ClaudeUsageBundle{}, err
	}
	return bundle, nil
}

// ClaudeUsageEnvelope reports whether raw carries the usage discriminator
// without decoding or validating its secrets.
func ClaudeUsageEnvelope(raw []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return len(raw) <= MaxClaudeUsageEnvelope && json.Unmarshal(raw, &probe) == nil && probe.Kind == claudeUsageEnvelopeKind
}

// SameClaudeUsageTokens reports whether two bundles carry identical tokens.
func SameClaudeUsageTokens(a, b ClaudeUsageBundle) bool {
	return bytes.Equal(a.AccessToken, b.AccessToken) && bytes.Equal(a.RefreshToken, b.RefreshToken)
}
