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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestCLIBudgetExplicitChangeAndCurrentReceiptReplay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx := context.Background()
	s, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.budget-session", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.SessionKind, id, 0, id, "", domain.Session{Name: "Retained session", Workspace: domain.GeneralChat, Archive: domain.Archived, Dispatch: domain.DispatchPaused, Outcome: domain.ExecutionSucceeded, Recovery: domain.NoRecovery})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	running, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(running, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
	run := func(args ...string) map[string]any {
		t.Helper()
		code, value := cliRun(t, root, args, "")
		if code != 0 {
			t.Fatal(value)
		}
		return value["result"].(map[string]any)
	}
	if run("session", "budget", "get", "--id", string(id))["view"].(map[string]any)["state"] != "BUDGET_STATE_DISABLED" {
		t.Fatal("implicit budget")
	}
	args := []string{"session", "budget", "set", "--id", string(id), "--revision", "1", "--currency", "USD", "--threshold", "0.000000000000001", "--request-id", string(domain.NewID())}
	set := run(args...)["view"].(map[string]any)
	if set["state"] != "BUDGET_STATE_ALLOW_INCOMPLETE" || set["budget"].(map[string]any)["threshold"] != "0.000000000000001" || set["selected_currency"].(map[string]any)["known_amount"] != "" {
		t.Fatal("CLI changed unknown or exact threshold", set)
	}
	removed := run("session", "budget", "remove", "--id", string(id), "--revision", set["session"].(map[string]any)["revision"].(string))["view"].(map[string]any)
	if removed["state"] != "BUDGET_STATE_DISABLED" {
		t.Fatal("explicit removal failed")
	}
	replay := run(args...)
	if replay["replayed"] != true || replay["view"].(map[string]any)["state"] != "BUDGET_STATE_DISABLED" {
		t.Fatal("old CLI receipt restored budget")
	}
	for _, bad := range [][]string{{"session", "budget", "set", "--id", string(id), "--revision", "3", "--currency", "USD", "--threshold", "NaN"}, {"session", "budget", "remove", "--id", string(id)}, {"session", "budget", "get", "--id", "bad"}} {
		if code, _ := cliRun(t, root, bad, ""); code == 0 {
			t.Fatal("invalid budget command", bad)
		}
	}
}
