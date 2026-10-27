// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func nativeStartRegression(t *testing.T, cfg NativeTLSConfig) (NativeTLSConfig, <-chan error, context.CancelFunc) {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Address = ln.Addr().String()
	ln.Close()
	if cfg.Token == "" {
		cfg.Token = strings.Repeat("d", 64)
	}
	cfg.Gateway = "https://router.test"
	cfg.Key = "router-key"
	started := make(chan struct{})
	cfg.OnReady = func() { close(started) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunNativeTLS(ctx, cfg) }()
	select {
	case <-started:
	case e := <-done:
		cancel()
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("runtime did not bind")
	}
	return cfg, done, cancel
}
func nativeCodexRegressionCA(t *testing.T) *NativeCA {
	t.Helper()
	opts := nativeTestOptions(t)
	opts.DNSNames = []string{"chatgpt.com"}
	ca, e := EnsureNativeCAWithOptions(realTemp(t), opts)
	if e != nil {
		t.Fatal(e)
	}
	return ca
}
func nativeRegressionTLSClient(t *testing.T, cfg NativeTLSConfig) *http.Client {
	t.Helper()
	proxy, _ := url.Parse("http://" + cfg.Address)
	roots := x509.NewCertPool()
	roots.AddCert(cfg.CA.Certificate)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 3 * time.Second}
}
func nativeVerifiedTestTransport(t *testing.T, ca *NativeCA, upstream string) *http.Transport {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "chatgpt.com:443" {
			t.Errorf("unexpected fixed-origin destination %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream)
	}, DisableCompression: true}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}
func TestNativeTLSCodexPreservesFixedOriginHTTP(t *testing.T) {
	ca := nativeCodexRegressionCA(t)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "chatgpt.com" || r.Header.Get("Authorization") != "Bearer native" || r.Header.Get("Cookie") != "native-cookie" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("native credential/fixed-origin boundary")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/backend-api/me", 307)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Set-Cookie", "native-result")
		w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " " + string(body)))
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{ca.TLSCertificate}}
	upstream.StartTLS()
	defer upstream.Close()
	cfg, done, cancel := nativeStartRegression(t, NativeTLSConfig{Policy: nativePolicyFixture(t, NativeCompatibilityCodexObserve), CA: ca, Transport: nativeVerifiedTestTransport(t, ca, upstream.Listener.Addr().String())})
	defer func() { cancel(); <-done }()
	client := nativeRegressionTLSClient(t, cfg)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, item := range []struct{ method, path, body string }{{"GET", "/backend-api/codex/models?x=1", ""}, {"POST", "/backend-api/codex/responses", `{"input":"fixture"}`}, {"GET", "/backend-api/me", ""}, {"POST", "/unmodified/%65ncoded", `data`}} {
		req, _ := http.NewRequest(item.method, "https://chatgpt.com"+item.path, strings.NewReader(item.body))
		req.Header.Set("Authorization", "Bearer native")
		req.Header.Set("Cookie", "native-cookie")
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || string(body) != item.method+" "+item.path+" "+item.body || res.Header.Get("Set-Cookie") != "native-result" {
			t.Fatalf("native HTTP altered: %d %q", res.StatusCode, body)
		}
	}
	req, _ := http.NewRequest("GET", "https://chatgpt.com/redirect", nil)
	req.Header.Set("Authorization", "Bearer native")
	req.Header.Set("Cookie", "native-cookie")
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 307 || res.Header.Get("Location") != "/backend-api/me" {
		t.Fatal("native redirect was not preserved")
	}
	if _, e = client.Get("https://example.com/backend-api/me"); e == nil {
		t.Fatal("arbitrary host admitted")
	}
}
func TestNativeTLSCodexPreservesUpgradeAndBufferedBytes(t *testing.T) {
	ca := nativeCodexRegressionCA(t)
	upstreamDone := make(chan struct{})
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		if r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer native" || r.Header.Get("Upgrade") != "websocket" {
			t.Error("upgrade request altered")
		}
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		io.WriteString(b, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		b.Flush()
		payload := make([]byte, 4)
		if _, e = io.ReadFull(b, payload); e != nil {
			t.Error(e)
			return
		}
		c.Write(payload)
		var after [1]byte
		_, _ = c.Read(after[:])
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{ca.TLSCertificate}}
	upstream.StartTLS()
	defer upstream.Close()
	cfg, done, cancel := nativeStartRegression(t, NativeTLSConfig{Policy: nativePolicyFixture(t, NativeCompatibilityCodexObserve), CA: ca, Transport: nativeVerifiedTestTransport(t, ca, upstream.Listener.Addr().String())})
	defer func() { cancel(); <-done }()
	raw, e := net.Dial("tcp", cfg.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(raw, "CONNECT chatgpt.com:443 HTTP/1.1\r\nHost: chatgpt.com:443\r\n\r\n")
	br := bufio.NewReader(raw)
	res, e := http.ReadResponse(br, nil)
	if e != nil || res.StatusCode != 200 {
		t.Fatal("CONNECT failed")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	secure := tls.Client(&nativeBufferedConn{Conn: raw, reader: br}, &tls.Config{RootCAs: roots, ServerName: "chatgpt.com"})
	if e = secure.Handshake(); e != nil {
		t.Fatal(e)
	}
	io.WriteString(secure, "GET /backend-api/codex/responses HTTP/1.1\r\nHost: chatgpt.com\r\nAuthorization: Bearer native\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\nping")
	reader := bufio.NewReader(secure)
	res, e = http.ReadResponse(reader, nil)
	if e != nil || res.StatusCode != 101 {
		t.Fatalf("upgrade failed %v %v", res, e)
	}
	payload := make([]byte, 4)
	if _, e = io.ReadFull(reader, payload); e != nil || string(payload) != "ping" {
		t.Fatalf("upgraded bytes lost: %q %v", payload, e)
	}
	if _, e = nativeControlRequest(context.Background(), cfg.Address, cfg.Token, "POST", "/_aiprouter/native/stop"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("upgraded upstream survived owned stop")
	}
}
func TestNativeTLSStopsAtServedLeafExpiry(t *testing.T) {
	ca := nativeCodexRegressionCA(t)
	leaf := *ca.TLSCertificate.Leaf
	leaf.NotAfter = time.Now().Add(2 * time.Second).Truncate(time.Second)
	encoded, e := x509.CreateCertificate(rand.Reader, &leaf, ca.Certificate, leaf.PublicKey, ca.Signer)
	if e != nil {
		t.Fatal(e)
	}
	ca.TLSCertificate.Certificate[0] = encoded
	ca.TLSCertificate.Leaf, e = x509.ParseCertificate(encoded)
	if e != nil {
		t.Fatal(e)
	}
	cfg, done, cancel := nativeStartRegression(t, NativeTLSConfig{Policy: nativePolicyFixture(t, NativeCompatibilityCodexObserve), CA: ca})
	defer cancel()
	if _, e = nativeControlRequest(context.Background(), cfg.Address, cfg.Token, "GET", "/_aiprouter/native/health"); e != nil {
		t.Fatal(e)
	}
	// A new public leaf elsewhere does not extend the in-memory certificate lifetime.
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired leaf listener remained ready")
	}
	if _, e = nativeControlRequest(context.Background(), cfg.Address, cfg.Token, "GET", "/_aiprouter/native/health"); e == nil {
		t.Fatal("expired served leaf reported readiness")
	}
}
