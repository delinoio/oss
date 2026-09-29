package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLINetworkProfilesSelectionAndExactRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
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
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness")
	}
	code, value := cliRun(t, root, []string{"network", "status"}, "")
	if code != 0 || value["result"].(map[string]any)["mode"] != "direct" {
		t.Fatal("default", value)
	}
	requestID := string(domain.NewID())
	args := []string{"network", "profile", "save", "--request-id", requestID, "--input", "-"}
	raw := `{"name":"Direct profile","mode":"direct"}`
	code, value = cliRun(t, root, args, raw)
	if code != 0 {
		t.Fatal("save", value)
	}
	id := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	code, value = cliRun(t, root, args, raw)
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("retry", value)
	}
	for _, args := range [][]string{{"network", "profile", "get", "--id", id}, {"network", "profile", "list"}} {
		if code, value := cliRun(t, root, args, ""); code != 0 {
			t.Fatal("read", value)
		}
	}
	code, value = cliRun(t, root, []string{"network", "select", "--profile-id", id, "--profile-revision", "1"}, "")
	if code != 0 {
		t.Fatal("selection", value)
	}
	route := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	if code, value := cliRun(t, root, []string{"network", "profile", "delete", "--id", id, "--revision", "1"}, ""); code == 0 {
		t.Fatal("deleted selected", value)
	}
	if code, value := cliRun(t, root, []string{"network", "select", "--id", route, "--revision", "1"}, ""); code != 0 {
		t.Fatal("Direct selection", value)
	}
	// Shared stdin cannot ambiguously carry definition, server token and proxy
	// credentials. Reject it locally before any secret/native mutation.
	if code, value := cliRun(t, root, []string{"network", "profile", "save", "--input", "-", "--credential-stdin"}, raw); code == 0 {
		t.Fatal("ambiguous stdin", value)
	}
}
