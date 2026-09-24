package cli

import (
	"bytes"
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

func TestCLIPairWorkerAndInspectRealRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
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
	repo := filepath.Join(t.TempDir(), "checkout")
	if err := exec.Command("git", "init", "--quiet", repo).Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repo, "remote", "add", "origin", "https://example.invalid/repository.git").Run(); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	request := string(domain.NewID())
	create := []string{"device", "create-pairing", "--type", "worker", "--name", "fixture", "--request-id", request}
	code, result := cliRun(t, root, create, "")
	if code != 0 {
		t.Fatalf("grant: %d %v", code, result)
	}
	path := result["result"].(map[string]any)["code_file"].(string)
	grant, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, retry := cliRun(t, root, create, "")
	if code != 0 || retry["result"].(map[string]any)["replayed"] != true {
		t.Fatalf("grant retry: %d %v", code, retry)
	}
	workerRoot := filepath.Join(t.TempDir(), "worker")
	pair := []string{"worker", "pair", "--worker-dir", workerRoot, "--code-stdin"}
	code, result = cliRun(t, root, pair, string(grant))
	if code != 0 {
		t.Fatalf("pair: %d %v", code, result)
	}
	machine := result["result"].(map[string]any)["machine_id"].(string)
	code, result = cliRun(t, root, pair, string(grant))
	if code != 0 || result["result"].(map[string]any)["machine_id"] != machine {
		t.Fatalf("pair retry changed machine: %d %v", code, result)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerReady := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Ready: func(domain.ID) { close(workerReady) }})
	}()
	defer func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Worker did not stop")
		}
	}()
	select {
	case <-workerReady:
	case err := <-workerDone:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("worker timeout")
	}
	code, result = cliRun(t, root, []string{"repository", "inspect", "--machine-id", machine, "--path", sub, "--preferred-remote", "origin", "--wait"}, "")
	if code != 0 {
		t.Fatalf("inspect: %d %v", code, result)
	}
	job := result["result"].(map[string]any)["job"].(map[string]any)
	data := job["data"].(map[string]any)
	if data["state"] != "succeeded" {
		t.Fatalf("job failed: %v", data)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if data["output"].(map[string]any)["root"] != canonical {
		t.Fatalf("wrong canonical root: %v", data)
	}

	repository, err := json.Marshal(domain.Repository{Name: "fixture", PreferredRemote: "origin", Checkouts: []domain.Checkout{{MachineID: domain.ID(machine), Path: sub}}})
	if err != nil {
		t.Fatal(err)
	}
	// Omit automatic fetch to exercise the server default, not a Go bool zero.
	var config map[string]any
	json.Unmarshal(repository, &config)
	delete(config, "auto_fetch")
	repository, _ = json.Marshal(config)
	saveID := string(domain.NewID())
	save := []string{"repository", "create", "--wait", "--request-id", saveID}
	code, result = cliRun(t, root, save, string(repository))
	if code != 0 {
		t.Fatalf("save: %d %v", code, result)
	}
	saved := result["result"].(map[string]any)["resource"].(map[string]any)
	savedID := saved["id"].(string)
	if saved["data"].(map[string]any)["auto_fetch"] != true {
		t.Fatal("automatic fetch default was disabled")
	}
	checkout := saved["data"].(map[string]any)["checkouts"].([]any)[0].(map[string]any)
	if checkout["path"] != canonical {
		t.Fatal("saved noncanonical checkout")
	}
	code, result = cliRun(t, root, save, string(repository))
	if code != 0 || result["result"].(map[string]any)["resource"].(map[string]any)["id"] != savedID {
		t.Fatalf("save retry duplicated: %d %v", code, result)
	}
	if err := exec.Command("git", "-C", repo, "remote", "remove", "origin").Run(); err != nil {
		t.Fatal(err)
	}
	code, result = cliRun(t, root, []string{"repository", "edit", "--id", savedID, "--revision", "1", "--wait"}, string(repository))
	if code != 2 || result["error"].(map[string]any)["code"] != "invalid_argument" {
		t.Fatalf("missing remote saved: %d %v", code, result)
	}
	if result["result"].(map[string]any)["job"] == nil {
		t.Fatal("failure lost accepted operation identity")
	}
	code, result = cliRun(t, root, []string{"repository", "get", "--id", savedID}, "")
	if code != 0 || result["result"].(map[string]any)["revision"] != float64(1) {
		t.Fatalf("failed validation changed configuration: %d %v", code, result)
	}
	code, result = cliRun(t, workerRoot, []string{"project", "list"}, "")
	if code != 3 {
		t.Fatalf("worker used as client: %d %v", code, result)
	}
	code, result = cliRun(t, root, []string{"device", "create-pairing", "--type", "client", "--name", "remote client"}, "")
	if code != 0 {
		t.Fatal(result)
	}
	grant, err = os.ReadFile(result["result"].(map[string]any)["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := filepath.Join(t.TempDir(), "client")
	code, result = cliRun(t, root, []string{"device", "pair", "--device-dir", clientRoot, "--code-stdin"}, string(grant))
	if code != 0 {
		t.Fatal(result)
	}
	code, result = cliRun(t, clientRoot, []string{"machine", "list"}, "")
	if code != 0 {
		t.Fatal(result)
	}
	credential, err := worker.LoadCredential(workerRoot)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(result)
	var grantDoc worker.PairingCode
	json.Unmarshal(grant, &grantDoc)
	for _, secret := range []string{credential.Token, grantDoc.Code} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatal("secret output")
		}
	}
}
