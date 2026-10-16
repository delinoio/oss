// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func usageNative(access, refresh string, expires int64, scopes ...string) []byte {
	raw, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": expires, "scopes": scopes, "subscriptionType": "max"}, "mcpOAuth": map[string]any{"x": 1}})
	return raw
}

func validUsageNative(now time.Time) []byte {
	return usageNative("sk-ant-oat01-"+strings.Repeat("a", 40), "sk-ant-ort01-"+strings.Repeat("b", 40), now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile", "user:sessions:claude_code")
}

func TestParseClaudeUsageNativeAcceptsBoundedBundleAndIgnoresUnrelatedEntries(t *testing.T) {
	now := time.Now().UTC()
	bundle, err := ParseClaudeUsageNative(validUsageNative(now), now)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Clear()
	if bundle.ClientID != ClaudeUsageClientID || len(bundle.Scopes) != 3 || !bundle.ExpiresAt.After(now) {
		t.Fatalf("unexpected bundle %+v", bundle)
	}
	if bundle.Expiring(now, time.Minute) || !bundle.Expiring(now, 2*time.Hour) {
		t.Fatal("renewal margin comparison is wrong")
	}
}

func TestParseClaudeUsageNativeFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	access := "sk-ant-oat01-" + strings.Repeat("a", 40)
	refresh := "sk-ant-ort01-" + strings.Repeat("b", 40)
	cases := map[string][]byte{
		"empty":               nil,
		"not json":            []byte("{"),
		"no oauth record":     []byte(`{"mcpOAuth":{}}`),
		"setup token shape":   usageNative("sk-ant-oat01-"+strings.Repeat("a", 40), "setup", now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile"),
		"api key shape":       usageNative("sk-ant-api03-"+strings.Repeat("a", 40), refresh, now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile"),
		"missing expiry":      []byte(`{"claudeAiOauth":{"accessToken":"` + access + `","refreshToken":"` + refresh + `","scopes":["user:inference","user:profile"]}}`),
		"ancient expiry":      usageNative(access, refresh, 1000, "user:inference", "user:profile"),
		"far future expiry":   usageNative(access, refresh, now.AddDate(5, 0, 0).UnixMilli(), "user:inference", "user:profile"),
		"missing profile":     usageNative(access, refresh, now.Add(time.Hour).UnixMilli(), "user:inference"),
		"missing inference":   usageNative(access, refresh, now.Add(time.Hour).UnixMilli(), "user:profile"),
		"unknown scope":       usageNative(access, refresh, now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile", "admin:everything"),
		"duplicate scope":     usageNative(access, refresh, now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile", "user:profile"),
		"newline in token":    usageNative(access+"\n", refresh, now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile"),
		"oversized":           []byte(`{"claudeAiOauth":{"accessToken":"` + strings.Repeat("a", 100000) + `"}}`),
		"swapped token roles": usageNative(refresh, access, now.Add(time.Hour).UnixMilli(), "user:inference", "user:profile"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseClaudeUsageNative(raw, now); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("expected closed failure, got %v", err)
			}
		})
	}
}

func TestClaudeUsageEnvelopeRoundTripAndBinding(t *testing.T) {
	now := time.Now().UTC()
	server, account, generation, credential := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	bundle, err := ParseClaudeUsageNative(validUsageNative(now), now)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Clear()
	sealed, err := SealClaudeUsage(server, account, generation, credential, bundle, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ClaudeUsageEnvelope(sealed) || ClaudeTokenEnvelope(sealed) {
		t.Fatal("the usage envelope has its own discriminator")
	}
	opened, err := OpenClaudeUsage(sealed, server, account, generation, credential, now)
	if err != nil {
		t.Fatal(err)
	}
	if !SameClaudeUsageTokens(bundle, opened) || !opened.ExpiresAt.Equal(bundle.ExpiresAt.Truncate(time.Nanosecond)) {
		t.Fatalf("round trip changed the bundle")
	}
	opened.Clear()
	for name, bind := range map[string][4]domain.ID{
		"other server":     {domain.NewID(), account, generation, credential},
		"other account":    {server, domain.NewID(), generation, credential},
		"other generation": {server, account, domain.NewID(), credential},
		"other credential": {server, account, generation, domain.NewID()},
	} {
		if _, err := OpenClaudeUsage(sealed, bind[0], bind[1], bind[2], bind[3], now); err == nil {
			t.Fatalf("%s must not open the envelope", name)
		}
	}
	token, err := SealClaudeToken(server, account, generation, []byte("sk-ant-oat01-"+strings.Repeat("c", 90)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenClaudeUsage(token, server, account, generation, credential, now); err == nil {
		t.Fatal("an execution setup-token envelope must never open as a usage credential")
	}
	if _, err := OpenClaudeToken(sealed, server, account, generation); err == nil {
		t.Fatal("a usage envelope must never open as an execution token")
	}
	if strings.Contains(string(sealed), "mcpOAuth") {
		t.Fatal("unrelated native entries must not be retained")
	}
}
