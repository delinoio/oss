// SPDX-License-Identifier: Apache-2.0
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"strings"
	"testing"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{key: key, issuer: "http://127.0.0.1:18090", codes: map[string]grant{}}
}
func TestProvider(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		body     string
		status   int
		contains string
	}{{`{"model":"fixture-model"}`, 200, "fixture response"}, {`{"model":"fixture-429"}`, 429, "rate_limit_error"}, {`{"model":"fixture-503"}`, 503, "unavailable"}, {`{"stream":true}`, 200, "data: [DONE]"}} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		f.handler().ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("unexpected fixture response %d %s", w.Code, w.Body.String())
		}
	}
}
func TestOIDCCodeSingleUse(t *testing.T) {
	f := newFixture(t)
	h := f.handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/authorize?client_id=tairouter-fixture&response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Fauth%2Fcallback&state=test&nonce=nonce", nil))
	if w.Code != 302 {
		t.Fatal(w.Body.String())
	}
	redirect, _ := url.Parse(w.Header().Get("Location"))
	if redirect.Query().Get("state") != "test" {
		t.Fatal("state lost")
	}
	body := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}}.Encode()
	for i, status := range []int{200, 400} {
		r := httptest.NewRequest("POST", "/token", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
		if i == 0 {
			var token map[string]any
			if json.Unmarshal(w.Body.Bytes(), &token) != nil || token["id_token"] == nil {
				t.Fatal("missing token")
			}
		}
	}
}
func TestOIDCRedirectRejected(t *testing.T) {
	f := newFixture(t)
	w := httptest.NewRecorder()
	f.handler().ServeHTTP(w, httptest.NewRequest("GET", "/authorize?redirect_uri=https://evil.invalid", nil))
	if w.Code != 400 {
		t.Fatal("unsafe redirect accepted")
	}
}
func TestSMTPReceiver(t *testing.T) {
	f := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			f.smtp(conn)
		}
	}()
	if err = smtp.SendMail(ln.Addr().String(), nil, "sender@example.invalid", []string{"alice@example.invalid"}, []byte("Subject: fixture\r\n\r\nsynthetic\r\n")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	count := f.mail
	f.mu.Unlock()
	if count != 1 {
		t.Fatal(count)
	}
}
func TestDiscovery(t *testing.T) {
	f := newFixture(t)
	s := httptest.NewServer(f.handler())
	defer s.Close()
	r, err := http.Get(s.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), f.issuer) {
		t.Fatal("missing issuer")
	}
}
