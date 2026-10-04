// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
)

func TestForwardLaneRoutesCleanupAndControlThroughOriginalNetwork(t *testing.T) {
	origin, _ := outboundtest.TLSOrigin(t, "forward.invalid", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted destination received authenticated forward traffic")
	}))
	proxy := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
	credential := &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password}
	runtime := &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{Route: domain.NetworkRoute{Profile: proxy.Profile}, Credential: credential}}}
	root := t.TempDir()
	machine := domain.NewID()
	runtimeID := domain.NewID()
	cleanup := map[string]any{"version": 1, "endpoint": "https://forward.invalid", "peer": map[string]any{"forward_id": domain.NewID(), "session_id": domain.NewID(), "runtime_id": runtimeID, "machine_id": machine, "instance_id": domain.NewID()}, "report_id": domain.NewID(), "clean": true}
	raw, _ := json.Marshal(cleanup)
	if err := security.PrivateDir(filepath.Join(root, "forward-cleanup")); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(root, "forward-cleanup", string(runtimeID)+".json"), raw); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	// Both independent RPC clients reach the selected CONNECT proxy. Independent
	// destination TLS verification must still refuse the fixture certificate.
	err := watchForwards(ctx, Config{Root: root, network: runtime, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, Credential{Endpoint: "https://forward.invalid", Token: "forward-token-sentinel", MachineID: machine}, domain.NewID())
	if err != nil || proxy.Calls.Load() != 2 {
		t.Fatal("forward cleanup/control bypassed selected route or failed to join", err, proxy.Calls.Load())
	}
	for i := 0; i < 2; i++ {
		if target := <-proxy.Targets; target != "forward.invalid:443" {
			t.Fatal("wrong forward destination", target)
		}
	}
	if !reflect.DeepEqual(runtime.current().bundle.Route.Profile, proxy.Profile) {
		t.Fatal("forward lane changed original network authority")
	}
}
