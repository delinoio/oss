// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"net/http"
	"testing"
)

func TestConnectionHeadersAndWire(t *testing.T) {
	c := Connection{Provider: "openrouter", APIKey: "key", Wire: Chat, Headers: map[string]string{"x-title": "Personal app", "X-Custom-Trace": "trace"}}
	r, e := Resolve(c, "model", InboundChat)
	if e != nil || r.Wire != Chat || r.Headers.Get("X-Title") != "Personal app" || r.Headers.Get("Authorization") != "Bearer key" {
		t.Fatal(r, e)
	}
	c.ModelWires = map[string]Wire{"model": Responses}
	r, e = Resolve(c, "model", InboundChat)
	if e != nil || r.Wire != Responses || r.Headers.Get("Authorization") != "Bearer key" {
		t.Fatal(r, e)
	}
	c = Connection{Provider: "custom", BaseURL: "https://example.test/v1", Wire: Chat, APIKey: "key", Headers: map[string]string{"X-Custom-Trace": "trace"}}
	a, _ := newMock(t, responseStep{"GET", "https://example.test/v1/models", 200, `{"data":[{"id":"model"}]}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("X-Custom-Trace") != "trace" || r.Header.Get("Authorization") != "Bearer key" {
			t.Fatal("discovery lost headers")
		}
	}})
	if _, e := a.ValidateRemote(context.Background(), testScope, c); e != nil {
		t.Fatal(e)
	}
}
func TestConnectionHeaderRejections(t *testing.T) {
	for _, h := range []map[string]string{{"aUThorization": "bad"}, {"Cookie": "bad"}, {"Proxy-Authorization": "bad"}, {"Connection": "x-extra"}, {"Transfer-Encoding": "chunked"}, {"Content-Length": "10"}, {"X-Aiprouter-Zed-Policy": "{}"}, {"ChatGPT-Account-ID": "other"}, {"X-Api-Key": "bad"}, {"Bad Name": "v"}, {"X-Test": "bad\r\nInjected: true"}, {"X-Test": "a", "x-test": "b"}} {
		c := Connection{Provider: "custom", BaseURL: "https://example.test/v1", Wire: Chat, APIKey: "key", Headers: h}
		if ValidateConnection(c) == nil {
			t.Fatalf("accepted forbidden headers %v", h)
		}
	}
}
func TestSpecializedWireCannotBeBypassed(t *testing.T) {
	for _, id := range []string{"cursor", "kiro", "zed", "devin", "workbuddy-ai", "google-antigravity", "google-vertex"} {
		p, _ := Lookup(id)
		if validModelWireOverride(p, Chat) {
			t.Fatal("specialized wire bypass", id)
		}

	}
	c := Connection{Provider: "cursor", Credential: &Credential{Scope: testScope, Provider: "cursor", AccessToken: "token"}, ModelWires: map[string]Wire{"unused-model": Chat}}
	if ValidateConnection(c) == nil {
		t.Fatal("inactive model bypass not validated")
	}
	c = Connection{Provider: "openrouter", APIKey: "key", Wire: Responses}
	if ValidateConnection(c) == nil {
		t.Fatal("generic promoted to specialized")
	}
}
func TestDiscoveryHeadersReachNativeCatalog(t *testing.T) {
	c := Credential{Scope: testScope, Provider: "openai", AccessToken: "token", AccountID: "account"}
	a, _ := newMock(t, responseStep{"GET", "https://chatgpt.com/backend-api/codex/models?client_version=99.0.0", 200, `{"models":[]}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("X-Trace") != "custom" || r.Header.Get("ChatGPT-Account-Id") != "account" {
			t.Fatal("lost native custom/account headers")
		}
	}})
	if _, _, e := a.Discover(context.Background(), testScope, Connection{Provider: "openai", Credential: &c, Headers: map[string]string{"x-trace": "custom"}}); e != nil {
		t.Fatal(e)
	}
	// A user Accept preference cannot turn a JSON response into an unchecked
	// protobuf response or permit malformed UTF-8 catalog bytes.
	a, _ = newMock(t, responseStep{"GET", "https://example.test/v1/models", 200, "{\"data\":[{\"id\":\"\xff\"}]}", nil})
	if _, e := a.ValidateRemote(context.Background(), testScope, Connection{Provider: "custom", Wire: Chat, BaseURL: "https://example.test/v1", APIKey: "key", Headers: map[string]string{"Accept": "application/proto"}}); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}
func TestModelWireOverridesStayWithinAuthBoundary(t *testing.T) {
	for _, c := range []Connection{
		{Provider: "openrouter", APIKey: "key", ModelWires: map[string]Wire{"model": Anthropic}},
		{Provider: "openrouter", APIKey: "key", ModelWires: map[string]Wire{"model": Google}},
		{Provider: "anthropic", APIKey: "key", Auth: Key, ModelWires: map[string]Wire{"model": Chat}},
		{Provider: "custom", Wire: Google, BaseURL: "https://example.test", APIKey: "key", ModelWires: map[string]Wire{"model": Chat}},
	} {
		if ValidateConnection(c) == nil {
			t.Fatal("cross-auth model override accepted", c.Provider)
		}
	}
}
func TestJEVExplicitPresetCredentialLane(t *testing.T) {
	c := Connection{Provider: "jev", Wire: JEVDecision, APIKey: "key"}
	if e := ValidateConnection(c); e != nil {
		t.Fatal(e)
	}
	if _, e := Resolve(c, "model", InboundResponses); e == nil {
		t.Fatal("credential-only JEV became inference route")
	}
	a, _ := newMock(t)
	v, e := a.ValidateRemote(context.Background(), testScope, c)
	if e != nil || v.State != ValidationUnknown {
		t.Fatal(v, e)
	}
	c.Wire = Chat
	if e := ValidateConnection(c); e == nil {
		t.Fatal("JEV pin bypass accepted")
	}
	c = Connection{Provider: "custom", Wire: JEVDecision, BaseURL: "https://api.typesafe.ai/v1/systemone", APIKey: "key"}
	if e := ValidateConnection(c); e != nil {
		t.Fatal(e)
	}
	for _, base := range []string{"https://api.typesafe.ai/v1/systemone", "https://decision.example.test/custom"} {
		c.BaseURL = base
		if e := ValidateConnection(c); e != nil {
			t.Fatal(e)
		}
		if _, e := Resolve(c, "decision-service-model", InboundResponses); e == nil {
			t.Fatal("custom JEV became direct inference route")
		}
		catalog, _, e := a.Discover(context.Background(), testScope, c)
		if e != nil || catalog.State != ValidationUnknown || len(catalog.Models) != 0 {
			t.Fatal(catalog, e)
		}
	}
	c.APIKey = ""
	if ValidateConnection(c) == nil {
		t.Fatal("custom JEV without credential accepted")
	}
	c.APIKey = "key"
	c.ModelWires = map[string]Wire{"model": Chat}
	if ValidateConnection(c) == nil {
		t.Fatal("custom JEV model pin bypass accepted")
	}
	c = Connection{Provider: "jev", Wire: JEVDecision, APIKey: "key", BaseURL: "https://decision.example.test/custom"}
	if ValidateConnection(c) == nil {
		t.Fatal("preset JEV destination overridden")
	}

}
func TestAnthropicCustomHeaderPrecedence(t *testing.T) {
	c := Connection{Provider: "anthropic", Credential: &Credential{Scope: testScope, Provider: "anthropic", AccessToken: "token"}, Headers: map[string]string{"anthropic-version": "2025-01-01", "ANTHROPIC-BETA": "personal-beta"}}
	r, e := Resolve(c, "claude-model", InboundAnthropic)
	if e != nil || r.Headers.Get("Anthropic-Version") != "2025-01-01" || r.Headers.Get("Anthropic-Beta") != "personal-beta" || r.Headers.Get("Authorization") != "Bearer token" {
		t.Fatal(r, e)
	}
	a, _ := newMock(t, responseStep{"GET", "https://api.anthropic.com/v1/models", 200, `{"data":[]}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("Anthropic-Version") != "2025-01-01" || r.Header.Get("Anthropic-Beta") != "personal-beta" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("discovery lost allowed custom precedence")
		}
	}})
	if _, e := a.ValidateRemote(context.Background(), testScope, c); e != nil {
		t.Fatal(e)
	}
}
