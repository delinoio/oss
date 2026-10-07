// SPDX-License-Identifier: Apache-2.0
package cli

import (
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

func TestCLIAgentWorkerCurrentSourcesAtomicityAndReceipts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
	run := func(args []string, value any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(value)
		code, output := cliRun(t, root, args, string(raw))
		if code != 0 {
			t.Fatalf("%v: %d %+v", args, code, output)
		}
		return output["result"].(map[string]any)
	}
	accountIDs := []domain.ID{}
	for _, name := range []string{"First", "Second"} {
		provider := run([]string{"provider", "create"}, domain.Provider{Name: name, Endpoint: "https://" + name + ".example.test/v1", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth, Enabled: new(true)})["resource"].(map[string]any)
		account := run([]string{"account", "create"}, domain.Account{Alias: name, Type: domain.APIAccount, ProviderID: domain.ID(provider["id"].(string)), Enabled: true, Health: domain.AccountDisconnected})["resource"].(map[string]any)
		accountIDs = append(accountIDs, domain.ID(account["id"].(string)))
	}
	agent := domain.Agent{Name: "Atomic route Worker", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Accounts: []domain.WeightedAccount{{ID: accountIDs[0], Weight: 1}}}, {Accounts: []domain.WeightedAccount{{ID: accountIDs[1], Weight: 2}}}}, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite}}
	selectionFile := filepath.Join(t.TempDir(), "models.json")
	write := func(selections []workerModelSelection) {
		t.Helper()
		raw, _ := json.Marshal(selections)
		if err := os.WriteFile(selectionFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	failure := func(args []string, body domain.Agent, expected string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, value := cliRun(t, root, args, string(raw))
		if code == 0 || value["error"].(map[string]any)["code"] != expected {
			t.Fatalf("%v: %d %+v", args, code, value)
		}
	}
	native := []workerModelSelection{{NativeID: "first-native"}, {NativeID: "second-native"}}
	write([]workerModelSelection{native[0], {ModelID: domain.NewID(), ModelRevision: 1}})
	createArgs := []string{"agent", "create", "--route-models-file", selectionFile, "--request-id", string(domain.NewID())}
	failure(createArgs, agent, "invalid_argument")
	// A failure after the first source model resolves must roll back that model.
	failing := agent
	failing.Routes = append([]domain.AgentSourceRoute(nil), agent.Routes...)
	failing.Routes[1].ModelID = domain.NewID()
	write([]workerModelSelection{native[0], {ModelID: failing.Routes[1].ModelID, ModelRevision: 1}})
	failure(createArgs, failing, "not_found")
	if models := run([]string{"model", "list"}, nil)["resources"].([]any); len(models) != 0 {
		t.Fatal("partial model survived failed Worker save")
	}
	write(native)
	saved := run(createArgs, agent)["resource"].(map[string]any)
	replay := run(createArgs, agent)
	if replay["replayed"] != true || replay["resource"].(map[string]any)["id"] != saved["id"] {
		t.Fatal("original Worker receipt changed")
	}
	raw, _ := json.Marshal(saved["data"])
	var configured domain.Agent
	if err := json.Unmarshal(raw, &configured); err != nil {
		t.Fatal(err)
	}
	canonical := []workerModelSelection{{ModelID: configured.Routes[0].ModelID, ModelRevision: 1}, {ModelID: configured.Routes[1].ModelID, ModelRevision: 1}}
	editArgs := []string{"agent", "edit", "--id", saved["id"].(string), "--revision", "1", "--route-models-file", selectionFile}
	stale := append([]workerModelSelection(nil), canonical...)
	stale[1].ModelRevision = 9
	write(stale)
	failure(editArgs, configured, "conflict")
	write(canonical)
	updated := run(editArgs, configured)["resource"].(map[string]any)
	if updated["revision"] != float64(2) {
		t.Fatal("current canonical route edit failed")
	}
	// The live account source must match each selected canonical model.
	mixed := configured
	mixed.Routes = append([]domain.AgentSourceRoute(nil), configured.Routes...)
	mixed.Routes[1].Accounts = agent.Routes[0].Accounts
	editArgs[5] = "2"
	failure(editArgs, mixed, "invalid_argument")
	empty := configured
	empty.Routes = append([]domain.AgentSourceRoute(nil), configured.Routes...)
	empty.Routes[0].Accounts = nil
	failure(editArgs, empty, "missing_input")
	write([]workerModelSelection{{ModelID: configured.Routes[0].ModelID}, {ModelID: configured.Routes[1].ModelID, ModelRevision: 1}})
	failure(editArgs, configured, "missing_input")
}
