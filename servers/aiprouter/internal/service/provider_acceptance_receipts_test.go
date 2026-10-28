// SPDX-License-Identifier: Apache-2.0
package service

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Opaque token receipts are distinct from raw tool reasoning restoration.
func TestProviderAcceptanceSignedReasoningReceipts(t *testing.T) {
	for _, wire := range []v1.ProviderWire{v1.ProviderWire_PROVIDER_WIRE_RESPONSES, v1.ProviderWire_PROVIDER_WIRE_ANTHROPIC} {
		t.Run(wire.String(), func(t *testing.T) {
			var calls atomic.Int32
			f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if wire == v1.ProviderWire_PROVIDER_WIRE_RESPONSES {
					providerAcceptanceResponse(w, `{"id":"resp_signed","object":"response","status":"completed","model":"fixture-model","output":[{"type":"reasoning","encrypted_content":"opaque-fixture-token","summary":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`)
				} else {
					providerAcceptanceResponse(w, `{"id":"msg_signed","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"thinking","thinking":"signed fixture thought","signature":"opaque-fixture-token"},{"type":"text","text":"fixture ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
				}
			})
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{Wire: &wire}
			f.save()
			path, first, next := "/v1/responses", `{"model":"fixture","input":"hello"}`, `{"model":"fixture","input":[{"type":"reasoning","encrypted_content":"opaque-fixture-token","summary":[]},{"role":"user","content":"next"}]}`
			if wire == v1.ProviderWire_PROVIDER_WIRE_ANTHROPIC {
				path = "/v1/messages"
				first = `{"model":"fixture","max_tokens":25,"messages":[{"role":"user","content":"hello"}]}`
				next = `{"model":"fixture","max_tokens":25,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"thinking","thinking":"signed fixture thought","signature":"opaque-fixture-token"},{"type":"text","text":"fixture ok"}]},{"role":"user","content":"next"}]}`
			}
			providerAcceptanceStatus(t, f.call(path, first, nil), 200)
			providerAcceptanceStatus(t, f.call(path, next, nil), 200)
			if calls.Load() != 2 {
				t.Fatal("signed receipt did not survive server request boundary")
			}
			_, e := f.db.Exec(`UPDATE "` + f.schema + `_temp".provider_states SET expires_at=clock_timestamp()-interval '1 second' WHERE namespace IN ('openai-responses','anthropic')`)
			acceptanceOK(t, e)
			w := f.call(path, next, nil)
			if w.Code < 400 || calls.Load() != 2 || !strings.Contains(w.Body.String(), "unbound_signed_reasoning") {
				t.Fatal("expired signed receipt accepted")
			}
		})
	}
}
