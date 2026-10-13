// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// MaxClaudeTokenEnvelope bounds the vault object for one managed Claude token.
const MaxClaudeTokenEnvelope = 4 << 10

const (
	claudeTokenEnvelopeVersion = 1
	claudeTokenEnvelopeKind    = "claude-setup-token"
)

// ClaudeToken is the decoded token owned by exactly one server, account and
// generation. Callers must call Clear after the token has been used.
type ClaudeToken struct {
	Token []byte
}

// Clear overwrites the decoded token bytes.
func (t *ClaudeToken) Clear() {
	clear(t.Token)
	t.Token = nil
}

// ClaudeTokenInvalid is the secret-free failure for every malformed, foreign or
// rebound envelope.
func ClaudeTokenInvalid() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed Claude token evidence is invalid or incomplete.", "Retain exclusive ownership and reconcile the original operation; never import or redistribute another token.")
}

type claudeTokenEnvelope struct {
	Version    int             `json:"version"`
	Kind       string          `json:"kind"`
	Server     domain.ID       `json:"server"`
	Account    domain.ID       `json:"account"`
	Generation domain.ID       `json:"generation"`
	Token      json.RawMessage `json:"token"`
}

// SealClaudeToken builds the distinct, versioned envelope that is stored under
// the existing AccountLogin vault ownership. The caller owns both buffers.
func SealClaudeToken(server, account, generation domain.ID, token []byte) ([]byte, error) {
	for _, id := range []domain.ID{server, account, generation} {
		if id.Validate() != nil {
			return nil, ClaudeTokenInvalid()
		}
	}
	if !domain.ClaudeSetupTokenValid(token) {
		return nil, ClaudeTokenInvalid()
	}
	quoted := make([]byte, 0, len(token)+2)
	quoted = append(quoted, '"')
	quoted = append(quoted, token...)
	quoted = append(quoted, '"')
	defer clear(quoted)
	envelope := claudeTokenEnvelope{Version: claudeTokenEnvelopeVersion, Kind: claudeTokenEnvelopeKind, Server: server, Account: account, Generation: generation, Token: quoted}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > MaxClaudeTokenEnvelope {
		clear(raw)
		return nil, ClaudeTokenInvalid()
	}
	return raw, nil
}

// OpenClaudeToken validates the closed envelope and its exact ownership binding.
// A Codex bundle, another account, another server or another generation fails.
func OpenClaudeToken(raw []byte, server, account, generation domain.ID) (ClaudeToken, error) {
	var envelope claudeTokenEnvelope
	if len(raw) == 0 || len(raw) > MaxClaudeTokenEnvelope || domain.DecodeWithLimit(raw, &envelope, MaxClaudeTokenEnvelope) != nil {
		return ClaudeToken{}, ClaudeTokenInvalid()
	}
	defer clear(envelope.Token)
	if envelope.Version != claudeTokenEnvelopeVersion || envelope.Kind != claudeTokenEnvelopeKind || envelope.Server != server || envelope.Account != account || envelope.Generation != generation {
		return ClaudeToken{}, ClaudeTokenInvalid()
	}
	if len(envelope.Token) < 2 || envelope.Token[0] != '"' || envelope.Token[len(envelope.Token)-1] != '"' {
		return ClaudeToken{}, ClaudeTokenInvalid()
	}
	token := append([]byte(nil), envelope.Token[1:len(envelope.Token)-1]...)
	if !domain.ClaudeSetupTokenValid(token) {
		clear(token)
		return ClaudeToken{}, errors.Join(ClaudeTokenInvalid())
	}
	return ClaudeToken{Token: token}, nil
}

// ClaudeTokenEnvelope reports whether raw has the Claude envelope discriminator
// without decoding or validating its secret.
func ClaudeTokenEnvelope(raw []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return len(raw) <= MaxClaudeTokenEnvelope && json.Unmarshal(raw, &probe) == nil && probe.Kind == claudeTokenEnvelopeKind
}
