// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkGitHubUsesProxyWithIndependentDestinationTLS(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			origin, roots := outboundtest.TLSOrigin(t, "api.github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer github-fixture-token" || r.Host != "api.github.com" {
					t.Error("authority/credential separation")
				}
				io.WriteString(w, `{"id":17,"node_id":"U_17","login":"fixture-user","type":"User"}`)
			}))
			fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			}
			c := New(fixture.Resolver())
			base := c.http.Transport.(*outbound.Transport).Base
			base.TLSClientConfig.RootCAs = roots
			base.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("direct denied") }
			result, err := c.Identity(context.Background(), []byte("github-fixture-token"))
			if err != nil || result.State != domain.IntegrationIdentityVerified || fixture.Calls.Load() != 1 {
				t.Fatal("GitHub routing", result, err)
			}
			if target := <-fixture.Targets; target != "api.github.com:443" {
				t.Fatal(target)
			}
		})
	}
}

func TestNetworkGitHubRedirectRefusalAndJoinedCancellation(t *testing.T) {
	for _, socks := range []bool{false, true} {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, 0} {
			t.Run(fmt.Sprintf("socks=%t/status=%d", socks, status), func(t *testing.T) {
				var redirected, calls, direct atomic.Int32
				recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
				defer recorder.Close()
				started, stopped := make(chan struct{}), make(chan struct{})
				origin, roots := outboundtest.TLSOrigin(t, "api.github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					defer close(stopped)
					if r.Host != "api.github.com" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer github-fixture-token" {
						t.Error("credential authority changed")
					}
					close(started)
					if status == 0 {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Location", recorder.URL)
					w.WriteHeader(status)
				}))
				fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
				if socks {
					fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
				}
				client := New(fixture.Resolver())
				base := client.http.Transport.(*outbound.Transport).Base
				base.TLSClientConfig.RootCAs = roots
				base.DialContext = func(context.Context, string, string) (net.Conn, error) {
					direct.Add(1)
					return nil, errors.New("direct denied")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				var result IdentityObservation
				var err error
				go func() { defer close(done); result, err = client.Identity(ctx, []byte("github-fixture-token")) }()
				outboundtest.Wait(t, started, "GitHub request was not transmitted")
				if status == 0 {
					cancel()
				}
				outboundtest.Wait(t, done, "GitHub cancellation did not join")
				outboundtest.Wait(t, stopped, "origin connection remained active")
				if status == 0 && domain.SafeError(err).Code != domain.Canceled || status != 0 && (err != nil || result.State != domain.IntegrationUnavailable) || fixture.Calls.Load() != 1 || calls.Load() != 1 || direct.Load() != 0 || redirected.Load() != 0 {
					t.Fatalf("route/retry/cancellation mismatch: %+v %v proxy=%d origin=%d direct=%d redirected=%d", result, err, fixture.Calls.Load(), calls.Load(), direct.Load(), redirected.Load())
				}
			})
		}
	}
}
