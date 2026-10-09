// SPDX-License-Identifier: Apache-2.0
package outbound_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestDirectAndBypassPinLocalhostBeforeCallerDialer(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyDirect, domain.ProxyHTTP} {
		for _, family := range []string{"tcp4", "tcp6"} {
			t.Run(string(mode)+"/"+family, func(t *testing.T) {
				address := "127.0.0.1:0"
				if family == "tcp6" {
					address = "[::1]:0"
				}
				listener, err := net.Listen(family, address)
				if err != nil {
					t.Skip("loopback family unavailable", err)
				}
				origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer synthetic-fixture" || !strings.HasPrefix(r.Host, "localhost:") {
						t.Error("original request changed")
					}
					io.WriteString(w, "ok")
				}))
				origin.Listener.Close()
				origin.Listener = listener
				origin.Start()
				defer origin.Close()
				_, port, _ := net.SplitHostPort(listener.Addr().String())
				profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: mode}}
				if mode == domain.ProxyHTTP {
					profile.Host = "127.0.0.1"
					profile.Port = 1
					profile.Bypass = []domain.ProxyBypass{{Host: "localhost"}}
				}
				var calls atomic.Int32
				base := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					calls.Add(1)
					host, _, err := net.SplitHostPort(address)
					ip := net.ParseIP(host)
					if err != nil || ip == nil || !ip.IsLoopback() {
						t.Errorf("unresolved or nonlocal dial: %s", address)
						return nil, errors.New("unsafe destination")
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}}
				client := &http.Client{Transport: &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) { return profile, nil, nil }}}
				req, _ := http.NewRequest("GET", "http://localhost:"+port+"/check", nil)
				req.Header.Set("Authorization", "Bearer synthetic-fixture")
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(response.Body)
				response.Body.Close()
				want := int32(1)
				if family == "tcp6" {
					want = 2
				}
				if err != nil || string(raw) != "ok" || calls.Load() != want {
					t.Fatal("dual-family routing changed", err, calls.Load())
				}
			})
		}
	}
}

func TestLocalhostDialCancellationStopsFamilyFallback(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	joined := make(chan struct{})
	base := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		calls.Add(1)
		if address != "127.0.0.1:12345" {
			t.Errorf("unexpected destination %s", address)
		}
		close(started)
		<-ctx.Done()
		close(joined)
		return nil, ctx.Err()
	}}
	profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Direct", Mode: domain.ProxyDirect}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) { return profile, nil, nil }}}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:12345/check", nil)
	done := make(chan error, 1)
	go func() { _, err := client.Do(req); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("caller dialer did not join")
	}
	if calls.Load() != 1 {
		t.Fatal("cancellation tried another family")
	}
}

func TestNonLocalDirectDestinationRetainsCallerDNS(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyDirect, domain.ProxyHTTP} {
		t.Run(string(mode), func(t *testing.T) {
			profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: mode}}
			if mode == domain.ProxyHTTP {
				profile.Host = "127.0.0.1"
				profile.Port = 1
				profile.Bypass = []domain.ProxyBypass{{Host: "provider.invalid"}}
			}
			var calls atomic.Int32
			base := &http.Transport{DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
				calls.Add(1)
				if address != "provider.invalid:80" {
					t.Errorf("nonlocal authority changed: %s", address)
				}
				return nil, errors.New("fixture DNS rejection")
			}}
			client := &http.Client{Transport: &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) { return profile, nil, nil }}}
			if _, err := client.Get("http://provider.invalid/check"); err == nil || calls.Load() != 1 {
				t.Fatal("ordinary DNS behavior changed", err)
			}
		})
	}
}

func TestLocalhostPinPreservesDestinationTLSHostname(t *testing.T) {
	origin, roots := outboundtest.TLSOrigin(t, "localhost", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.ServerName != "localhost" {
			t.Error("original TLS hostname lost")
		}
		io.WriteString(w, "tls-ok")
	}))
	_, port, _ := net.SplitHostPort(origin.Listener.Addr().String())
	base := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			t.Errorf("unresolved TLS destination %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Direct", Mode: domain.ProxyDirect}}
	client := &http.Client{Transport: &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) { return profile, nil, nil }}}
	response, err := client.Get("https://localhost:" + port + "/check")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(raw) != "tls-ok" {
		t.Fatal("TLS verification changed", err)
	}
}
