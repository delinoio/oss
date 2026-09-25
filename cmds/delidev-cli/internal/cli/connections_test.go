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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLIConnectionCommandsPreservePrivatePairingAndRequireExplicitScope(t *testing.T) {
	serverRoot := filepath.Join(t.TempDir(), "server")
	root := filepath.Join(t.TempDir(), "desktop")
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: serverRoot, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	code, output := cliRun(t, root, []string{"connection", "list"}, "")
	if code != 0 {
		t.Fatal(output)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("list started a server or initialized client state")
	}
	code, output = cliRun(t, serverRoot, []string{"device", "create-pairing", "--type", "client", "--name", "Saved client"}, "")
	if code != 0 {
		t.Fatal(output)
	}
	raw, err := os.ReadFile(output["result"].(map[string]any)["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var grant worker.PairingCode
	if err := json.Unmarshal(raw, &grant); err != nil {
		t.Fatal(err)
	}
	id := string(domain.NewID())
	pair := []string{"connection", "pair", "--id", id, "--name", "Personal server", "--code-stdin"}
	code, output = cliRun(t, root, pair, string(raw))
	if code != 0 {
		t.Fatal(output)
	}
	metadata := output["result"].(map[string]any)
	if metadata["state"] != "paired" || metadata["server_id"] != string(grant.ServerID) {
		t.Fatal(metadata)
	}
	for _, args := range [][]string{{"connection", "inspect", "--id", id}, {"connection", "retry", "--id", id}, {"connection", "verify", "--id", id}, {"connection", "list"}} {
		code, output := cliRun(t, root, args, "")
		if code != 0 {
			t.Fatal(output)
		}
		encoded, _ := json.Marshal(output)
		if strings.Contains(string(encoded), grant.Code) || strings.Contains(string(encoded), `"token"`) {
			t.Fatal("secret escaped connection output")
		}
	}
	for _, args := range [][]string{{"--server", grant.Endpoint, "connection", "list"}, {"--token-stdin", "connection", "verify", "--id", id}, {"connection", "pair", "--id", id, "--name", "replacement"}} {
		if code, output := cliRun(t, root, args, ""); code != 2 {
			t.Fatal("invalid connection authority/input accepted", output)
		}
	}
	for _, name := range []string{"owner.json", "server.json", "state.sqlite"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("client profile created server authority")
		}
	}
}
