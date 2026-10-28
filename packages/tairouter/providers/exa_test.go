// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"testing"
)

func TestExaPersonalHostedCredential(t *testing.T) {
	p, ok := Lookup("exa")
	if !ok || p.Auth != Key || !p.CredentialOnly || p.BaseURL != "https://api.exa.ai" || p.AllowBaseURLOverride || p.LiveModels == nil || *p.LiveModels || len(p.Models) != 0 || len(AuthMethods("exa")) != 0 {
		t.Fatalf("invalid hosted credential preset %+v", p)
	}
	c := Connection{Provider: "exa", Auth: Key, APIKey: "personal-fixture-key"}
	if e := ValidateConnection(c); e != nil {
		t.Fatal(e)
	}
	if _, e := Resolve(c, "search", InboundResponses); e == nil {
		t.Fatal("Exa became inference route")
	}
	a, _ := newMock(t)
	v, e := a.ValidateRemote(context.Background(), testScope, c)
	if e != nil || v.State != ValidationUnknown {
		t.Fatal(v, e)
	}
	models, _, e := a.Discover(context.Background(), testScope, c)
	if e != nil || models.State != ValidationUnknown || len(models.Models) != 0 {
		t.Fatal(models, e)
	}
	p.Metadata[0] = '!'
	fresh, _ := Lookup("exa")
	if fresh.Metadata[0] == '!' {
		t.Fatal("mutable extension metadata")
	}
}
func TestExaNarrowConfiguration(t *testing.T) {
	for _, edit := range []func(*Connection){
		func(c *Connection) { c.APIKey = "" }, func(c *Connection) { c.APIKey = " " }, func(c *Connection) { c.APIKey = "key\x00" },
		func(c *Connection) { c.BaseURL = "https://api.exa.ai.evil.test" }, func(c *Connection) { c.BaseURL = "https://api.exa.ai/search" }, func(c *Connection) { c.BaseURL = "http://api.exa.ai" },
		func(c *Connection) { c.Wire = Chat }, func(c *Connection) { c.Auth = OAuth }, func(c *Connection) { c.ModelWires = map[string]Wire{"search": Responses} },
		func(c *Connection) {
			c.Credential = &Credential{Scope: testScope, Provider: "exa", AccessToken: "other-token"}
		},
	} {
		c := Connection{Provider: "exa", APIKey: "personal-key"}
		edit(&c)
		if ValidateConnection(c) == nil {
			t.Fatal("invalid Exa configuration accepted")
		}
	}
	if ValidateConnection(Connection{Provider: "custom", Wire: ExaSearch, APIKey: "key", BaseURL: "https://api.exa.ai"}) == nil {
		t.Fatal("custom Exa adapter accepted")
	}
}
