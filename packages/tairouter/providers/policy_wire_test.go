// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"testing"
)

func TestTrustedPolicyWireAuthSynthesis(t *testing.T) {
	c := Connection{Provider: "openrouter", APIKey: "private", Headers: map[string]string{"Anthropic-Version": "custom-version"}}
	for _, tc := range []struct {
		wire   Wire
		header string
	}{{Anthropic, "X-Api-Key"}, {Google, "X-Goog-Api-Key"}, {Responses, "Authorization"}} {
		r, e := ResolvePolicyWire(c, "model", InboundResponses, tc.wire)
		if e != nil || r.Wire != tc.wire || r.Headers.Get(tc.header) == "" {
			t.Fatal(r, e)
		}
		for _, header := range []string{"X-Api-Key", "X-Goog-Api-Key", "Authorization"} {
			if header != tc.header && r.Headers.Get(header) != "" {
				t.Fatal("stale auth scheme", header)
			}
		}
		if tc.wire == Anthropic && r.Headers.Get("Anthropic-Version") != "custom-version" {
			t.Fatal("custom precedence lost")
		}
	}
	c.ModelWires = map[string]Wire{"model": Anthropic}
	if _, e := ResolvePolicyWire(c, "model", InboundResponses, Anthropic); e == nil {
		t.Fatal("trusted helper bypassed user validation")
	}
}
func TestTrustedPolicyWirePreservesSpecializedPins(t *testing.T) {
	c := Connection{Provider: "cursor", Credential: &Credential{Scope: testScope, Provider: "cursor", AccessToken: "token"}}
	if _, e := ResolvePolicyWire(c, "model", InboundResponses, Anthropic); e == nil {
		t.Fatal("specialized wire bypass")
	}
	if r, e := ResolvePolicyWire(c, "model", InboundResponses, Wire("cursor")); e != nil || r.Wire != "cursor" {
		t.Fatal(r, e)
	}
	c = Connection{Provider: "google-antigravity", Credential: &Credential{Scope: testScope, Provider: "google-antigravity", AccessToken: "token", ProjectID: "project"}}
	if _, e := ResolvePolicyWire(c, "model", InboundResponses, Responses); e == nil {
		t.Fatal("project envelope bypass")
	}
	c = Connection{Provider: "openrouter", APIKey: "key"}
	if _, e := ResolvePolicyWire(c, "model", InboundResponses, Wire("cursor")); e == nil {
		t.Fatal("generic promoted to specialized")
	}
	if _, e := ResolvePolicyWire(c, "model", InboundResponses, ""); e == nil {
		t.Fatal("missing trusted wire accepted")
	}
}
