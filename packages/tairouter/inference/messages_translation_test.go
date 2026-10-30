// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestForceMessagesTranslationKeepsDestinationAndNormalizesResponses(t *testing.T) {
	for _, forced := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("forced=%v/stream=%v", forced, streaming), func(t *testing.T) {
				h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/messages" {
						t.Errorf("wrong endpoint %s", r.URL.Path)
					}
					var body object
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["max_tokens"] == nil || body["messages"] == nil {
						t.Errorf("not Messages: %v", body)
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, strings.ReplaceAll(streamFixture(Anthropic), `"type":"message_stop"`, `"type":"message_stop","native_extension":true`))
					} else {
						io.WriteString(w, strings.Replace(fixture(Anthropic), `"type":"message"`, `"type":"message","native_extension":true`, 1))
					}
				}, Anthropic)
				router := h.c.Router.(testRouter)
				router.route.Targets[0].ForceMessagesTranslation = forced
				h.c.Router = router
				out := call(h, "/v1/messages", fmt.Sprintf(`{"model":"m","max_tokens":100,"stream":%v,"messages":[{"role":"user","content":"hi"}]}`, streaming))
				if out.Code != 200 || !strings.Contains(out.Body.String(), "hello") {
					t.Fatalf("%d %s", out.Code, out.Body)
				}
				if strings.Contains(out.Body.String(), "native_extension") == forced {
					t.Fatalf("wrong response lane: %s", out.Body)
				}
			})
		}
	}
}

func TestForceMessagesTranslationRejectsUnrepresentableRequestBeforeSend(t *testing.T) {
	h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected send") }, Anthropic)
	router := h.c.Router.(testRouter)
	router.route.Targets[0].ForceMessagesTranslation = true
	h.c.Router = router
	out := call(h, "/v1/messages", `{"model":"m","max_tokens":100,"native_only":true,"messages":[{"role":"user","content":"hi"}]}`)
	if out.Code != 400 || u.records[0].Sends != 0 {
		t.Fatalf("%d %s", out.Code, out.Body)
	}
}
