// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeControlAuthentication(t *testing.T) {
	token := strings.Repeat("a", 64)
	replays := make(chan *http.Request, 1)
	control, _ := newNativeControlServer(token)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("reusable authorization sent")
		}
		if control.Challenge(w, r) {
			return
		}
		if !control.Authorize(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		replays <- r.Clone(context.Background())
		nativeControlRespond(w, r, token, 200, []byte(`{"ok":true}`))
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if _, e := nativeControlRequest(context.Background(), address, token, "GET", "/health"); e != nil {
		t.Fatal(e)
	}
	replay := <-replays
	if control.Authorize(replay) {
		t.Fatal("replay accepted")
	}
	replay.Header.Set("X-Tairouter-Time", strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10))
	replay.Header.Set("X-Tairouter-Proof", nativeControlProof(replay, token, "tairouter/native/request/v2"))
	if control.Authorize(replay) {
		t.Fatal("stale request accepted")
	}
	if NativeProxyPassword(token) == token {
		t.Fatal("proxy password not separated")
	}
	for _, attack := range []string{"body", "status", "path", "nonce", "generation", "redirect", "oversize"} {
		t.Run(attack, func(t *testing.T) {
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if control.Challenge(w, r) {
					return
				}
				body := []byte("ok")
				status := 200
				proof := nativeControlProof(r, token, "tairouter/native/response/v2", "200", "ok")
				switch attack {
				case "body":
					body = []byte("evil")
				case "status":
					status = 201
				case "path":
					r.URL.Path = "/other"
					proof = nativeControlProof(r, token, "tairouter/native/response/v2", "200", "ok")
				case "nonce":
					r.Header.Set("X-Tairouter-Nonce", strings.Repeat("b", 64))
					proof = nativeControlProof(r, token, "tairouter/native/response/v2", "200", "ok")
				case "generation":
					r.Header.Set("X-Tairouter-Generation", strings.Repeat("c", 64))
					proof = nativeControlProof(r, token, "tairouter/native/response/v2", "200", "ok")
				case "redirect":
					status = 302
					w.Header().Set("Location", server.URL)
				case "oversize":
					body = []byte(strings.Repeat("x", 4097))
				}
				w.Header().Set("X-Tairouter-Proof", proof)
				w.WriteHeader(status)
				w.Write(body)
			}))
			defer foreign.Close()
			if _, e := nativeControlRequest(context.Background(), strings.TrimPrefix(foreign.URL, "http://"), token, "GET", "/health"); e == nil {
				t.Fatal("forged response accepted")
			}
		})
	}
}
func TestNativeControllerSquatterCannotOwnRuntime(t *testing.T) {
	c, req, _ := controllerFixture(t)
	var mu sync.Mutex
	var requests []string
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, r.URL.String()+fmt.Sprint(r.Header)+string(b))
		mu.Unlock()
		io.WriteString(w, `{"status":"ready","pacURL":"http://127.0.0.1:17844/0123456789abcdef/proxy.pac"}`)
	}))
	defer foreign.Close()
	u, _ := url.Parse(foreign.URL)
	req.Port, _ = strconv.Atoi(u.Port())
	ctx := context.Background()
	status, e := c.Enable(ctx, req, controllerConsent(t, c, req))
	if e != nil || status.Ready {
		t.Fatalf("enable: %v ready %v", e, status.Ready)
	}
	record, _, e := c.load(req.Profile.Name)
	if e != nil {
		t.Fatal(e)
	}
	status, e = c.Status(ctx, req.Profile.Name)
	if e != nil || status.Ready {
		t.Fatal("squatter status ready")
	}
	if _, e = c.Ready(ctx, req.Profile.Name); e == nil {
		t.Fatal("squatter ready accepted")
	}
	if e = c.Stop(ctx, req.Profile.Name); e == nil {
		t.Fatal("squatter stop accepted")
	}
	if _, e = c.codexPAC(ctx, record); e == nil {
		t.Fatal("squatter PAC accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range requests {
		if strings.Contains(r, record.Token) || strings.Contains(r, NativeProxyPassword(record.Token)) {
			t.Fatal("reusable secret leaked")
		}
	}
}
