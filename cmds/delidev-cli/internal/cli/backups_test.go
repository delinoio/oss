package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
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
		done <- err // Preserve the joined result for deferred cleanup.
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	code, value := cliRun(t, root, []string{"backup", "create", "--wait"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	creation := value["result"].(map[string]any)["job"].(map[string]any)
	id := creation["backup_id"].(string)
	if creation["state"] != "BACKUP_CREATION_STATE_SUCCEEDED" {
		t.Fatal(value)
	}
	code, value = cliRun(t, root, []string{"backup", "creation", "--id", creation["id"].(string)}, "")
	if code != 0 || value["result"].(map[string]any)["job"].(map[string]any)["backup_id"] != id {
		t.Fatal(code, value)
	}
	code, value = cliRun(t, root, []string{"backup", "creations", "--limit", "1"}, "")
	if code != 0 || len(value["result"].(map[string]any)["jobs"].([]any)) != 1 {
		t.Fatal(code, value)
	}
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
	inspection := value["result"].(map[string]any)
	metadata := inspection["backup"].(map[string]any)
	args := []string{"backup", "delete", "--id", id, "--expected-revision", metadata["revision"].(string), "--size-bytes", metadata["size_bytes"].(string), "--modified-at", metadata["modified_at"].(string), "--sha256", inspection["sha256"].(string)}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("unconfirmed permanent deletion accepted")
	}
	code, value = cliRun(t, root, append(args, "--confirm"), "")
	if code != 0 {
		t.Fatal(code, value)
	}
	job := value["result"].(map[string]any)["job"].(map[string]any)["id"]
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, value = cliRun(t, root, []string{"backup", "deletions"}, "")
		if code != 0 {
			t.Fatal(code, value)
		}
		jobs := value["result"].(map[string]any)["jobs"].([]any)
		if len(jobs) != 1 {
			t.Fatal(value)
		}
		state := jobs[0].(map[string]any)
		if state["id"] != job {
			t.Fatal(value)
		}
		if state["state"] == "BACKUP_DELETION_STATE_SUCCEEDED" {
			if _, err := strconv.ParseUint(state["image_bytes"].(string), 10, 64); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion did not complete", value)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
