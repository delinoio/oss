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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
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

func TestManagedMCPReadOperationIsScopedToOneCallAndChecksItsIdentity(t *testing.T) {
	credential := Credential{ServerID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID()}
	instance := domain.NewID()
	build := func(edit func(*workspace.ReadRequest)) workspace.ReadRequest {
		q := &domain.ManagedMCPRequest{ServerID: credential.ServerID, MachineID: credential.MachineID, WorkerDeviceID: credential.DeviceID, WorkerInstanceID: instance, ActorID: domain.NewID(), RequestID: domain.NewID(), Action: domain.MCPList, CallbackURL: "http://localhost/callback?code=private"}
		request := workspace.ReadRequest{ID: domain.NewID(), Deadline: time.Now().Add(10 * time.Second), ManagedMCP: q}
		edit(&request)
		return request
	}
	config := Config{Root: t.TempDir()}
	for name, edit := range map[string]func(*workspace.ReadRequest){
		"no-deadline":      func(r *workspace.ReadRequest) { r.Deadline = time.Time{} },
		"expired":          func(r *workspace.ReadRequest) { r.Deadline = time.Now().Add(-time.Second) },
		"too-far":          func(r *workspace.ReadRequest) { r.Deadline = time.Now().Add(time.Hour) },
		"foreign-machine":  func(r *workspace.ReadRequest) { r.ManagedMCP.MachineID = domain.NewID() },
		"foreign-instance": func(r *workspace.ReadRequest) { r.ManagedMCP.WorkerInstanceID = domain.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, uncertain := executeManagedMCPRead(context.Background(), config, credential, instance, build(edit)); !uncertain {
				t.Fatal("a mismatched managed MCP request was executed")
			}
		})
	}
	request := build(func(*workspace.ReadRequest) {})
	document, problem, uncertain := executeManagedMCPRead(context.Background(), config, credential, instance, request)
	if uncertain || problem != nil || len(document) == 0 {
		t.Fatalf("a valid read failed: %v %v", problem, uncertain)
	}
	if request.ManagedMCP.CallbackURL != "" {
		t.Fatal("the callback URL outlived its operation")
	}
}
