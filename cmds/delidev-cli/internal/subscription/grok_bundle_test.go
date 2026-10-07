// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func grokBundleFixture(t *testing.T) []byte {
	t.Helper()
	created := time.Now().UTC().Add(-time.Minute)
	raw, err := json.Marshal(map[string]GrokAuth{GrokScope: {
		Key: "fixture-access", Refresh: "fixture-refresh", Mode: GrokOIDC,
		Created: created, Expires: created.Add(time.Hour), User: "fixture-user",
		Issuer: GrokIssuer, ClientID: GrokClientID, RetentionOptOut: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGrokBundlePinsScopeAndCompleteIdentity(t *testing.T) {
	raw := grokBundleFixture(t)
	_, id, err := ParseGrok(raw)
	if err != nil || id.Service != domain.SubscriptionGrok || id.Issuer != GrokIssuer || id.User != "fixture-user" || id.PrincipalType != "User" || id.PrincipalID != id.User {
		t.Fatalf("invalid pinned identity: %v", err)
	}
	for _, service := range []domain.SubscriptionService{domain.SubscriptionChatGPT, domain.SubscriptionClaude, "unknown"} {
		if _, err := ParseService(service, raw); err == nil {
			t.Fatal("a different service accepted Grok material")
		}
	}
	var b map[string]GrokAuth
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("fixture")
	}
	for _, mutate := range []func(map[string]GrokAuth){
		func(v map[string]GrokAuth) { v["xai::api_key"] = v[GrokScope] },
		func(v map[string]GrokAuth) { v["https://accounts.x.ai/sign-in"] = v[GrokScope]; delete(v, GrokScope) },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.Mode = "api_key"; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.Issuer = "https://foreign.invalid"; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.ClientID = "different-client"; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.Key = ""; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.Refresh = "two tokens"; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.User = ""; v[GrokScope] = a },
		func(v map[string]GrokAuth) { a := v[GrokScope]; p := "User"; a.PrincipalType = &p; v[GrokScope] = a },
		func(v map[string]GrokAuth) {
			a := v[GrokScope]
			p, u := "User", "different-user"
			a.PrincipalType = &p
			a.PrincipalID = &u
			v[GrokScope] = a
		},
		func(v map[string]GrokAuth) {
			a := v[GrokScope]
			p, u := "Team", "team"
			a.PrincipalType = &p
			a.PrincipalID = &u
			v[GrokScope] = a
		},
		func(v map[string]GrokAuth) {
			a := v[GrokScope]
			a.Created = time.Now().Add(2 * time.Minute)
			v[GrokScope] = a
		},
		func(v map[string]GrokAuth) { a := v[GrokScope]; a.Expires = a.Created; v[GrokScope] = a },
	} {
		copy := map[string]GrokAuth{GrokScope: b[GrokScope]}
		mutate(copy)
		data, _ := json.Marshal(copy)
		if _, _, err := ParseGrok(data); err == nil {
			t.Fatal("foreign or incomplete bundle acquired authority")
		}
	}
	for _, data := range [][]byte{nil, bytes.Repeat([]byte("x"), MaxBundle+1), bytes.Replace(raw, []byte(`"key":`), []byte(`"key":"duplicate","key":`), 1), bytes.Replace(raw, []byte(`"user_id":`), []byte(`"unexpected":true,"user_id":`), 1)} {
		if _, _, err := ParseGrok(data); err == nil {
			t.Fatal("malformed bundle was accepted")
		}
	}
}

func TestGrokRefreshRequiresSameIdentityAndNewIssuance(t *testing.T) {
	before := grokBundleFixture(t)
	var store map[string]GrokAuth
	_ = json.Unmarshal(before, &store)
	a := store[GrokScope]
	a.Key = "new-fixture-access"
	a.Refresh = "new-fixture-refresh"
	a.Created = a.Created.Add(30 * time.Second)
	a.Expires = a.Expires.Add(30 * time.Second)
	store[GrokScope] = a
	after, _ := json.Marshal(store)
	if err := RefreshedService(domain.SubscriptionGrok, before, after); err != nil {
		t.Fatal(err)
	}
	if err := RefreshedService(domain.SubscriptionGrok, before, before); err == nil {
		t.Fatal("unchanged file proved refresh")
	}
	a.User = "other-user"
	store[GrokScope] = a
	after, _ = json.Marshal(store)
	if err := RefreshedService(domain.SubscriptionGrok, before, after); err == nil {
		t.Fatal("foreign identity replaced the original generation")
	}
	want := []byte(`{"Account":"original","User":"user"}`)
	if !bytes.Equal(CommitmentInput(Identity{Account: "original", User: "user"}), want) {
		t.Fatal("historical Codex commitment bytes changed")
	}
}
