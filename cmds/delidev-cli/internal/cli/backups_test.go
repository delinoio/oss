package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestBackupCLIUsesServerInventoryAndChecksOriginalImage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	code, value := cliRun(t, root, []string{"backup", "create"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	id := value["result"].(map[string]any)["id"].(string)
	code, value = cliRun(t, root, []string{"backup", "list", "--limit", "1"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	item := value["result"].(map[string]any)["backups"].([]any)[0].(map[string]any)
	if item["id"] != id {
		t.Fatal(value)
	}
	if _, ok := item["size_bytes"].(string); !ok {
		t.Fatal("bytes lost decimal-string encoding", item)
	}
	code, value = cliRun(t, root, []string{"backup", "inspect", "--id", id}, "")
	if code != 0 || len(value["result"].(map[string]any)["sha256"].(string)) != 64 {
		t.Fatal(code, value)
	}
}
