// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
)

func TestExternalHTTPClientUsesSelectedRouteAndIgnoresAmbientProxy(t *testing.T) {
	origin, _ := outboundtest.TLSOrigin(t, "oauth.invalid", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted OAuth destination received traffic")
	}))
	proxy := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
	var ambient atomic.Int32
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ambient.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer decoy.Close()
	t.Setenv("HTTPS_PROXY", decoy.URL)
	t.Setenv("HTTP_PROXY", decoy.URL)
	t.Setenv("https_proxy", decoy.URL)
	t.Setenv("http_proxy", decoy.URL)
	runtime := &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{Route: domain.NetworkRoute{Profile: proxy.Profile}, Credential: &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password}}}}
	client, closeClient, err := externalHTTPClient(context.Background(), Config{Root: t.TempDir(), network: runtime}, Credential{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	request, _ := http.NewRequest(http.MethodGet, "https://oauth.invalid/.well-known/oauth-protected-resource", nil)
	// Destination TLS verification rejects the fixture certificate; the selected
	// proxy must still be the only route attempted.
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("fixture certificate was trusted")
	}
	if proxy.Calls.Load() != 1 || ambient.Load() != 0 {
		t.Fatal("OAuth client bypassed the selected route", proxy.Calls.Load(), ambient.Load())
	}
	if target := <-proxy.Targets; target != "oauth.invalid:443" {
		t.Fatal("wrong OAuth destination", target)
	}
}

func TestExternalHTTPClientWithoutRouteNeverUsesAmbientProxy(t *testing.T) {
	var ambient atomic.Int32
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ambient.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer decoy.Close()
	t.Setenv("HTTP_PROXY", decoy.URL)
	t.Setenv("http_proxy", decoy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	client, closeClient, err := externalHTTPClient(context.Background(), Config{Root: t.TempDir()}, Credential{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	// Go skips ambient proxies for loopback unless a non-loopback name is used.
	request, _ := http.NewRequest(http.MethodGet, "http://localtest.invalid/", nil)
	_, _ = client.Do(request)
	if ambient.Load() != 0 {
		t.Fatal("ambient proxy was used for OAuth traffic")
	}
}
