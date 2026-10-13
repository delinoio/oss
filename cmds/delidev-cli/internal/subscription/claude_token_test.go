// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const syntheticClaudeToken = "sk-ant-oat01-Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDEFG"

func TestClaudeTokenEnvelopeBindsServerAccountAndGeneration(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	raw, err := SealClaudeToken(server, account, generation, []byte(syntheticClaudeToken))
	if err != nil || !ClaudeTokenEnvelope(raw) {
		t.Fatalf("seal failed: %v", err)
	}
	opened, err := OpenClaudeToken(raw, server, account, generation)
	if err != nil || string(opened.Token) != syntheticClaudeToken {
		t.Fatalf("open failed: %v", err)
	}
	opened.Clear()
	if opened.Token != nil {
		t.Fatal("token was retained after clear")
	}
	for name, ids := range map[string][3]domain.ID{
		"server":     {domain.NewID(), account, generation},
		"account":    {server, domain.NewID(), generation},
		"generation": {server, account, domain.NewID()},
	} {
		if _, err := OpenClaudeToken(raw, ids[0], ids[1], ids[2]); err == nil {
			t.Fatalf("rebound %s was accepted", name)
		}
	}
}

func TestClaudeTokenEnvelopeRejectsMalformedMaterial(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	for _, token := range []string{"", "short", "sk-ant-api03-Abcdefghijklmnopqrstuvwxyz0123456789", syntheticClaudeToken + " ", syntheticClaudeToken + "\n", strings.Repeat("a", 600)} {
		if _, err := SealClaudeToken(server, account, generation, []byte(token)); err == nil {
			t.Fatalf("seal accepted %q", token)
		}
	}
	raw, err := SealClaudeToken(server, account, generation, []byte(syntheticClaudeToken))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"empty":     nil,
		"codex":     []byte(`{"tokens":{}}`),
		"unknown":   bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"extra":true`), 1),
		"version":   bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1),
		"kind":      bytes.Replace(raw, []byte(claudeTokenEnvelopeKind), []byte("claude-api-key"), 1),
		"duplicate": bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"oversized": bytes.Repeat([]byte("x"), MaxClaudeTokenEnvelope+1),
	} {
		if _, err := OpenClaudeToken(value, server, account, generation); err == nil {
			t.Fatalf("%s envelope was accepted", name)
		}
	}
	if ClaudeTokenEnvelope([]byte(`{"tokens":{}}`)) {
		t.Fatal("a Codex bundle was classified as a Claude envelope")
	}
}

func TestClaudeTokenErrorsNeverContainTheSecret(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	_, err := OpenClaudeToken([]byte(`{"version":1,"kind":"claude-setup-token","token":"`+syntheticClaudeToken+`"}`), server, account, generation)
	if err == nil || strings.Contains(err.Error(), syntheticClaudeToken) || strings.Contains(domain.SafeError(err).Message, "sk-ant") {
		t.Fatal("failure exposed token material")
	}
}
