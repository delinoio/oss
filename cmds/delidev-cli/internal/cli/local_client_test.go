package cli

import (
	"context"
	"encoding/json"
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
