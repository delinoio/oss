// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
)

func TestManagedMCPOAuthClientUsesSelectedWorkerRoute(t *testing.T) {
	origin, _ := outboundtest.TLSOrigin(t, "oauth.invalid", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted OAuth destination received traffic")
	}))
	proxy := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
	credential := &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password}
	runtime := &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{Route: domain.NetworkRoute{Profile: proxy.Profile}, Credential: credential}}}
	client, closeClient, err := managedMCPHTTPClient(context.Background(), Config{Root: t.TempDir(), network: runtime}, Credential{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://oauth.invalid/.well-known/oauth-authorization-server", nil)
	// Destination TLS verification still refuses the fixture certificate; the
	// request must nevertheless have reached the selected CONNECT proxy.
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("fixture certificate was trusted")
	}
	if proxy.Calls.Load() != 1 {
		t.Fatalf("OAuth traffic bypassed the selected route: %d", proxy.Calls.Load())
	}
	if target := <-proxy.Targets; target != "oauth.invalid:443" {
		t.Fatal("wrong OAuth destination", target)
	}
}

func TestManagedMCPOAuthClientIgnoresAmbientProxy(t *testing.T) {
	decoy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer decoy.Close()
	var connections atomic.Int32
	go func() {
		for {
			c, err := decoy.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			c.Close()
		}
	}()
	t.Setenv("HTTP_PROXY", "http://"+decoy.Addr().String())
	t.Setenv("HTTPS_PROXY", "http://"+decoy.Addr().String())
	t.Setenv("http_proxy", "http://"+decoy.Addr().String())
	t.Setenv("https_proxy", "http://"+decoy.Addr().String())
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	// Without a prepared Worker route, the client is direct and ignores the host.
	client, closeClient, err := managedMCPHTTPClient(context.Background(), Config{Root: t.TempDir()}, Credential{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://oauth.invalid/token", nil)
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
	}
	if connections.Load() != 0 {
		t.Fatal("OAuth traffic used the ambient proxy")
	}
}
