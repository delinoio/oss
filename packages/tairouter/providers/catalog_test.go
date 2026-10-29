// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogCompletenessAndCopies(t *testing.T) {
	all := Catalog()
	var pinned []json.RawMessage
	if json.Unmarshal(catalogJSON, &pinned) != nil || len(pinned) != 106 || len(all) != 107 {
		t.Fatal("pinned/additive catalog inventory changed")
	}
	if p, ok := Lookup("exa"); !ok || p.Wire != ExaSearch {
		t.Fatal("missing owned Exa extension")
	}
	ids := map[string]bool{}
	excluded := map[string]bool{}
	wires := map[Wire]bool{}
	for _, p := range all {
		if p.ID == "" || ids[p.ID] || p.Label == "" || p.BaseURL == "" || p.Wire == "" {
			t.Fatalf("invalid/duplicate catalog row %s", p.ID)
		}
		ids[p.ID] = true
		if p.ID != "exa" {
			wires[p.Wire] = true
		}
		if p.Excluded {
			excluded[p.ID] = true
		}
		if (p.Auth == OAuth || p.Auth == Forward) && len(AuthMethods(p.ID)) == 0 {
			t.Errorf("auth family missing: %s", p.ID)
		}
		var raw map[string]any
		if json.Unmarshal(p.Metadata, &raw) != nil || raw["id"] != p.ID || raw["baseUrl"] != p.BaseURL || raw["adapter"] != string(p.Wire) || raw["authKind"] != string(p.Auth) {
			t.Errorf("snapshot projection mismatch: %s", p.ID)
		}
	}
	for _, id := range []string{"claude-cli", "codebuddy", "codebuddy-cn", "qoder", "qoder-cn"} {
		if !excluded[id] {
			t.Errorf("CLI preset not excluded: %s", id)
		}
	}
	// Verified by generate.mjs executing the pinned core and extended registries.
	for _, wire := range []Wire{"anthropic", "azure-openai", "claude-cli", "codebuddy", "command-code", "cursor", "devin", "dsh-account", "google", "jev-decision", "kiro", "mimo-free", "ollama-native", "openai-chat", "openai-responses", "qoder", "workbuddy-ai", "zed"} {
		if !wires[wire] {
			t.Errorf("missing pinned wire %s", wire)
		}
	}
	if len(excluded) != 5 || len(wires) != 18 {
		t.Fatalf("excluded/wire inventory %d/%d", len(excluded), len(wires))
	}
	p, _ := Lookup("openai-apikey")
	p.Models[0] = "mutated"
	p.Metadata[0] = '!'
	next, _ := Lookup("openai-apikey")
	if next.Models[0] == "mutated" || next.Metadata[0] == '!' {
		t.Fatal("shared registry mutable")
	}
}
func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		p      string
		wire   Wire
		header string
	}{
		{"openai-apikey", Responses, "Authorization"}, {"anthropic-apikey", Anthropic, "X-Api-Key"}, {"google", Google, "X-Goog-Api-Key"},
	} {
		t.Run(tc.p, func(t *testing.T) {
			p, ok := Lookup(tc.p)
			if !ok {
				t.Fatal("missing preset")
			}
			c := Connection{Provider: tc.p, APIKey: "key"}
			r, e := Resolve(c, "model", InboundResponses)
			if e != nil {
				t.Fatal(e)
			}
			if r.Wire != tc.wire || r.BaseURL != p.BaseURL || r.Headers.Get(tc.header) == "" {
				t.Fatalf("wrong route: %+v", r)
			}
		})
	}
	c := Connection{Provider: "github-copilot", Credential: &Credential{Scope: testScope, Provider: "github-copilot", AccessToken: "copilot", APIBaseURL: "https://api.business.githubcopilot.com"}}
	r, e := Resolve(c, "gpt-5.4", InboundChat)
	if e != nil {
		t.Fatal(e)
	}
	if r.Wire != Responses || r.BaseURL != c.Credential.APIBaseURL {
		t.Fatal("model wire/account destination lost")
	}
	c.ModelWires = map[string]Wire{"gpt-5.4": Chat}
	r, e = Resolve(c, "gpt-5.4", InboundResponses)
	if e != nil || r.Wire != Chat {
		t.Fatal("explicit wire did not win")
	}
	c.Credential.APIBaseURL = "https://githubcopilot.com.evil.example"
	if _, e = Resolve(c, "gpt-5.4", InboundChat); !errors.Is(e, ErrConnection) {
		t.Fatal("unsafe account destination accepted")
	}
	custom := Connection{Provider: "custom", BaseURL: "https://example.test/v1", Wire: Responses, APIKey: "key"}
	if _, e = Resolve(custom, "model", InboundResponses); e != nil {
		t.Fatal(e)
	}
	for _, base := range []string{"https://user:password@example.test", "file:///tmp/model", "https://example.test/{account}", "https://example.test?token=secret", "https://example.test/#fragment"} {
		custom.BaseURL = base
		if e = ValidateConnection(custom); !errors.Is(e, ErrConnection) {
			t.Errorf("invalid destination accepted: %s", base)
		}
	}
	for _, p := range []string{"claude-cli", "qoder", "codebuddy", "missing"} {
		if e = ValidateConnection(Connection{Provider: p, APIKey: "key"}); !errors.Is(e, ErrProvider) {
			t.Errorf("excluded/unknown accepted: %s", p)
		}
	}
	if e = ValidateConnection(Connection{Provider: "openai-apikey", APIKey: "key", BaseURL: "https://attacker.example"}); !errors.Is(e, ErrConnection) {
		t.Fatal("preset key retargeted")
	}
}
func TestRemoteValidation(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		state  ValidationState
		bad    bool
	}{
		{200, `{"data":[{"id":"model"},{"id":"model"},{"id":"bad\nmodel"}]}`, ValidationAccepted, false},
		{401, `{"error":"SECRET"}`, ValidationRejected, false},
		{403, ``, ValidationRejected, false},
		{200, `{"not_models":[]}`, "", true},
		{500, `{"error":"SECRET"}`, "", true},
	} {
		t.Run(string(tc.state)+tc.body, func(t *testing.T) {
			a, _ := newMock(t, responseStep{"GET", "https://example.test/v1/models", tc.status, tc.body, func(t *testing.T, r *http.Request, b []byte) {
				if r.Header.Get("Authorization") != "Bearer key" {
					t.Fatal("key missing")
				}
			}})
			got, e := a.ValidateRemote(context.Background(), testScope, Connection{Provider: "custom", BaseURL: "https://example.test/v1", Wire: Chat, APIKey: "key"})
			if (e != nil) != tc.bad {
				t.Fatalf("unexpected error: %v", e)
			}
			if e != nil && strings.Contains(e.Error(), "SECRET") {
				t.Fatal("upstream secret leaked")
			}
			if e == nil && got.State != tc.state {
				t.Fatal("wrong validation state")
			}
			if got.State == ValidationAccepted && (len(got.Models) != 1 || got.Models[0] != "model") {
				t.Fatal("model filtering failed")
			}
		})
	}
}
func TestDiscoveryFilters(t *testing.T) {
	row := map[string]any{"type": "TEXT", "providers": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}, "capabilities": []any{"tools", "vision"}}
	for _, tc := range []struct {
		p    DiscoveryPredicate
		want bool
	}{
		{DiscoveryPredicate{Path: []string{"type"}, EqualsAny: []any{"text"}, CaseInsensitive: true}, true},
		{DiscoveryPredicate{Path: []string{"providers", "*", "id"}, ContainsAll: []any{"a", "b"}}, true},
		{DiscoveryPredicate{Path: []string{"capabilities"}, ContainsAll: []any{"tools", "vision"}}, true},
		{DiscoveryPredicate{Path: []string{"capabilities"}, ContainsAny: []any{"tool"}}, false},
	} {
		if predicate(row, tc.p) != tc.want {
			t.Fatalf("predicate mismatch %+v", tc.p)
		}
	}
}

func TestLookupMatchesCatalogAndOwnsReferences(t *testing.T) {
	for _, want := range Catalog() {
		got, ok := Lookup(want.ID)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("lookup differs from catalog for %s", want.ID)
		}
		mutatePresetReferences(reflect.ValueOf(&got).Elem())
		again, ok := Lookup(want.ID)
		if !ok || !reflect.DeepEqual(again, want) {
			t.Fatalf("lookup exposed shared state for %s", want.ID)
		}
	}
	if got, ok := Lookup("unknown-provider"); ok || !reflect.DeepEqual(got, Preset{}) {
		t.Fatal("unknown lookup changed")
	}
}

// Corrupt each reference-backed value to exercise nested maps, pointer-valued
// capabilities and slices added to the snapshot without selecting one provider.
func mutatePresetReferences(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			mutatePresetReferences(v.Field(i))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			mutatePresetReferences(v.Elem())
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			mutatePresetReferences(v.Index(i))
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			mutatePresetReferences(value)
			v.SetMapIndex(key, value)
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	case reflect.Bool:
		if v.CanSet() {
			v.SetBool(!v.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.CanSet() {
			v.SetInt(v.Int() + 1)
		}
	case reflect.Uint8:
		if v.CanSet() {
			v.SetUint(v.Uint() ^ 1)
		}
	}
}

func BenchmarkLookup(b *testing.B) {
	for _, id := range []string{"openai-apikey", "unknown-provider"} {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Lookup(id)
			}
		})
	}
}
