package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIIntegrationProfilesAndStrictPATInput(t *testing.T) {
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
		t.Fatal("startup timeout")
	}
	request := string(domain.NewID())
	args := []string{"integration", "create", "--request-id", request, "--input", "-"}
	raw := `{"name":"CLI GitHub","provider":"github.com","token_kind":"fine-grained","resource_owner":"fixture-owner"}`
	code, value := cliRun(t, root, args, raw)
	if code != 0 {
		t.Fatalf("create: %+v", value)
	}
	id := value["result"].(map[string]any)["profile"].(map[string]any)["id"].(string)
	code, value = cliRun(t, root, args, raw)
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("create replay", value)
	}
	for _, args := range [][]string{{"integration", "list"}, {"integration", "snapshot"}, {"integration", "get", "--id", id}} {
		if code, value := cliRun(t, root, args, ""); code != 0 {
			t.Fatal("metadata read", value)
		}
	}
	for _, ending := range []string{"", "\n", "\r\n"} {
		token, err := readPAT(strings.NewReader(strings.Repeat("t", 512) + ending))
		if err != nil || len(token) != 512 {
			t.Fatal("bounded PAT", err)
		}
		clear(token)
	}
	for _, raw := range []string{"", "\n", " token", "token ", "token\n\n", "token\x00", strings.Repeat("t", 513)} {
		if _, err := readPAT(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	for _, args := range [][]string{{"integration", "replace-token", "--id", id, "--revision", "1"}, {"integration", "replace-token", "--id", id, "--revision", "1", "--pat", "never-a-flag"}, {"integration", "validate", "--id", id, "--revision", "0"}} {
		if code, value := cliRun(t, root, args, ""); code == 0 {
			t.Fatal("invalid command", value)
		}
	}
	// Metadata-only creation does not write a PAT; deletion needs no user secret.
	code, value = cliRun(t, root, []string{"integration", "delete", "--id", id, "--revision", "1"}, "")
	if code != 0 || value["result"].(map[string]any)["deleted"] != true {
		t.Fatal("delete", value)
	}
}
