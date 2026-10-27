// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type gateAuth struct {
	*testAuth
	mu      sync.Mutex
	arrived chan struct{}
	proceed chan struct{}
}

func (a *gateAuth) Dispatch(ctx context.Context, p Principal, start func(context.Context) error) error {
	if a.arrived != nil {
		close(a.arrived)
		select {
		case <-a.proceed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	return start(ctx)
}
func TestRevokedBetweenSelectionAndPhysicalWrite(t *testing.T) {
	h, _, a, _, u := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("revoked request reached upstream") }, Chat)
	g := &gateAuth{testAuth: a, arrived: make(chan struct{}), proceed: make(chan struct{})}
	h.c.Authorizer = g
	done := make(chan int, 1)
	go func() { w := call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`); done <- w.Code }()
	<-g.arrived
	a.denied.Store(true)
	close(g.proceed)
	if code := <-done; code != 401 {
		t.Fatalf("status %d", code)
	}
	if u.records[0].Sends != 0 {
		t.Fatal("unsent request charged as send")
	}
}
func TestSlowResponseDoesNotHoldDispatchGate(t *testing.T) {
	received := make(chan struct{})
	finish := make(chan struct{})
	h, _, a, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(received)
		<-finish
		io.WriteString(w, fixture(Chat))
	}, Chat)
	g := &gateAuth{testAuth: a}
	h.c.Authorizer = g
	done := make(chan struct{})
	go func() { defer close(done); call(h, "/v1/chat/completions", `{"model":"m","messages":[]}`) }()
	<-received
	admitted := make(chan struct{})
	go func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		_, release, e := g.Admit(context.Background(), "client-secret")
		if e == nil {
			release()
		}
		close(admitted)
	}()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		close(finish)
		t.Fatal("dispatch gate held while waiting for headers")
	}
	close(finish)
	<-done
}
func TestSSEMultilineAndCRLF(t *testing.T) {
	var got string
	e := readSSE(strings.NewReader(": heartbeat\r\ndata: first\r\ndata: second\r\n\r\n"), func(_ string, b []byte) error { got = string(b); return nil })
	if e != nil || got != "first\nsecond" {
		t.Fatalf("%q %v", got, e)
	}
}

type preflightAdapter struct{ base string }

func (a preflightAdapter) Prepare(ctx context.Context, in AdapterInput, x AdapterExecutor) (json.RawMessage, error) {
	for range 2 {
		resp, e := x.Do(ctx, AdapterRequest{Method: "GET", URL: a.base + "/catalog"})
		if e != nil {
			return nil, e
		}
		resp.Body.Close()
	}
	return in.Body, nil
}
func (a preflightAdapter) Build(_ context.Context, in AdapterInput) (AdapterRequest, error) {
	return AdapterRequest{URL: a.base + "/model", Body: in.Body, ResponseWire: Chat}, nil
}
func (a preflightAdapter) Decode(_ context.Context, r *http.Response) (*http.Response, error) {
	return r, nil
}
func TestPreflightSharesPhysicalBudget(t *testing.T) {
	sends := 0
	h, srv, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if r.URL.Path != "/catalog" {
			t.Error("model sent after budget spent")
		}
		io.WriteString(w, `{}`)
	}, Wire("fixture-special"))
	h.c.Adapters = map[Wire]Adapter{Wire("fixture-special"): preflightAdapter{srv.URL}}
	rt := h.c.Router.(testRouter)
	rt.route.MaxSends = 2
	h.c.Router = rt
	w := call(h, "/v1/responses", `{"model":"m","input":"hi"}`)
	if w.Code != 429 || sends != 2 || u.records[0].Sends != 2 {
		t.Fatalf("%d sends=%d %+v", w.Code, sends, u.records)
	}
}

func TestDuplexExecutorDoesNotSerializeHeaderWaits(t *testing.T) {
	runEntered := make(chan struct{})
	appendEntered := make(chan struct{})
	h, srv, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/run":
			close(runEntered)
			<-appendEntered
		case "/append":
			close(appendEntered)
		}
		io.WriteString(w, `{}`)
	}, Chat)
	p := Principal{OrganizationID: "org", UserID: "user", KeyID: "key"}
	rec := &UsageRecord{}
	x := &requestExecutor{h: h, p: p, target: Target{BaseURL: srv.URL}, record: rec, maxSends: 2}
	done := make(chan error, 1)
	go func() {
		r, e := x.Do(context.Background(), AdapterRequest{URL: srv.URL + "/run"})
		if e == nil {
			r.Body.Close()
		}
		done <- e
	}()
	<-runEntered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, e := x.Do(ctx, AdapterRequest{URL: srv.URL + "/append"})
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if rec.Sends != 2 {
		t.Fatal(rec.Sends)
	}
	if _, e = x.Do(ctx, AdapterRequest{URL: srv.URL + "/extra"}); publicError(e).Code != "request_send_budget_exhausted" {
		t.Fatal(e)
	}
}
