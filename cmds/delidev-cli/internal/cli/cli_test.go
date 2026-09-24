package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func cliRun(t *testing.T, root string, args []string, input string) (int, map[string]any) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := Run(context.Background(), append([]string{"--data-dir", root}, args...), IO{In: bytes.NewBufferString(input), Out: &out, Err: &stderr})
	var value map[string]any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		t.Fatalf("not one JSON document: %q: %v", out.String(), err)
	}
	return code, value
}
func TestCommandsNeverImplicitlyStartServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uncreated")
	code, value := cliRun(t, root, []string{"project", "list"}, "")
	if code != 4 || value["error"].(map[string]any)["code"] != "server_unavailable" {
		t.Fatalf("wrong failure: %d %+v", code, value)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read command created server state")
	}
}
func TestVersionedCLIMutationRevisionAndMissingInput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
		t.Fatal("server timeout")
	}
	input := `{"name":"Ollama","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":false}`
	request := string(domain.NewID())
	args := []string{"provider", "create", "--input", "-", "--request-id", request}
	code, value := cliRun(t, root, args, input)
	if code != 0 || value["version"] != float64(1) || value["request_id"] != request {
		t.Fatalf("create failed: %d %+v", code, value)
	}
	resource := value["result"].(map[string]any)["resource"].(map[string]any)
	id := resource["id"].(string)
	code, value = cliRun(t, root, args, input)
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatalf("retry failed: %d %+v", code, value)
	}
	code, value = cliRun(t, root, []string{"provider", "edit", "--id", id, "--revision", "9"}, input)
	if code != 5 || value["error"].(map[string]any)["code"] != "conflict" {
		t.Fatalf("stale revision accepted: %d %+v", code, value)
	}
	code, value = cliRun(t, root, []string{"provider", "list"}, "")
	if code != 0 || len(value["result"].(map[string]any)["resources"].([]any)) != 1 {
		t.Fatalf("list failed: %d %+v", code, value)
	}
	code, value = cliRun(t, root, []string{"provider", "edit", "--id", id}, input)
	if code != 2 || value["error"].(map[string]any)["code"] != "missing_input" {
		t.Fatalf("missing revision accepted: %d %+v", code, value)
	}
}
