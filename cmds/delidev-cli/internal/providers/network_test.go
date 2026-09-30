// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkProviderInspectionUsesExplicitProxy(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer provider-fixture-key" {
					t.Error("credential separation")
				}
				io.WriteString(w, `{"data":[{"id":"model-one"}]}`)
			}))
			defer origin.Close()
			fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			}
			p := localProvider(origin.URL)
			p.Authentication = domain.BearerAuth
			result, err := Inspect(context.Background(), p, []byte("provider-fixture-key"), fixture.Resolver())
			if err != nil || result.Problem() != nil || len(result.Models) != 1 || fixture.Calls.Load() != 1 {
				t.Fatal("provider routing", result, err)
			}
		})
	}
}

func TestNetworkProviderRedirectRefusalAndJoinedCancellation(t *testing.T) {
	for _, socks := range []bool{false, true} {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, 0} {
			t.Run(fmt.Sprintf("socks=%t/status=%d", socks, status), func(t *testing.T) {
				var redirected, calls atomic.Int32
				recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
				defer recorder.Close()
				started, stopped := make(chan struct{}), make(chan struct{})
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					defer close(stopped)
					if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer provider-fixture-key" {
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
				defer origin.Close()
				address, direct := outboundtest.DenyDirect(t)
				fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
				if socks {
					fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
				}
				provider := localProvider("http://" + address)
				provider.Authentication = domain.BearerAuth
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				var result Observation
				var err error
				go func() {
					defer close(done)
					result, err = Inspect(ctx, provider, []byte("provider-fixture-key"), fixture.Resolver())
				}()
				outboundtest.Wait(t, started, "provider request was not transmitted")
				if status == 0 {
					cancel()
				}
				outboundtest.Wait(t, done, "provider cancellation did not join")
				outboundtest.Wait(t, stopped, "origin connection remained active")
				expected := RedirectRefused
				if status == 0 {
					expected = Canceled
				}
				if err != nil || result.Failure != expected || fixture.Calls.Load() != 1 || calls.Load() != 1 || direct.Load() != 0 || redirected.Load() != 0 {
					t.Fatalf("route/retry/cancellation mismatch: %+v %v proxy=%d origin=%d direct=%d redirected=%d", result, err, fixture.Calls.Load(), calls.Load(), direct.Load(), redirected.Load())
				}
			})
		}
	}
}
