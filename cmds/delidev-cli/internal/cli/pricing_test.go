package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIPricingRevisionMissingRatesAndHistoricalRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
		t.Fatal("readiness timeout")
	}
	run := func(args []string, input string) map[string]any {
		t.Helper()
		code, result := cliRun(t, root, args, input)
		if code != 0 {
			t.Fatal(result)
		}
		return result["result"].(map[string]any)
	}
	provider := run([]string{"provider", "create"}, `{"name":"Fixture","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":false,"enabled":true}`)["resource"].(map[string]any)["id"].(string)
	raw, _ := json.Marshal(domain.Model{Name: "Fixture", ProviderID: domain.ID(provider), NativeID: "fixture", Manual: true, MetadataSource: domain.UserDeclared, Harnesses: []domain.Harness{domain.Codex}})
	model := run([]string{"model", "create"}, string(raw))["resource"].(map[string]any)["id"].(string)
	if run([]string{"usage", "pricing", "get", "--model-id", model}, "")["pricing"] != nil {
		t.Fatal("invented price")
	}
	basis := `{"currency":"USD","source":"Fixture rates","as_of":"2026-09-25","input_mode":"uniform-input","input_per_million":"0.000000001","output_per_million":null,"exclusions":["Token basis only"]}`
	args := []string{"usage", "pricing", "set", "--model-revision", "1", "--model-id", model, "--request-id", string(domain.NewID())}
	first := run(args, basis)["pricing"].(map[string]any)
	if first["revision"] != "1" || first["basis"].(map[string]any)["input_per_million"] != "0.000000001" {
		t.Fatal("decimal precision lost", first)
	}
	next := []string{"usage", "pricing", "set", "--model-revision", "1", "--model-id", model, "--revision", "1"}
	second := run(next, basis)["pricing"].(map[string]any)
	replay := run(args, basis)
	if replay["replayed"] != true || replay["pricing"].(map[string]any)["id"] != first["id"] {
		t.Fatal("retry changed basis")
	}
	if run([]string{"usage", "pricing", "get", "--model-id", model}, "")["pricing"].(map[string]any)["id"] != second["id"] {
		t.Fatal("retry replaced active basis")
	}
	if run([]string{"usage", "pricing", "version", "--id", first["id"].(string)}, "")["pricing"].(map[string]any)["id"] != first["id"] {
		t.Fatal("lost history")
	}
	if code, _ := cliRun(t, root, next, basis); code != 5 {
		t.Fatal("stale price accepted", code)
	}
	if code, _ := cliRun(t, root, []string{"usage", "pricing", "set", "--model-revision", "1", "--model-id", model, "--revision", "2"}, `{"currency":"USD","source":"Fixture rates","as_of":"2026-09-25","input_mode":"uniform-input","output_per_million":"1e-9"}`); code == 0 {
		t.Fatal("exponential price accepted")
	}
}
