// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"net/http"
	"testing"
)

func TestCodexCatalogExactRows(t *testing.T) {
	raw := `{ "slug":"native-model", "display_name":"Native", "model_messages":{"instructions_template":"exact upstream template"}, "future_field":null }`
	c := Credential{Scope: testScope, Provider: "openai", AccessToken: "account-token", AccountID: "account-id"}
	a, _ := newMock(t, responseStep{"GET", "https://chatgpt.com/backend-api/codex/models?client_version=99.0.0", 200, `{"models":[` + raw + `]}`, func(t *testing.T, r *http.Request, b []byte) {
		if r.Header.Get("Authorization") != "Bearer account-token" || r.Header.Get("ChatGPT-Account-Id") != "account-id" {
			t.Fatal("wrong account")
		}
	}})
	catalog, _, err := a.Discover(context.Background(), testScope, Connection{Provider: "openai", Credential: &c})
	if err != nil || len(catalog.CodexCatalog) != 1 || string(catalog.CodexCatalog[0]) != raw || catalog.CodexClientVersion != "99.0.0" {
		t.Fatal(catalog, err)
	}
	for _, v := range []string{"0", "0.0.0-dev", "bad", "1.2\r\n"} {
		a, _ := newMock(t)
		_, _, err := a.Discover(context.Background(), testScope, Connection{Provider: "openai", Credential: &c, CodexClientVersion: v})
		if err == nil {
			t.Fatal("invalid version", v)
		}
	}
	a, _ = newMock(t, responseStep{"GET", "https://chatgpt.com/backend-api/codex/models?client_version=0.155.0", 200, `{"models":[]}`, nil})
	catalog, _, err = a.Discover(context.Background(), testScope, Connection{Provider: "openai", Credential: &c, CodexClientVersion: "0.155.0"})
	if err != nil || catalog.CodexCatalog == nil || len(catalog.Models) != 0 {
		t.Fatal(catalog, err)
	}
}
