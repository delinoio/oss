package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLIConfigurationTransferThroughRealWorker(t *testing.T) {
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
		t.Fatal("server timeout")
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
			t.Fatalf("command %v: %v", args, result)
		}
		return result["result"].(map[string]any)
	}
	grant := run([]string{"device", "create-pairing", "--type", "worker", "--name", "import fixture"}, nil)
	codeDocument, err := os.ReadFile(grant["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	workerRoot := filepath.Join(t.TempDir(), "worker")
	code, paired := cliRun(t, root, []string{"worker", "pair", "--worker-dir", workerRoot, "--code-stdin"}, string(codeDocument))
	if code != 0 {
		t.Fatal(paired)
	}
	machine := domain.ID(paired["result"].(map[string]any)["machine_id"].(string))
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	workerReady := make(chan struct{})
	go func() {
		workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Ready: func(domain.ID) { close(workerReady) }})
	}()
	defer func() {
		stopWorker()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-workerReady:
	case <-time.After(10 * time.Second):
		t.Fatal("Worker timeout")
	}
	checkout := t.TempDir()
	checkout, err = filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-b", "main", checkout).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	sourceMachine, repoID, templateID := domain.NewID(), domain.NewID(), domain.NewID()
	repo, _ := json.Marshal(domain.Repository{Name: "Imported repository", Checkouts: []domain.Checkout{{MachineID: sourceMachine, Path: "/old/computer/path"}}, AutoFetch: true})
	template, _ := json.Marshal(domain.Template{Name: "Imported instructions", Contents: "Keep this original content.\n"})
	selection := domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: 1, Entries: []domain.ConfigurationEntry{{ID: repoID, Kind: domain.RepositoryKind, Document: repo}, {ID: templateID, Kind: domain.TemplateKind, Document: template}}, Machines: []domain.ConfigurationMachine{{ID: sourceMachine, Name: "Original computer", OS: "linux", Architecture: "amd64"}}}, Machines: []domain.ConfigurationMachineBinding{{SourceID: sourceMachine, TargetID: machine}}, Checkouts: []domain.ConfigurationCheckoutBinding{{RepositoryID: repoID, MachineID: sourceMachine, Path: checkout}}}
	previewPath := filepath.Join(t.TempDir(), "reviewed.json")
	run([]string{"configuration", "preview", "--output", previewPath}, selection)
	preview, err := os.ReadFile(previewPath)
	if err != nil {
		t.Fatal(err)
	}
	request := string(domain.NewID())
	apply := []string{"configuration", "apply", "--request-id", request, "--input", previewPath}
	accepted := run(apply, nil)["import"].(map[string]any)
	jobID := accepted["job_id"].(string)
	deadline := time.Now().Add(15 * time.Second)
	for {
		result := run([]string{"job", "get", "--id", jobID}, nil)
		data := result["data"].(map[string]any)
		if data["state"] == "succeeded" {
			break
		}
		if data["state"] != "queued" || time.Now().After(deadline) {
			t.Fatalf("import did not complete: %v", data)
		}
		time.Sleep(25 * time.Millisecond)
	}
	replay := run(apply, nil)
	if replay["replayed"] != true || replay["import"].(map[string]any)["state"] != "succeeded" {
		t.Fatal("did not observe original completed import")
	}
	exportedPath := filepath.Join(t.TempDir(), "export.json")
	run([]string{"configuration", "export", "--output", exportedPath}, nil)
	exported, err := os.ReadFile(exportedPath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ConfigurationBundle
	if err = domain.Decode(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Entries) != 8 || len(bundle.Machines) != 1 || bundle.Machines[0].ID != machine {
		t.Fatal("missing configuration/machine mapping")
	}
	for _, entry := range bundle.Entries {
		if entry.Kind == domain.RepositoryKind {
			var value domain.Repository
			if err = domain.Decode(entry.Document, &value); err != nil {
				t.Fatal(err)
			}
			if value.Checkouts[0].Path != checkout || value.Checkouts[0].MachineID != machine {
				t.Fatal("wrong checkout")
			}
		}
		if entry.Kind == domain.TemplateKind && string(entry.Document) != string(template) {
			t.Fatal("instructions changed")
		}
	}
	if code, _ := cliRun(t, root, []string{"configuration", "export", "--output", exportedPath}, ""); code == 0 {
		t.Fatal("overwrote export")
	}
	retained, err := os.ReadFile(previewPath)
	if err != nil || string(retained) != string(preview) {
		t.Fatal("changed reviewed preview")
	}
}
