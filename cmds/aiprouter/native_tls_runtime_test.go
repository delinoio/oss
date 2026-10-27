// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type nativeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nativeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeRuntimeFixture(t *testing.T, transport http.RoundTripper) (NativeTLSConfig, *http.Client, func()) {
	t.Helper()
	ca, _, _ := nativeTestCA(t)
	p, e := NewNativeCompatibilityPolicy(NativeCompatibilityClaudePicker, "darwin", NativeCompatibilityClaudeBuild)
	if e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := ln.Addr().String()
	ln.Close()
	ready := make(chan struct{})
	cfg := NativeTLSConfig{Address: addr, Policy: p, CA: ca, Gateway: "https://router.test", Key: "router-key", Token: strings.Repeat("a", 64), Transport: transport, OnReady: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunNativeTLS(ctx, cfg) }()
	select {
	case <-ready:
	case e := <-done:
		cancel()
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("start timeout")
	}
	proxy, _ := url.Parse("http://aiprouter:" + NativeProxyPassword(cfg.Token) + "@" + addr)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	stop := func() {
		cancel()
		tr.CloseIdleConnections()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("shutdown timeout")
		}
	}
	return cfg, client, stop
}
func TestNativeTLSGatewayCredentialsAndRefusal(t *testing.T) {
	var calls atomic.Int32
	cfg, client, stop := nativeRuntimeFixture(t, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != "https://router.test/v1/messages" || r.Header.Get("Authorization") != "Bearer router-key" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-Native-Account") != "" {
			t.Error("native credential or destination crossed gateway boundary")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "{}" {
			t.Error("request body changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: ok\n\n")), ContentLength: -1}, nil
	}))
	defer stop()
	r, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages?native_secret=discard", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer native-secret")
	r.Header.Set("Cookie", "native-session")
	r.Header.Set("X-Api-Key", "native-key")
	r.Header.Set("X-Native-Account", "native-account")
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != "data: ok\n\n" {
		t.Fatalf("bad gateway response: %d %q", res.StatusCode, b)
	}
	r, _ = http.NewRequest("POST", "https://api.anthropic.com/admin", strings.NewReader("{}"))
	res, e = client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 || calls.Load() != 1 {
		t.Fatal("off-route dispatch")
	}
	if _, e = client.Get("https://example.com/"); e == nil {
		t.Fatal("off-host CONNECT accepted")
	}
	raw, e := net.Dial("tcp", cfg.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	io.WriteString(raw, "CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\n\r\n")
	unauth, e := http.ReadResponse(bufio.NewReader(raw), nil)
	if e != nil || unauth.StatusCode != 407 {
		t.Fatal("unauthenticated CONNECT accepted")
	}
	unauth.Body.Close()
}
func TestNativeTLSUpstreamCredentialsStayAtOriginalHost(t *testing.T) {
	_, client, stop := nativeRuntimeFixture(t, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "claude.ai" || r.Header.Get("Authorization") != "Bearer native-session" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("upstream credential boundary")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"unknown":"preserved"}`))}, nil
	}))
	defer stop()
	r, _ := http.NewRequest("GET", "https://claude.ai/api/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer native-session")
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != `{"unknown":"preserved"}` {
		t.Fatalf("response changed %q", b)
	}
}
func TestNativeTLSControlAndOwnedShutdown(t *testing.T) {
	cfg, _, stop := nativeRuntimeFixture(t, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected transport"); return nil, nil }))
	defer stop()
	control := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	res, e := control.Get("http://" + cfg.Address + "/_aiprouter/native/stop")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("unowned control accepted")
	}
	if _, e = nativeControlRequest(context.Background(), cfg.Address, cfg.Token, "GET", "/_aiprouter/native/health"); e != nil {
		t.Fatal(e)
	}
	raw, e := net.Dial("tcp", cfg.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	io.WriteString(raw, "CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\nProxy-Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("aiprouter:"+NativeProxyPassword(cfg.Token)))+"\r\n\r\n")
	br := bufio.NewReader(raw)
	res, e = http.ReadResponse(br, nil)
	if e != nil || res.StatusCode != 200 {
		t.Fatal("CONNECT setup failed")
	}
	if _, e = nativeControlRequest(context.Background(), cfg.Address, cfg.Token, "POST", "/_aiprouter/native/stop"); e != nil {
		t.Fatal(e)
	}
	raw.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = br.ReadByte(); e == nil {
		t.Fatal("owned CONNECT survived stop")
	}
}

func TestNativeTLSCodexPACIsLifetimeBoundAndDestinationRestricted(t *testing.T) {
	options := nativeTestOptions(t)
	options.DNSNames = []string{"chatgpt.com"}
	ca, e := EnsureNativeCAWithOptions(realTemp(t), options)
	if e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := ln.Addr().String()
	ln.Close()
	cfg := NativeTLSConfig{Address: address, Policy: nativePolicyFixture(t, NativeCompatibilityCodexObserve), CA: ca, Gateway: "https://router.test", Key: "router-key", Token: strings.Repeat("b", 64)}
	ready := make(chan struct{})
	cfg.OnReady = func() { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunNativeTLS(ctx, cfg) }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		t.Fatal("start timeout")
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	body, e := nativeControlRequest(context.Background(), address, cfg.Token, "GET", "/_aiprouter/native/launch")
	if e != nil {
		t.Fatal(e)
	}
	var info struct {
		PACURL string `json:"pacURL"`
	}
	if json.Unmarshal(body, &info) != nil {
		t.Fatal("launch descriptor")
	}
	if validateNativePAC(info.PACURL, address) != nil {
		t.Fatal("invalid PAC descriptor")
	}
	res, e := client.Get(info.PACURL)
	if e != nil {
		t.Fatal(e)
	}
	script, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(script), "chatgpt.com") || !strings.Contains(string(script), "; DIRECT") || strings.Contains(string(script), cfg.Token) || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("PAC authority or lifetime")
	}
	conn, e := net.Dial("tcp", address)
	if e != nil {
		t.Fatal(e)
	}
	io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	r, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e != nil || r.StatusCode != 403 {
		t.Fatal("PAC enabled broad unauth proxy")
	}
	r.Body.Close()
	conn.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PAC listener did not stop")
	}
	if _, e = client.Get(info.PACURL); e == nil {
		t.Fatal("PAC survived owner stop")
	}
}
func TestNativeTLSHeaderBound(t *testing.T) {
	_, e := nativeReadRequest(bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\nX-Large: " + strings.Repeat("a", 40<<10) + "\r\n\r\n")))
	if e == nil {
		t.Fatal("oversized header accepted")
	}
}

func TestNativeTLSDisconnectCancelsUpstream(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	_, client, stop := nativeRuntimeFixture(t, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
		return nil, r.Context().Err()
	}))
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", strings.NewReader("{}"))
	done := make(chan struct{})
	go func() {
		res, _ := client.Do(r)
		if res != nil {
			res.Body.Close()
		}
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream never entered")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnected downstream retained upstream inference")
	}
	<-done
}
func TestNativeTLSControlProtocolMatchesController(t *testing.T) {
	cfg, _, stop := nativeRuntimeFixture(t, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled }))
	defer stop()
	c := &nativeRuntimeController{}
	_, port, _ := net.SplitHostPort(cfg.Address)
	n, _ := strconv.Atoi(port)
	record := nativeRuntimeRecord{Token: cfg.Token, Fingerprint: cfg.CA.Fingerprint, Request: NativeRuntimeRequest{Port: n}}
	if e := c.control(context.Background(), record, "GET", "health"); e != nil {
		t.Fatal(e)
	}
	if e := c.control(context.Background(), record, "POST", "stop"); e != nil {
		t.Fatal(e)
	}
}

func TestNativeTLSUsageStreamDropsCacheValidators(t *testing.T) {
	cfg := NativeTLSConfig{Policy: nativePolicyFixture(t, NativeCompatibilityCodexObserve)}
	request, _ := http.NewRequest("GET", "https://chatgpt.com/backend-api/wham/usage/stream", nil)
	request.Body = io.NopCloser(strings.NewReader(""))
	upstream := nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"public, max-age=600"}, "Digest": []string{"sha256=old"}, "Last-Modified": []string{"yesterday"}}, Body: io.NopCloser(strings.NewReader(": heartbeat\n\n")), ContentLength: -1}, nil
	})
	var output bytes.Buffer
	if e := nativeForwardResponse(context.Background(), &output, request, cfg, &url.URL{}, upstream); e != nil {
		t.Fatal(e)
	}
	res, e := http.ReadResponse(bufio.NewReader(&output), request)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Digest") != "" || res.Header.Get("Last-Modified") != "" || string(body) != ": heartbeat\n\n" {
		t.Fatal("stream headers or bytes changed incorrectly")
	}
}
