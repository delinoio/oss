// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	syntheticUsageAccess   = "usage-access-Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDEFG"
	syntheticUsageRefresh  = "usage-refresh-Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDEFG"
	syntheticUsageIdentity = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func syntheticUsageBundle() ClaudeUsageBundle {
	return ClaudeUsageBundle{AccessToken: []byte(syntheticUsageAccess), RefreshToken: []byte(syntheticUsageRefresh), ExpiresAt: time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC), Scopes: []string{"user:profile", "user:inference"}, ClientID: "client-1.example"}
}

func TestClaudeUsageBundleEnvelopeBindsServerAccountGenerationAndIdentity(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	raw, err := SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, syntheticUsageBundle())
	if err != nil || !ClaudeUsageBundleEnvelope(raw) || ClaudeTokenEnvelope(raw) {
		t.Fatalf("seal failed: %v", err)
	}
	opened, err := OpenClaudeUsageBundle(raw, server, account, generation, syntheticUsageIdentity)
	if err != nil || string(opened.AccessToken) != syntheticUsageAccess || string(opened.RefreshToken) != syntheticUsageRefresh || !opened.ExpiresAt.Equal(syntheticUsageBundle().ExpiresAt) || strings.Join(opened.Scopes, ",") != "user:profile,user:inference" || opened.ClientID != "client-1.example" {
		t.Fatalf("open failed: %v", err)
	}
	access, refresh := opened.AccessToken, opened.RefreshToken
	opened.Clear()
	if opened.AccessToken != nil || opened.RefreshToken != nil || !bytes.Equal(access, make([]byte, len(access))) || !bytes.Equal(refresh, make([]byte, len(refresh))) {
		t.Fatal("tokens were retained after clear")
	}
	other := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	for name, ids := range map[string][4]any{
		"server":     {domain.NewID(), account, generation, syntheticUsageIdentity},
		"account":    {server, domain.NewID(), generation, syntheticUsageIdentity},
		"generation": {server, account, domain.NewID(), syntheticUsageIdentity},
		"identity":   {server, account, generation, other},
	} {
		if bundle, err := OpenClaudeUsageBundle(raw, ids[0].(domain.ID), ids[1].(domain.ID), ids[2].(domain.ID), ids[3].(string)); err == nil || bundle.AccessToken != nil {
			t.Fatalf("rebound %s was accepted", name)
		}
	}
}

func TestClaudeUsageBundleAllowsAccessOnlyBundle(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	bundle := syntheticUsageBundle()
	bundle.RefreshToken, bundle.ClientID = nil, ""
	raw, err := SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, bundle)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenClaudeUsageBundle(raw, server, account, generation, syntheticUsageIdentity)
	if err != nil || len(opened.RefreshToken) != 0 || opened.ClientID != "" {
		t.Fatalf("access-only bundle: %v", err)
	}
	opened.Clear()
}

func TestClaudeUsageBundleRejectsMalformedMaterial(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	mutate := func(edit func(*ClaudeUsageBundle)) ClaudeUsageBundle {
		b := syntheticUsageBundle()
		edit(&b)
		return b
	}
	for name, bundle := range map[string]ClaudeUsageBundle{
		"empty access":       mutate(func(b *ClaudeUsageBundle) { b.AccessToken = nil }),
		"short access":       mutate(func(b *ClaudeUsageBundle) { b.AccessToken = []byte("short") }),
		"quote in access":    mutate(func(b *ClaudeUsageBundle) { b.AccessToken = []byte(syntheticUsageAccess + `"`) }),
		"space in access":    mutate(func(b *ClaudeUsageBundle) { b.AccessToken = []byte(syntheticUsageAccess + " ") }),
		"newline in refresh": mutate(func(b *ClaudeUsageBundle) { b.RefreshToken = []byte(syntheticUsageRefresh + "\n") }),
		"oversized access":   mutate(func(b *ClaudeUsageBundle) { b.AccessToken = bytes.Repeat([]byte("a"), maxClaudeUsageTokenBytes+1) }),
		"zero expiry":        mutate(func(b *ClaudeUsageBundle) { b.ExpiresAt = time.Time{} }),
		"no scopes":          mutate(func(b *ClaudeUsageBundle) { b.Scopes = nil }),
		"duplicate scope":    mutate(func(b *ClaudeUsageBundle) { b.Scopes = []string{"user:profile", "user:profile"} }),
		"bad scope":          mutate(func(b *ClaudeUsageBundle) { b.Scopes = []string{"user profile"} }),
		"too many scopes": mutate(func(b *ClaudeUsageBundle) {
			b.Scopes = nil
			for i := range maxClaudeUsageScopes + 1 {
				b.Scopes = append(b.Scopes, "scope"+string(rune('a'+i)))
			}
		}),
		"bad client": mutate(func(b *ClaudeUsageBundle) { b.ClientID = "client id" }),
	} {
		if _, err := SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, bundle); err == nil {
			t.Fatalf("seal accepted %s", name)
		}
	}
	if _, err := SealClaudeUsageBundle(server, account, generation, "ABC", syntheticUsageBundle()); err == nil {
		t.Fatal("seal accepted a malformed identity commitment")
	}
	if _, err := SealClaudeUsageBundle(server, account, "not-an-id", syntheticUsageIdentity, syntheticUsageBundle()); err == nil {
		t.Fatal("seal accepted a malformed generation")
	}
	raw, err := SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, syntheticUsageBundle())
	if err != nil {
		t.Fatal(err)
	}
	setupToken, err := SealClaudeToken(server, account, generation, []byte(syntheticClaudeToken))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"empty":        nil,
		"setup token":  setupToken,
		"codex":        []byte(`{"tokens":{}}`),
		"unknown":      bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"extra":true`), 1),
		"version":      bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1),
		"kind":         bytes.Replace(raw, []byte(claudeUsageBundleKind), []byte(claudeTokenEnvelopeKind), 1),
		"duplicate":    bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"oversized":    bytes.Repeat([]byte("x"), MaxClaudeUsageBundleEnvelope+1),
		"unquoted":     bytes.Replace(raw, []byte(`"`+syntheticUsageAccess+`"`), []byte(syntheticUsageAccess), 1),
		"token type":   bytes.Replace(raw, []byte(`"`+syntheticUsageAccess+`"`), []byte(`1`), 1),
		"scope change": bytes.Replace(raw, []byte(`user:profile`), []byte(`user profile`), 1),
	} {
		if bundle, err := OpenClaudeUsageBundle(value, server, account, generation, syntheticUsageIdentity); err == nil || bundle.AccessToken != nil {
			t.Fatalf("%s envelope was accepted", name)
		}
	}
	if ClaudeUsageBundleEnvelope([]byte(`{"tokens":{}}`)) || ClaudeUsageBundleEnvelope(setupToken) {
		t.Fatal("another envelope was classified as a usage bundle")
	}
	if _, err := OpenClaudeToken(raw, server, account, generation); err == nil {
		t.Fatal("a usage bundle was readable as a setup token")
	}
}

func TestClaudeUsageBundleErrorsNeverContainSecrets(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	raw, err := SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, syntheticUsageBundle())
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenClaudeUsageBundle(raw, server, account, domain.NewID(), syntheticUsageIdentity)
	if err == nil || strings.Contains(err.Error(), syntheticUsageAccess) || strings.Contains(err.Error(), syntheticUsageRefresh) || strings.Contains(domain.SafeError(err).Message, "usage-access") {
		t.Fatal("failure exposed credential material")
	}
	_, err = SealClaudeUsageBundle(server, account, generation, syntheticUsageIdentity, ClaudeUsageBundle{AccessToken: []byte(syntheticUsageAccess + " ")})
	if err == nil || strings.Contains(err.Error(), syntheticUsageAccess) {
		t.Fatal("seal failure exposed credential material")
	}
}
