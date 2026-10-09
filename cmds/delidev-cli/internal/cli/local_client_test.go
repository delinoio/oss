package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	"connectrpc.com/connect"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLILocalClientPairingRetainsIdentityAndNeverReplacesRevocation(t *testing.T) {
	for _, kind := range []string{"device", "worker"} {
		t.Run(kind, func(t *testing.T) { testLocalPairing(t, kind) })
	}
}
func testLocalPairing(t *testing.T, command string) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not become ready")
	}
	clientRoot := filepath.Join(root, "desktop-client")
	flag := "--device-dir"
	if command == "worker" {
		flag = "--worker-dir"
	}
	args := []string{command, "pair-local", flag, clientRoot}
	code, first := cliRun(t, root, args, "")
	if code != 0 {
		t.Fatal(first)
	}
	saved, err := worker.LoadCredential(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := security.LoadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token == owner.Token {
		t.Fatal("owner token escaped into client")
	}
	for _, command := range [][]string{args, {command, "inspect", flag, clientRoot}} {
		code, result := cliRun(t, root, command, "")
		if code != 0 || result["result"].(map[string]any)["device_id"] != string(saved.DeviceID) {
			t.Fatal(result)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), saved.Token) || strings.Contains(string(raw), owner.Token) || strings.Contains(string(raw), `"token"`) {
			t.Fatal("secret in metadata")
		}
	}
	other, otherFlag := "worker", "--worker-dir"
	if command == "worker" {
		other, otherFlag = "device", "--device-dir"
	}
	if code, result := cliRun(t, root, []string{other, "inspect", otherFlag, clientRoot}, ""); code == 0 {
		t.Fatal("wrong device type accepted", result)
	}
	if code, result := cliRun(t, root, []string{"device", "pair-local", "--device-dir", root}, ""); code == 0 {
		t.Fatal("owner scope replaced", result)
	}
	code, result := cliRun(t, root, []string{"device", "revoke", "--id", string(saved.DeviceID), "--revision", "1"}, "")
	if code != 0 {
		t.Fatal(result)
	}
	if code, result = cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "unauthenticated" {
		t.Fatal("revoked client replaced", result)
	}
	retained, err := worker.LoadCredential(clientRoot)
	if err != nil || retained != saved {
		t.Fatal("revoked identity changed", err)
	}
	// Interrupted local metadata publication must retain the original grant and
	// PairDevice receipt, not create another device on the next process start.
	if err := os.Remove(filepath.Join(clientRoot, "device.json")); err != nil {
		t.Fatal(err)
	}
	if code, result = cliRun(t, root, args, ""); code == 0 {
		t.Fatal("revoked receipt restored authority", result)
	}
}

func TestCLILocalPairingDoesNotStartServerOrCreateMissingScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	code, _ := cliRun(t, root, []string{"device", "pair-local", "--device-dir", filepath.Join(root, "client")}, "")
	if code == 0 {
		t.Fatal("missing server accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("implicit server scope creation", err)
	}
}

func TestCLILocalWorkerNameAndOriginalAcknowledgmentReplay(t *testing.T) {
	for _, pinnedName := range []string{"This computer", "", "My workstation"} {
		t.Run("name="+pinnedName, func(t *testing.T) {
			root, workerRoot := filepath.Join(t.TempDir(), "server"), filepath.Join(t.TempDir(), "paired-worker")
			ctx, cancel := context.WithCancel(context.Background())
			ready, done := make(chan server.Endpoint, 1), make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(endpoint server.Endpoint) { ready <- endpoint })
			}()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			var endpoint server.Endpoint
			select {
			case endpoint = <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("server did not become ready")
			}
			originalURL := endpoint.URL
			httpClient, transport := rpc.HTTPClient()
			defer transport.CloseIdleConnections()
			var mu sync.Mutex
			requests := map[string][][]byte{}
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == delidevv1connect.DeviceServiceCreatePairingProcedure {
					intentRaw, err := security.ReadPrivate(filepath.Join(workerRoot, "local-pairing.json"), 4096)
					var intent localPairingAttempt
					if err != nil || json.Unmarshal(intentRaw, &intent) != nil || intent.Name != pinnedName {
						t.Error("name was not pinned before grant issuance")
					}
				}
				raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				forward, _ := http.NewRequestWithContext(r.Context(), r.Method, originalURL+r.URL.Path, bytes.NewReader(raw))
				forward.Header = r.Header.Clone()
				response, err := httpClient.Do(forward)
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				mu.Lock()
				lose := false
				if r.URL.Path == delidevv1connect.DeviceServiceCreatePairingProcedure || r.URL.Path == delidevv1connect.DeviceServicePairDeviceProcedure {
					requests[r.URL.Path] = append(requests[r.URL.Path], raw)
					lose = len(requests[r.URL.Path]) == 1
				}
				mu.Unlock()
				if lose {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				for name, values := range response.Header {
					for _, value := range values {
						w.Header().Add(name, value)
					}
				}
				w.WriteHeader(response.StatusCode)
				_, _ = w.Write(body)
			}))
			defer proxy.Close()
			endpoint.URL = proxy.URL
			raw, _ := json.Marshal(endpoint)
			if err := security.WriteAtomic(filepath.Join(root, "server.json"), raw); err != nil {
				t.Fatal(err)
			}
			expectedName := pinnedName
			if pinnedName != "This computer" {
				if err := security.PrivateDir(workerRoot); err != nil {
					t.Fatal(err)
				}
				attempt := localPairingAttempt{RequestID: domain.NewID(), ServerID: endpoint.ServerID, Endpoint: proxy.URL, Name: pinnedName}
				raw, _ := json.Marshal(attempt)
				if err := security.WriteAtomic(filepath.Join(workerRoot, "local-pairing.json"), raw); err != nil {
					t.Fatal(err)
				}
				if pinnedName == "" {
					expectedName = "DeliDev local Worker"
				}
			}
			args := []string{"worker", "pair-local", "--worker-dir", workerRoot}
			for i := 0; i < 2; i++ {
				if code, _ := cliRun(t, root, args, ""); code == 0 {
					t.Fatal("lost acknowledgment reported complete")
				}
			}
			// The original machine JSON remains in the lower-level pending journal.
			pending, err := os.ReadFile(filepath.Join(workerRoot, "pairing-pending.json"))
			if err != nil {
				t.Fatal(err)
			}
			if code, result := cliRun(t, root, args, ""); code != 0 {
				t.Fatal(result)
			}
			saved, err := worker.LoadCredential(workerRoot)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := security.LoadIdentity(root)
			if err != nil {
				t.Fatal(err)
			}
			resources := delidevv1connect.NewResourceServiceClient(httpClient, originalURL)
			for kind, id := range map[pb.EntityKind]domain.ID{pb.EntityKind_ENTITY_KIND_DEVICE: saved.DeviceID, pb.EntityKind_ENTITY_KIND_MACHINE: saved.MachineID} {
				req := connect.NewRequest(&pb.GetResourceRequest{Kind: kind, Id: string(id)})
				req.Header().Set("Authorization", "Bearer "+identity.Token)
				response, err := resources.GetResource(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				var document struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(response.Msg.Resource.DocumentJson, &document); err != nil || document.Name != expectedName {
					t.Fatalf("%v name = %q, want %q: %v", kind, document.Name, expectedName, err)
				}
			}
			if code, result := cliRun(t, root, args, ""); code != 0 {
				t.Fatal(result)
			}
			repeated, err := worker.LoadCredential(workerRoot)
			if err != nil || repeated != saved {
				t.Fatal("completed credentials changed", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatal("both acknowledgment boundaries were not tested")
			}
			for procedure, values := range requests {
				expected := 2
				if procedure == delidevv1connect.DeviceServiceCreatePairingProcedure {
					expected = 3
				}
				if len(values) != expected {
					t.Fatalf("%s replay count = %d, want %d", procedure, len(values), expected)
				}
				for _, value := range values[1:] {
					if !bytes.Equal(values[0], value) {
						t.Fatal("replay changed original pairing bytes")
					}
				}
			}
			var journal map[string]any
			if err := json.Unmarshal(pending, &journal); err != nil || len(journal) == 0 {
				t.Fatal("original pending journal missing", err)
			}
			// Exact PairDevice bytes include the retained machine document, request,
			// verifier and identities, so a replay cannot create another resource.
		})
	}
}
