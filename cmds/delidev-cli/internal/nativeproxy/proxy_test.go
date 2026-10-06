// SPDX-License-Identifier: Apache-2.0
package nativeproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

type tunnelFixture struct {
	server *httptest.Server
	dials  atomic.Int32
	refuse atomic.Bool
}

func TestOwnedNativeProxyUpstreamModesPinAuthorityAndJoinActiveTLSCancellation(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyHTTP, domain.ProxyHTTPS, domain.ProxySOCKS5} {
		t.Run(string(mode), func(t *testing.T) {
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(release)
			origin, roots := outboundtest.TLSOrigin(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
				close(canceled)
			}))
			address, direct := outboundtest.DenyDirect(t)
			var fixture *outboundtest.Fixture
			if mode == domain.ProxySOCKS5 {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			} else {
				fixture = outboundtest.Connect(t, outboundtest.Address(origin.URL), mode == domain.ProxyHTTPS)
			}
			credential := &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password}
			config := Config{Origin: "https://" + address, Profile: fixture.Profile, Credential: credential, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: fixture.Roots}}
			life, cancel := context.WithCancel(context.Background())
			defer cancel()
			p, err := Open(life, config)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			// A later profile/credential edit cannot replace this runtime's copy.
			credential.Password = "changed-after-launch"
			config.Profile.Host = "foreign.invalid"
			u, _ := url.Parse(p.NativeURL())
			transport := &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
			defer transport.CloseIdleConnections()
			done := make(chan error, 1)
			go func() {
				r, _ := http.NewRequest(http.MethodPost, "https://"+address+apiproxy.Prefix+"/responses", strings.NewReader(`{"model":"fixture"}`))
				response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(r)
				if response != nil {
					response.Body.Close()
				}
				done <- err
			}()
			outboundtest.Wait(t, entered, "native TLS request did not use its original pinned upstream")
			if fixture.Calls.Load() != 1 || direct.Load() != 0 {
				t.Fatal("native request retried or fell back to direct")
			}
			cancel()
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			outboundtest.Wait(t, canceled, "active native origin request survived owned cleanup")
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled native request succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("native copy survived joined cancellation")
			}
		})
	}
}

const upstreamUsername = "fixture-proxy-user"
const upstreamPassword = "fixture-upstream-proxy-secret"

func newTunnelFixture(t *testing.T) *tunnelFixture {
	t.Helper()
	f := &tunnelFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.dials.Add(1)
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(upstreamUsername+":"+upstreamPassword)) {
			t.Error("upstream proxy authority was replaced")
			http.Error(w, "denied", http.StatusProxyAuthRequired)
			return
		}
		if f.refuse.Load() {
			http.Error(w, "denied", http.StatusProxyAuthRequired)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		local, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer local.Close()
		_, _ = io.WriteString(local, "HTTP/1.1 200 Connection Established\r\n\r\n")
		finished := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(upstream, buffered); finished <- struct{}{} }()
		go func() { _, _ = io.Copy(local, upstream); finished <- struct{}{} }()
		<-finished
		local.Close()
		upstream.Close()
		<-finished
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *tunnelFixture) config(origin string) Config {
	u, _ := url.Parse(f.server.URL)
	port, _ := strconv.ParseUint(u.Port(), 10, 16)
	return Config{Origin: origin, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: u.Hostname(), Port: uint16(port)}, CredentialGeneration: domain.NewID()}, Credential: &domain.ProxyCredential{Username: upstreamUsername, Password: upstreamPassword}}
}

func TestOwnedNativeProxyTLSIdentityAndOriginalBytes(t *testing.T) {
	var requests, observations atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != apiproxy.Prefix+"/responses" || r.Header.Get("Authorization") != "Bearer fixture-execution-token" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("origin received changed API/proxy authority")
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"model":"fixture-model","input":"original"}` {
			t.Error("original native bytes changed")
		}
		_, _ = io.WriteString(w, "original native response")
	}))
	defer origin.Close()
	f := newTunnelFixture(t)
	config := f.config(origin.URL)
	config.Observed = func(context.Context) { observations.Add(1) }
	p, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if observations.Load() != 0 || f.dials.Load() != 0 {
		t.Fatal("listener readiness fabricated native traffic")
	}
	proxyURL, _ := url.Parse(p.NativeURL())
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.DisableKeepAlives = true
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, origin.URL+apiproxy.Prefix+"/responses", strings.NewReader(`{"model":"fixture-model","input":"original"}`))
		r.Header.Set("Authorization", "Bearer fixture-execution-token")
		return r
	}
	response, err := client.Do(request())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(raw) != "original native response" || requests.Load() != 1 || f.dials.Load() != 1 || observations.Load() != 1 {
		t.Fatal("original native route was changed or repeated", err)
	}
	wrongTrust := transport.Clone()
	wrongTrust.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "wrong-server.invalid", RootCAs: transport.TLSClientConfig.RootCAs}
	if _, err := (&http.Client{Transport: wrongTrust, Timeout: 5 * time.Second}).Do(request()); err == nil {
		t.Fatal("tunnel bypassed destination TLS identity")
	}
	if requests.Load() != 1 {
		t.Fatal("TLS rejection sent an origin request")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if connection, err := net.DialTimeout("tcp", proxyURL.Host, 200*time.Millisecond); err == nil {
		connection.Close()
		t.Fatal("cleanup retained its listener")
	}
}

func TestOwnedNativeProxyLocalEscapesAndFailureCannotDialDirect(t *testing.T) {
	f := newTunnelFixture(t)
	p, err := Open(context.Background(), f.config("https://server.invalid:444"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.NativeURL())
	for _, value := range []struct{ target, host, auth string }{
		{"server.invalid:444", "server.invalid:444", ""},
		{"server.invalid:444", "server.invalid:444", "Basic stale"},
		{"foreign.invalid:444", "foreign.invalid:444", p.localAuth},
		{"server.invalid:443", "server.invalid:443", p.localAuth},
		{"SERVER.invalid:444", "SERVER.invalid:444", p.localAuth},
		{"server.invalid.:444", "server.invalid.:444", p.localAuth},
		{"user@server.invalid:444", "user@server.invalid:444", p.localAuth},
		{"server.invalid:444", "foreign.invalid:444", p.localAuth},
	} {
		connection, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		connection.SetDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", value.target, value.host, value.auth)
		response, _ := http.ReadResponse(bufio.NewReader(connection), nil)
		connection.Close()
		if response != nil && response.StatusCode == http.StatusOK {
			t.Fatal("foreign local authority was accepted")
		}
	}
	if f.dials.Load() != 0 {
		t.Fatal("local rejection dialed upstream")
	}
	var origins, failures atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { origins.Add(1) }))
	defer origin.Close()
	f.refuse.Store(true)
	config := f.config(origin.URL)
	config.Failed = func(context.Context) { failures.Add(1) }
	refused, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	proxyURL, _ := url.Parse(refused.NativeURL())
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	if _, err := client.Post(origin.URL+apiproxy.Prefix+"/responses", "application/json", strings.NewReader(`{}`)); err == nil {
		t.Fatal("refused proxy selected direct")
	}
	if origins.Load() != 0 || f.dials.Load() != 1 || failures.Load() != 1 {
		t.Fatal("proxy made another attempt or omitted failure")
	}
}

func TestOwnedNativeProxyExactBypassAndPlainHTTPPaths(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("local credential escaped to origin")
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	defer origin.Close()
	f := newTunnelFixture(t)
	config := f.config(origin.URL)
	if p, err := Open(context.Background(), config); err == nil {
		p.Close()
		t.Fatal("plaintext relay could traverse upstream")
	}
	u, _ := url.Parse(origin.URL)
	port, _ := strconv.ParseUint(u.Port(), 10, 16)
	config.Profile.Bypass = []domain.ProxyBypass{{Host: u.Hostname(), Port: uint16(port)}}
	p, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse(p.NativeURL())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.DisableKeepAlives = true
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, path := range []string{apiproxy.Prefix + "/responses", "/foreign", apiproxy.Prefix + "/responses?foreign=true", apiproxy.Prefix + "/%72esponses"} {
		response, err := client.Post(origin.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if (response.StatusCode == http.StatusOK) != (path == apiproxy.Prefix+"/responses") {
			t.Fatal("alternate plaintext API path was accepted")
		}
	}
	if requests.Load() != 1 || f.dials.Load() != 0 {
		t.Fatal("exact bypass used another route or API path")
	}
}

func TestOwnedNativeProxyConnectionBoundAndCancellation(t *testing.T) {
	f := newTunnelFixture(t)
	life, cancel := context.WithCancel(context.Background())
	p, err := Open(life, f.config("https://server.invalid:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.NativeURL())
	var connections []net.Conn
	defer func() {
		for _, connection := range connections {
			connection.Close()
		}
	}()
	for i := 0; i < MaxConnections; i++ {
		connection, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		deadline := time.Now().Add(time.Second)
		for {
			p.mu.Lock()
			count := p.localCount
			p.mu.Unlock()
			if count == i+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("connection admission did not settle")
			}
			time.Sleep(time.Millisecond)
		}
	}
	excess, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	excess.SetDeadline(time.Now().Add(time.Second))
	_, _ = fmt.Fprintf(excess, "CONNECT server.invalid:443 HTTP/1.1\r\nHost: server.invalid:443\r\nProxy-Authorization: %s\r\n\r\n", p.localAuth)
	var first [1]byte
	if _, err := excess.Read(first[:]); err == nil {
		t.Fatal("seventeenth connection was admitted")
	}
	excess.Close()
	if f.dials.Load() != 0 {
		t.Fatal("excess connection reached upstream")
	}
	cancel()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for _, connection := range connections {
		connection.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := connection.Read(first[:]); err == nil {
			t.Fatal("cancellation retained an owned connection")
		}
	}
}
