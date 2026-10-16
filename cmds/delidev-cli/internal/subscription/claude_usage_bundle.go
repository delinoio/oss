// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// MaxClaudeUsageBundleEnvelope bounds the vault object for one usage bundle.
const MaxClaudeUsageBundleEnvelope = 16 << 10

const (
	claudeUsageBundleVersion = 1
	claudeUsageBundleKind    = "claude-usage-oauth"
	maxClaudeUsageTokenBytes = 4 << 10
	maxClaudeUsageScopes     = 16
)

var (
	claudeUsageScopePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	claudeUsageClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// ClaudeUsageBundle is the decoded usage-only OAuth credential owned by exactly
// one server, account, usage generation and reviewed identity. It can fetch
// usage and nothing else: it is never delivered to a Worker, returned over RPC,
// exported, backed up or logged. Callers must call Clear after use.
type ClaudeUsageBundle struct {
	AccessToken  []byte
	RefreshToken []byte
	ExpiresAt    time.Time
	Scopes       []string
	// ClientID is the optional OAuth client binding of the bundle.
	ClientID string
}

// Clear overwrites the decoded token bytes.
func (b *ClaudeUsageBundle) Clear() {
	clear(b.AccessToken)
	clear(b.RefreshToken)
	b.AccessToken, b.RefreshToken = nil, nil
}

// ClaudeUsageBundleInvalid is the secret-free failure for every malformed,
// foreign or rebound envelope.
func ClaudeUsageBundleInvalid() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The Claude usage credential evidence is invalid or incomplete.", "Retain exclusive ownership and reconcile the original usage operation; never import or redistribute another credential.")
}

type claudeUsageBundleEnvelope struct {
	Version      int             `json:"version"`
	Kind         string          `json:"kind"`
	Server       domain.ID       `json:"server"`
	Account      domain.ID       `json:"account"`
	Generation   domain.ID       `json:"generation"`
	Identity     string          `json:"identity"`
	AccessToken  json.RawMessage `json:"access_token"`
	RefreshToken json.RawMessage `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at"`
	Scopes       []string        `json:"scopes"`
	ClientID     string          `json:"client_id,omitempty"`
}

// claudeUsageTokenValid reports whether value has the closed shape of one OAuth
// token. It validates syntax only and never proves the provider accepts it.
func claudeUsageTokenValid(value []byte) bool {
	if len(value) < 16 || len(value) > maxClaudeUsageTokenBytes {
		return false
	}
	for _, b := range value {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' || b == '+' || b == '/' || b == '=') {
			return false
		}
	}
	return true
}

func claudeUsageBundleShapeValid(identity string, b ClaudeUsageBundle) bool {
	if !domain.NativeIdentityCommitmentValid(identity) || !claudeUsageTokenValid(b.AccessToken) || len(b.RefreshToken) != 0 && !claudeUsageTokenValid(b.RefreshToken) {
		return false
	}
	if b.ExpiresAt.IsZero() || b.ExpiresAt.Unix() <= 0 || b.ExpiresAt.Year() > 9999 || len(b.Scopes) == 0 || len(b.Scopes) > maxClaudeUsageScopes {
		return false
	}
	seen := map[string]bool{}
	for _, scope := range b.Scopes {
		if !claudeUsageScopePattern.MatchString(scope) || seen[scope] {
			return false
		}
		seen[scope] = true
	}
	return b.ClientID == "" || claudeUsageClientIDPattern.MatchString(b.ClientID)
}

func quoteClaudeUsageToken(token []byte) []byte {
	quoted := make([]byte, 0, len(token)+2)
	quoted = append(quoted, '"')
	quoted = append(quoted, token...)
	return append(quoted, '"')
}

func unquoteClaudeUsageToken(quoted json.RawMessage) ([]byte, bool) {
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		return nil, false
	}
	return append([]byte(nil), quoted[1:len(quoted)-1]...), true
}

// SealClaudeUsageBundle builds the distinct, versioned envelope stored under the
// claude-usage vault purpose. It binds the bundle to the server, account, usage
// generation and the reviewed identity commitment. The caller owns the buffers.
func SealClaudeUsageBundle(server, account, generation domain.ID, identity string, bundle ClaudeUsageBundle) ([]byte, error) {
	for _, id := range []domain.ID{server, account, generation} {
		if id.Validate() != nil {
			return nil, ClaudeUsageBundleInvalid()
		}
	}
	if !claudeUsageBundleShapeValid(identity, bundle) {
		return nil, ClaudeUsageBundleInvalid()
	}
	envelope := claudeUsageBundleEnvelope{Version: claudeUsageBundleVersion, Kind: claudeUsageBundleKind, Server: server, Account: account, Generation: generation, Identity: identity, ExpiresAt: bundle.ExpiresAt.UTC(), Scopes: slices.Clone(bundle.Scopes), ClientID: bundle.ClientID}
	envelope.AccessToken = quoteClaudeUsageToken(bundle.AccessToken)
	defer clear(envelope.AccessToken)
	if len(bundle.RefreshToken) != 0 {
		envelope.RefreshToken = quoteClaudeUsageToken(bundle.RefreshToken)
		defer clear(envelope.RefreshToken)
	}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > MaxClaudeUsageBundleEnvelope {
		clear(raw)
		return nil, ClaudeUsageBundleInvalid()
	}
	return raw, nil
}

// OpenClaudeUsageBundle validates the closed envelope and its exact ownership
// binding. A setup-token envelope, another account, server, generation or
// identity commitment fails.
func OpenClaudeUsageBundle(raw []byte, server, account, generation domain.ID, identity string) (ClaudeUsageBundle, error) {
	var envelope claudeUsageBundleEnvelope
	if len(raw) == 0 || len(raw) > MaxClaudeUsageBundleEnvelope || domain.DecodeWithLimit(raw, &envelope, MaxClaudeUsageBundleEnvelope) != nil {
		return ClaudeUsageBundle{}, ClaudeUsageBundleInvalid()
	}
	defer clear(envelope.AccessToken)
	defer clear(envelope.RefreshToken)
	if envelope.Version != claudeUsageBundleVersion || envelope.Kind != claudeUsageBundleKind || envelope.Server != server || envelope.Account != account || envelope.Generation != generation || envelope.Identity != identity {
		return ClaudeUsageBundle{}, ClaudeUsageBundleInvalid()
	}
	access, ok := unquoteClaudeUsageToken(envelope.AccessToken)
	if !ok {
		return ClaudeUsageBundle{}, ClaudeUsageBundleInvalid()
	}
	bundle := ClaudeUsageBundle{AccessToken: access, ExpiresAt: envelope.ExpiresAt.UTC(), Scopes: envelope.Scopes, ClientID: envelope.ClientID}
	if len(envelope.RefreshToken) != 0 {
		refresh, ok := unquoteClaudeUsageToken(envelope.RefreshToken)
		if !ok {
			bundle.Clear()
			return ClaudeUsageBundle{}, ClaudeUsageBundleInvalid()
		}
		bundle.RefreshToken = refresh
	}
	if !claudeUsageBundleShapeValid(identity, bundle) {
		bundle.Clear()
		return ClaudeUsageBundle{}, ClaudeUsageBundleInvalid()
	}
	return bundle, nil
}

// ClaudeUsageBundleEnvelope reports whether raw has the usage envelope
// discriminator without decoding or validating its secrets.
func ClaudeUsageBundleEnvelope(raw []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return len(raw) <= MaxClaudeUsageBundleEnvelope && json.Unmarshal(raw, &probe) == nil && probe.Kind == claudeUsageBundleKind
}
