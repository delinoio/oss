// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeUsageAccountDocument(connection string) []byte {
	return []byte(`{"type":"subscription","subscription":{"claude_credential":"setup","claude_usage":{"connection":"` + connection + `","generation":"gen","identity":{"email":"person@example.com","organization":"Example Org","plan":"Max","commitment":"` + strings.Repeat("a", 64) + `"}}}}`)
}

func TestAccountResourceProjectionRedactsUnlinkedClaudeUsageIdentity(t *testing.T) {
	record := store.Record{ID: "account", Kind: "account", Revision: 3, Data: claudeUsageAccountDocument("confirming")}
	original := bytes.Clone(record.Data)
	public := Resource(record).DocumentJson
	for _, private := range []string{"person@example.com", "Example Org", "Max"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("confirming projection disclosed reviewed identity text %q", private)
		}
	}
	var document struct {
		Subscription struct {
			Usage struct {
				Connection string `json:"connection"`
				Generation string `json:"generation"`
				Identity   struct {
					Email      string `json:"email"`
					Commitment string `json:"commitment"`
				} `json:"identity"`
			} `json:"claude_usage"`
			Credential string `json:"claude_credential"`
		} `json:"subscription"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(public, &document); err != nil {
		t.Fatal(err)
	}
	usage := document.Subscription.Usage
	if usage.Connection != "confirming" || usage.Generation != "gen" || usage.Identity.Commitment != strings.Repeat("a", 64) || usage.Identity.Email != "" || document.Subscription.Credential != "setup" || document.Type != "subscription" {
		t.Fatalf("projection lost the lifecycle, commitment or sibling fields: %s", public)
	}
	if !bytes.Equal(record.Data, original) {
		t.Fatal("projection mutated the stored account document")
	}
}

func TestAccountResourceProjectionKeepsOtherClaudeUsageStatesAndKinds(t *testing.T) {
	for _, connection := range []string{"connecting", "linked", "reconnect-required"} {
		raw := claudeUsageAccountDocument(connection)
		if got := Resource(store.Record{ID: "account", Kind: "account", Data: raw}).DocumentJson; !bytes.Equal(got, raw) {
			t.Fatalf("%s projection changed the account document", connection)
		}
	}
	raw := claudeUsageAccountDocument("confirming")
	if got := Resource(store.Record{ID: "project", Kind: "project", Data: raw}).DocumentJson; !bytes.Equal(got, raw) {
		t.Fatal("account redaction changed another resource kind")
	}
	for _, malformed := range []string{`{"subscription":{"claude_usage":[]}}`, `{"subscription":"claude_usage"}`, `not-json claude_usage`} {
		if got := Resource(store.Record{ID: "account", Kind: "account", Data: []byte(malformed)}).DocumentJson; string(got) != malformed {
			t.Fatalf("unparsable document %q was rewritten", malformed)
		}
	}
}
