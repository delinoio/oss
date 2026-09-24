package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLISessionAcceptanceQueueAndArchive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
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
		t.Fatal("server startup timeout")
	}
	run := func(args []string, input any) map[string]any {
		t.Helper()
		raw := ""
		if input != nil {
			b, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(b)
		}
		code, result := cliRun(t, root, args, raw)
		if code != 0 {
			t.Fatalf("%v: %d %v", args, code, result)
		}
		return result["result"].(map[string]any)
	}
	grant := run([]string{"device", "create-pairing", "--type", "worker", "--name", "session fixture"}, nil)
	codeDocument, err := os.ReadFile(grant["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	code, paired := cliRun(t, root, []string{"worker", "pair", "--worker-dir", filepath.Join(t.TempDir(), "worker"), "--code-stdin"}, string(codeDocument))
	if code != 0 {
		t.Fatal(paired)
	}
	machine := paired["result"].(map[string]any)["machine_id"].(string)
	p := run([]string{"provider", "create"}, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})["resource"].(map[string]any)
	m := run([]string{"model", "create"}, domain.Model{Name: "Fixture model", NativeID: "fixture", ProviderID: domain.ID(p["id"].(string)), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})["resource"].(map[string]any)
	a := run([]string{"agent", "create"}, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: domain.ID(m["id"].(string)), Options: domain.AgentOptions{Permission: domain.PermissionDefault}})["resource"].(map[string]any)
	request := string(domain.NewID())
	args := []string{"session", "create", "--request-id", request}
	input := domain.CreateSession{Name: "CLI session", AgentID: domain.ID(a["id"].(string)), MachineID: domain.ID(machine), Workspace: domain.GeneralChat, Prompt: "CLI private fixture"}
	created := run(args, input)
	session := created["session"].(map[string]any)
	id := session["id"].(string)
	if body := session["data"].(map[string]any); body["source"] != "EXTERNAL_CLI" || body["dispatch"] != "blocked" || body["outcome"] != "not-started" {
		t.Fatal("CLI changed acceptance semantics")
	}
	if replay := run(args, input); replay["replayed"] != true || replay["session"].(map[string]any)["id"] != id {
		t.Fatal("CLI acceptance duplicated")
	}
	enqueued := run([]string{"session", "enqueue", "--id", id}, domain.SessionInput{Prompt: "plan later", Mode: domain.PlanMode})
	item := enqueued["input"].(map[string]any)
	itemID := item["id"].(string)
	run([]string{"queue", "edit", "--session-id", id, "--id", itemID, "--revision", "1"}, map[string]string{"prompt": "changed plan"})
	listed := run([]string{"queue", "list", "--session-id", id}, nil)["inputs"].([]any)
	if len(listed) != 2 || listed[1].(map[string]any)["data"].(map[string]any)["mode"] != "plan" {
		t.Fatal("CLI edit changed queued mode")
	}
	removed := run([]string{"queue", "remove", "--session-id", id, "--id", itemID, "--revision", "2"}, nil)
	session = removed["session"].(map[string]any)
	revision := strconv.FormatUint(uint64(session["revision"].(float64)), 10)
	archived := run([]string{"session", "archive", "--id", id, "--revision", revision}, nil)["session"].(map[string]any)
	if len(run([]string{"session", "list"}, nil)["sessions"].([]any)) != 0 {
		t.Fatal("CLI ordinary list includes archive")
	}
	revision = strconv.FormatUint(uint64(archived["revision"].(float64)), 10)
	restored := run([]string{"session", "restore", "--id", id, "--revision", revision}, nil)["session"].(map[string]any)
	if restored["data"].(map[string]any)["dispatch"] != "paused" {
		t.Fatal("CLI restore dispatched queue")
	}
	revision = strconv.FormatUint(uint64(restored["revision"].(float64)), 10)
	code, failed := cliRun(t, root, []string{"session", "resume", "--id", id, "--revision", revision}, "")
	if code == 0 || failed["error"].(map[string]any)["code"] != "unsupported" {
		t.Fatal("CLI claimed unsupported native execution")
	}
}
