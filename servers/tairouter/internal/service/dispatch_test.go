// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func dispatchFixture(t *testing.T) (*inferenceAdapter, core.Scope, string) {
	t.Helper()
	s := serverStore(t)
	u, _ := testIdentity(t, s, "dispatch")
	o, e := s.CreateOrganization(context.Background(), u.ID, "Dispatch org")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(context.Background(), o.ID, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, raw, e := s.IssueKey(context.Background(), sc, "dispatch", nil)
	if e != nil {
		t.Fatal(e)
	}
	return &inferenceAdapter{s: s, telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))}, sc, raw
}

type dispatchRouter struct{ url string }

func (r dispatchRouter) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{MaxSends: 1, Targets: []inference.Target{{Provider: "fixture", BaseURL: r.url, Model: "model", Wire: inference.Chat, Auth: inference.APIKey}}}, nil
}
func (r dispatchRouter) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}

type dispatchCredential struct{}

func (dispatchCredential) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return inference.Credential{AccountID: "fixture", Secret: "secret"}, nil
}
func (dispatchCredential) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrUnsupported
}

type barrierAuthorizer struct {
	*inferenceAdapter
	entered, proceed chan struct{}
}

func (b barrierAuthorizer) Dispatch(ctx context.Context, p inference.Principal, start func(context.Context) error) error {
	close(b.entered)
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.inferenceAdapter.Dispatch(ctx, p, start)
}
func dispatchHandler(t *testing.T, a *inferenceAdapter, authorizer inference.Authorizer, url string) *inference.Handler {
	t.Helper()
	h, e := inference.New(inference.Config{Authorizer: authorizer, Router: dispatchRouter{url}, Credentials: dispatchCredential{}, Usage: a, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{url}}})
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func invokeInference(ctx context.Context, h http.Handler, raw string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"test"}]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+raw)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDispatchRevocationBeforePhysicalSend(t *testing.T) {
	a, sc, raw := dispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sends atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	barrier := barrierAuthorizer{a, make(chan struct{}), make(chan struct{})}
	h := dispatchHandler(t, a, barrier, upstream.URL)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- invokeInference(ctx, h, raw) }()
	<-barrier.entered
	a.mu.Lock()
	keyID := a.lease.Principal.KeyID
	a.mu.Unlock()
	if e := a.s.RevokeKey(ctx, sc, keyID); e != nil {
		t.Fatal(e)
	}
	close(barrier.proceed)
	w := <-done
	if sends.Load() != 0 || w.Code < 400 {
		t.Fatalf("revoked key physically dispatched: sends=%d status=%d", sends.Load(), w.Code)
	}
}
func TestDispatchReleasesGateBeforeSlowHeaders(t *testing.T) {
	a, sc, raw := dispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, _ := testIdentity(t, a.s, "second")
	if _, e := a.s.Invite(ctx, sc, u.Email); e != nil {
		t.Fatal(e)
	}
	mail, e := a.s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var token string
	if e = a.s.WithMailAttempt(ctx, mail, func(_ context.Context, m store.Mail) store.MailOutcome { token = m.Token; return store.MailSuccess }); e != nil {
		t.Fatal(e)
	}
	other, e := a.s.AcceptInvite(ctx, u.ID, token)
	if e != nil {
		t.Fatal(e)
	}
	_, otherRaw, e := a.s.IssueKey(ctx, other, "other", nil)
	if e != nil {
		t.Fatal(e)
	}
	received := make(chan struct{})
	headers := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(received)
		select {
		case <-headers:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"test","model":"model","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	h := dispatchHandler(t, a, a, upstream.URL)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- invokeInference(ctx, h, raw) }()
	<-received
	admitted := make(chan error, 1)
	go func() {
		lease, e := a.s.Admit(ctx, otherRaw)
		if e == nil {
			e = a.s.Release(ctx, lease)
		}
		admitted <- e
	}()
	select {
	case e := <-admitted:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(headers)
		t.Fatal("slow headers blocked another user's admission")
	}
	a.mu.Lock()
	keyID := a.lease.Principal.KeyID
	a.mu.Unlock()
	revoked := make(chan error, 1)
	go func() { revoked <- a.s.RevokeKey(ctx, sc, keyID) }()
	select {
	case e := <-revoked:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(headers)
		t.Fatal("slow headers blocked key revocation")
	}
	close(headers)
	w := <-done
	if w.Code != 200 {
		t.Fatalf("already dispatched response failed: %d %s", w.Code, w.Body.String())
	}
}
