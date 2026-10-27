// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type viewFixture struct {
	auth   *testAuth
	revoke bool
}

func (v viewFixture) Read(_ context.Context, p Principal, op Operation, q url.Values) (json.RawMessage, error) {
	if p.OrganizationID != "org" || p.UserID != "user" {
		return nil, ErrUnauthorized
	}
	if v.revoke {
		v.auth.denied.Store(true)
	}
	return json.RawMessage(`{"models":[]}`), nil
}
func TestScopedViewsAndHead(t *testing.T) {
	h, _, auth, _, _ := harness(t, func(http.ResponseWriter, *http.Request) { t.Fatal("view contacted provider") }, Chat)
	h.c.Views = viewFixture{auth, false}
	for _, path := range []string{"/v1/catalog", "/v1/hub-state"} {
		req := httptest.NewRequest("HEAD", path, nil)
		req.Header.Set("Authorization", "Bearer client-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/v1/usage?model=a&model=b", nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	h.c.Views = viewFixture{auth, true}
	req = httptest.NewRequest("GET", "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("view returned after revocation")
	}
}

type contextFixture struct {
	binding ContextBinding
	expire  bool
}

func (c contextFixture) ResolveContext(_ context.Context, p Principal, id string) (ContextBinding, error) {
	if id != "session" || p.UserID != "user" || c.expire {
		return ContextBinding{}, &Error{409, "context_account_unavailable", 0}
	}
	return c.binding, nil
}
func (c contextFixture) ValidateContext(context.Context, Principal, string, ContextBinding) error {
	if c.expire {
		return ErrContinuation
	}
	return nil
}
func TestContextExactOwnerAndSingleSend(t *testing.T) {
	sends := 0
	h, srv, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if !strings.HasPrefix(r.URL.Path, "/alpha/") {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer bound-account" {
			t.Error("wrong account")
		}
		io.WriteString(w, `{"items":[]}`)
	}, Responses)
	h.c.Contexts = contextFixture{binding: ContextBinding{Target: Target{BaseURL: srv.URL, Wire: Responses, Auth: OAuth}, Credential: Credential{AccountID: "bound", Secret: "bound-account"}, Receipt: "generation:1"}}
	for path := range contextPaths {
		w := call(h, "/v1/"+path, `{"context":{"session_id":"session"}}`)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if sends != 10 {
		t.Fatal(sends)
	}
	w := call(h, "/v1/alpha/notes/v2/write_file", `{"context":{"session_id":"foreign"}}`)
	if w.Code != 409 || sends != 10 {
		t.Fatal("foreign owner sent")
	}
}
func audioBody(t *testing.T, duplicate bool) ([]byte, string) {
	var b bytes.Buffer
	f := multipart.NewWriter(&b)
	part, e := f.CreateFormFile("file", "sample.wav")
	if e != nil {
		t.Fatal(e)
	}
	part.Write([]byte("mock audio"))
	f.WriteField("model", "whisper-1")
	if duplicate {
		f.WriteField("model", "gpt-4o-transcribe")
	}
	f.WriteField("response_format", "text")
	f.Close()
	return b.Bytes(), f.FormDataContentType()
}
func TestTranscriptionMultipartContract(t *testing.T) {
	sends := 0
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		sends++
		if r.URL.Path != "/audio/transcriptions" {
			t.Error(r.URL.Path)
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
		}
		if r.FormValue("response_format") != "json" || r.FormValue("model") != "whisper-1" {
			t.Error("bad projection")
		}
		io.WriteString(w, `{"text":"transcribed","private":"must not forward"}`)
	}, Chat)
	for _, duplicate := range []bool{false, true} {
		b, ct := audioBody(t, duplicate)
		req := httptest.NewRequest("POST", "/v1/audio/transcriptions", bytes.NewReader(b))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer client-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if duplicate {
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
		} else if w.Code != 200 || w.Body.String() != "transcribed" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if sends != 1 {
		t.Fatal(sends)
	}
}

type liveStoreFixture struct {
	binding LiveBinding
	p       Principal
	id      string
}

func (s *liveStoreFixture) SaveBinding(_ context.Context, p Principal, id string, b LiveBinding) error {
	s.binding = b
	s.p = p
	s.id = id
	return nil
}
func (s *liveStoreFixture) ResolveBinding(_ context.Context, p Principal, id string) (LiveBinding, Credential, error) {
	if p != s.p || id != s.id {
		return LiveBinding{}, Credential{}, errors.New("missing")
	}
	return s.binding, Credential{AccountID: s.binding.AccountID, Secret: "bound"}, nil
}
func TestLiveCallBindingRewritesLocation(t *testing.T) {
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realtime/calls" || r.URL.Query().Get("architecture") != "avas" {
			t.Error(r.URL.String())
		}
		w.Header().Set("Location", "https://provider.example/v1/realtime/calls/call_original")
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(201)
		io.WriteString(w, "answer")
	}, Responses)
	store := &liveStoreFixture{}
	h.c.LiveBindings = store
	req := httptest.NewRequest("POST", "/v1/realtime/calls", strings.NewReader(`{"sdp":"offer","session":{"model":"realtime"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 201 || w.Header().Get("Location") != "/v1/realtime/calls/"+store.id || store.binding.UpstreamCallID != "call_original" || store.binding.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		t.Fatalf("%d %v %+v", w.Code, w.Header(), store.binding)
	}
}

type continuationFixture struct {
	owner Principal
	data  map[string]Continuation
}

func (s *continuationFixture) Load(_ context.Context, p Principal, id string) (Continuation, error) {
	if p != s.owner {
		return Continuation{}, ErrContinuation
	}
	c, ok := s.data[id]
	if !ok {
		return c, ErrContinuation
	}
	return c, nil
}
func (s *continuationFixture) Save(_ context.Context, p Principal, id string, c Continuation) error {
	s.owner = p
	s.data[id] = c
	return nil
}
func TestContinuationExpiryIsolationAndHistory(t *testing.T) {
	turn := 0
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		turn++
		raw, _ := io.ReadAll(r.Body)
		b, _ := decode(raw)
		if turn == 2 && len(arr(b["input"])) != 3 {
			t.Errorf("history %s", raw)
		}
		io.WriteString(w, fixture(Responses))
	}, Responses)
	state := &continuationFixture{data: map[string]Continuation{}}
	h.c.Continuations = state
	w := call(h, "/v1/responses", `{"model":"m","input":"first"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(h, "/v1/responses", `{"model":"m","previous_response_id":"resp_fixture","input":"next"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c := state.data["resp_fixture"]
	c.ExpiresAt = time.Now().Add(-time.Second)
	state.data["resp_fixture"] = c
	w = call(h, "/v1/responses", `{"model":"m","previous_response_id":"resp_fixture","input":"next"}`)
	if w.Code != 400 || turn != 2 {
		t.Fatal("expired continuation dispatched")
	}
	state.owner.OrganizationID = "other"
	w = call(h, "/v1/responses", `{"model":"m","previous_response_id":"resp_fixture","input":"next"}`)
	if w.Code != 400 || turn != 2 {
		t.Fatal("cross-org continuation dispatched")
	}
}
