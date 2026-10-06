// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestBrowserCLIUsesAuthenticatedRPCAndKeepsBrowsingContentLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	device, account, session := domain.NewID(), domain.NewID(), domain.NewID()
	token := string(domain.NewID())
	digest := sha256.Sum256([]byte(token))
	_, err = db.Mutate(context.Background(), domain.NewID(), "browser.cli.fixture", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Browser client fixture", Type: domain.ClientDevice}); err != nil {
			return nil, err
		}
		if err := tx.PutCredential(device, digest[:]); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "Browser fixture", Health: domain.AccountDisconnected}); err != nil {
			return nil, err
		}
		return tx.Put(domain.SessionKind, session, 0, "", "", domain.Session{CurrentExecution: &domain.ExecutionSelection{AccountID: account}})
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan server.Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(e server.Endpoint) { ready <- e })
	}()
	serverFinished := false
	t.Cleanup(func() {
		stop()
		if serverFinished {
			return
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("isolated browser server shutdown timed out")
		}
	})
	var endpoint server.Endpoint
	select {
	case endpoint = <-ready:
	case err := <-done:
		serverFinished = true
		t.Fatal(err)
	case <-time.After(15 * time.Second):
		t.Fatal("isolated browser server startup timed out")
	}
	clientRoot := filepath.Join(t.TempDir(), "client")
	args := []string{"--server", endpoint.URL, "--token-stdin", "--request-id", string(domain.NewID()), "browser-profile", "register", "--id", string(session), "--revision", "1", "--account-id", string(account)}
	code, reply := cliRun(t, clientRoot, args, token)
	if code != 0 {
		t.Fatal(code, reply)
	}
	profile := reply["result"].(map[string]any)["profile"].(map[string]any)
	data := profile["data"].(map[string]any)
	if data["account_id"] != string(account) || data["device_id"] != string(device) || data["state"] != "active" {
		t.Fatal(data)
	}
	for _, key := range []string{"url", "tabs", "cookies", "history", "path", "credentials"} {
		if _, ok := data[key]; ok {
			t.Fatal("browser content entered CLI product data", key)
		}
	}
	code, replay := cliRun(t, clientRoot, args, token)
	if code != 0 || replay["result"].(map[string]any)["replayed"] != true || replay["result"].(map[string]any)["profile"].(map[string]any)["id"] != profile["id"] {
		t.Fatal(code, replay)
	}
	code, listed := cliRun(t, clientRoot, []string{"--server", endpoint.URL, "--token-stdin", "browser-profile", "list"}, token)
	if code != 0 || len(listed["result"].(map[string]any)["profiles"].([]any)) != 1 {
		t.Fatal(code, listed)
	}
	code, counts := cliRun(t, clientRoot, []string{"--server", endpoint.URL, "--token-stdin", "browser-profile", "account-status", "--account-id", string(account)}, token)
	if code != 0 || counts["result"].(map[string]any)["active"] != float64(1) {
		t.Fatal(code, counts)
	}
}
