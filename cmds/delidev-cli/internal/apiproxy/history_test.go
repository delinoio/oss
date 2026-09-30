// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSwitchedHistoryGuardRunsBeforeCredentialsOrUpstream(t *testing.T) {
	for _, path := range []string{"/responses", "/responses/compact"} {
		for _, body := range []string{
			`{"model":"fixed-model","input":[],"previous_response_id":"resp_A"}`,
			`{"model":"fixed-model","input":[],"conversation":{"id":"conv_A"}}`,
			`{"model":"fixed-model","input":[{"type":"item_reference","id":"item_A"}]}`,
			`{"model":"fixed-model","input":["unrelated",{"type":"item_reference","id":"item_A"}]}`,
			`{"model":"fixed-model","input":[{"type":"item_reference","id":"item_A"},42]}`,
		} {
			t.Run(path+body, func(t *testing.T) {
				f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate, ResponseCompact}, func(w http.ResponseWriter, r *http.Request) { t.Error("account-bound request reached upstream") })
				f.authority.authorize = func(context.Context, ReferenceKind, string) error { return nil }
				f.authority.history = func(_ context.Context, bound bool) error {
					if !bound {
						t.Error("remote history was classified as portable")
					}
					return domain.Fail(domain.PermissionDenied, "Switched history is account-bound.", "")
				}
				response, _, err := f.request(t, path, body, nil)
				if err != nil || response.StatusCode != http.StatusForbidden || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
					t.Fatal("history guard did not precede provider work", err)
				}
			})
		}
	}
}

func TestFullNativeHistoryRemainsUnchangedByRelay(t *testing.T) {
	const body = `{"model":"fixed-model","input":[{"role":"user","content":"old A prompt"},{"role":"assistant","content":"old A output"},{"role":"user","content":"new B prompt"}],"previous_response_id":null}`
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != body {
			t.Error("relay changed full native history")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_B","object":"response","status":"completed","output":[]}`)
	})
	var observed atomic.Bool
	f.authority.history = func(_ context.Context, bound bool) error {
		observed.Store(true)
		if bound {
			t.Error("full history was treated as account-bound")
		}
		return nil
	}
	response, _, err := f.request(t, "/responses", body, nil)
	if err != nil || response.StatusCode != http.StatusOK || !observed.Load() || f.calls.Load() != 1 {
		t.Fatal("portable history failed", err)
	}
}
