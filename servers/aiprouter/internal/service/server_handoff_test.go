// SPDX-License-Identifier: Apache-2.0
package service

import (
	"net/http"
	"testing"

	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Exercise the actual server configuration: a successful routed summarizer saves
// a handoff, while a later native compaction clears this owner's same thread.
func TestServerCompactionHandoffRegistration(t *testing.T) {
	f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			providerAcceptanceResponse(w, providerAcceptanceChat)
		case "/v1/responses/compact":
			providerAcceptanceResponse(w, `{"output":[{"type":"compaction","encrypted_content":"fixture-native"}]}`)
		default:
			t.Error("unexpected compaction endpoint")
			w.WriteHeader(404)
		}
	})
	headers := http.Header{"Thread-Id": []string{"handoff-fixture-thread"}}
	body := `{"model":"fixture","input":[{"role":"user","content":"synthetic compaction task"}]}`
	providerAcceptanceStatus(t, f.call("/v1/responses/compact", body, headers), 200)
	count := func() int {
		t.Helper()
		var n int
		err := f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`_temp".compaction_handoffs WHERE key_id=$1`, f.keyID).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 1 {
		t.Fatalf("server did not capture routed handoff: %d", n)
	}
	wire := v1.ProviderWire_PROVIDER_WIRE_RESPONSES
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{Wire: &wire}
	f.save()
	providerAcceptanceStatus(t, f.call("/v1/responses/compact", body, headers), 200)
	if n := count(); n != 0 {
		t.Fatalf("native success did not clear handoff: %d", n)
	}
}
