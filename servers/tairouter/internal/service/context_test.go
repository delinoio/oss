// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	rpc "github.com/delinoio/oss/protos/tairouter/v1/tairouterv1connect"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductionContextRegistrationAndRevocation(t *testing.T) {
	calls := 0
	f := newRecoveryHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "/alpha/") {
			providerAcceptanceResponse(w, `{"items":[]}`)
			return
		}
		providerAcceptanceResponse(w, `{"id":"ctx-resp","object":"response","status":"completed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`)
	})
	server := httptest.NewServer(f.srv.handler())
	defer server.Close()
	client := rpc.NewClientServiceClient(server.Client(), server.URL)
	configure := func(enabled bool) {
		r := connect.NewRequest(&v1.ConfigureClientContextRequest{Enabled: enabled})
		r.Header().Set("Authorization", "Bearer "+f.key)
		_, e := client.ConfigureClientContext(context.Background(), r)
		acceptanceOK(t, e)
	}
	bind := func(key, session string) error {
		r := connect.NewRequest(&v1.BindClientContextSessionRequest{SessionId: session})
		r.Header().Set("Authorization", "Bearer "+key)
		_, e := client.BindClientContextSession(context.Background(), r)
		return e
	}
	request := func() int {
		return f.call("/v1/alpha/history/v2/list_items", `{"context":{"session_id":"owned-session"}}`, nil).Code
	}
	if request() != 409 || calls != 0 {
		t.Fatal("unregistered context escaped")
	}
	configure(true)
	if bind(f.key, "owned-session") == nil {
		t.Fatal("unobserved account binding accepted")
	}
	providerAcceptanceStatus(t, f.call("/v1/responses", `{"model":"fixture","input":"hello","store":false}`, http.Header{"Session-Id": []string{"owned-session"}}), 200)
	if request() != 409 {
		t.Fatal("unconfirmed context escaped")
	}
	acceptanceOK(t, bind(f.key, "owned-session"))
	if request() != 200 {
		t.Fatalf("registered production context unavailable: %s", f.call("/v1/alpha/history/v2/list_items", `{"context":{"session_id":"owned-session"}}`, nil).Body.String())
	}
	_, other, e := f.s.IssueKey(context.Background(), f.scope, "other", nil)
	acceptanceOK(t, e)
	if bind(other, "owned-session") == nil {
		t.Fatal("another key bound the session")
	}
	// Capability removal is checked again at the physical-send gate, not only
	// when the request first resolves its binding.
	lease, e := f.s.Admit(context.Background(), f.key)
	acceptanceOK(t, e)
	bound, e := f.s.ResolveClientContext(context.Background(), lease, "owned-session")
	acceptanceOK(t, e)
	acceptanceOK(t, f.s.RotateEncryption(context.Background()))
	configure(false)
	sent := false
	e = f.s.WithClientContextDispatch(context.Background(), lease, "owned-session", bound.Receipt, func(context.Context) error { sent = true; return nil })
	if e == nil || sent {
		t.Fatal("disabled context escaped dispatch gate")
	}
	acceptanceOK(t, f.s.Release(context.Background(), lease))
	configure(true)
	providerAcceptanceStatus(t, f.call("/v1/responses", `{"model":"fixture","input":"hello","store":false}`, http.Header{"Session-Id": []string{"owned-session"}}), 200)
	acceptanceOK(t, bind(f.key, "owned-session"))
	// Credential replacement invalidates the bound generation; it never chooses
	// another account even though the public route is still available.
	f.data.Secret = "replacement"
	f.data.Credential.AccessToken = "replacement"
	f.save()
	if request() != 409 {
		t.Fatal("stale credential binding survived replacement")
	}
	configure(false)
	var n int
	acceptanceOK(t, f.db.QueryRow("SELECT count(*) FROM "+f.schema+"_temp.client_context_bindings").Scan(&n))
	if n != 0 {
		t.Fatal("disable retained bindings")
	}
	configure(true)
	if bind(f.key, "owned-session") == nil {
		t.Fatal("reenable resurrected deleted proof")
	}
}
