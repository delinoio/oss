// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDeprecatedMiMoSavedRuntime(t *testing.T) {
	c := Connection{Provider: "mimo-free"}
	if err := ValidateConnection(c); !errors.Is(err, ErrProvider) {
		t.Fatal("new deprecated enrollment accepted", err)
	}
	if err := ValidateSavedConnection(c); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(c, "", InboundChat)
	if err != nil {
		t.Fatal(err)
	}
	if r.Wire != "mimo-free" || r.BaseURL != "https://api.xiaomimimo.com/api/free-ai/openai/chat" || r.Model != "mimo-auto" {
		t.Fatalf("wrong saved MiMo route %+v", r)
	}
	if r.Preset.Deprecated == "" || !strings.Contains(string(r.Preset.Metadata), "Unsupported model") {
		t.Fatal("live-unavailability disclosure removed")
	}
	a, _ := newMock(t)
	catalog, _, err := a.Discover(context.Background(), testScope, c)
	if err != nil || catalog.State != ValidationUnknown {
		t.Fatal(catalog, err)
	}
	for _, edit := range []func(*Connection){func(c *Connection) { c.BaseURL = "https://attacker.test" }, func(c *Connection) { c.Wire = Chat }, func(c *Connection) { c.ModelWires = map[string]Wire{"mimo-auto": Responses} }, func(c *Connection) { c.Headers = map[string]string{"Authorization": "injected"} }, func(c *Connection) { c.APIKey = "bad\nkey" }} {
		bad := c
		edit(&bad)
		if _, err := Resolve(bad, "", InboundChat); err == nil {
			t.Fatal("runtime security validation bypassed")
		}
	}
	for _, id := range []string{"missing", "claude-cli", "qoder", "codebuddy"} {
		if _, err := Resolve(Connection{Provider: id, APIKey: "key"}, "model", InboundChat); !errors.Is(err, ErrProvider) {
			t.Fatal("excluded/unknown runtime allowed", id, err)
		}
	}
}
