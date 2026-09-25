package cli

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

// A private protocol fixture supplies connected Worker metadata, not a running
// installed harness. The real server/Connect/CLI own every schedule transition.
func scheduleCLIFixture(t *testing.T) (string, string, domain.ScheduleDefinition) {
	t.Helper()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "server")
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	machine, device, model, provider, agent, repository, project := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	instance := domain.NewID()
	token, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.cli-schedule", nil, func(tx *store.Tx) (any, error) {
		for _, item := range []struct {
			kind  domain.Kind
			id    domain.ID
			value any
		}{
			{domain.ProviderKind, provider, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth}},
			{domain.ModelKind, model, domain.Model{Name: "Fixture", NativeID: "fixture", ProviderID: provider, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}},
			{domain.AgentKind, agent, domain.Agent{Name: "Fixture", ModelID: model, Harness: domain.Codex, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}},
			{domain.MachineKind, machine, domain.Machine{Name: "Fixture", OS: "linux", Architecture: "arm64"}},
			{domain.DeviceKind, device, domain.Device{Name: "Fixture", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}},
			{domain.RepositoryKind, repository, domain.Repository{Name: "Fixture", Checkouts: []domain.Checkout{{MachineID: machine, Path: checkout}}, Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}},
			{domain.ProjectKind, project, domain.Project{Name: "Fixture", Repositories: []domain.ID{repository}, PrimaryRepository: repository}},
		} {
			if _, err := tx.Put(item.kind, item.id, 0, "", "", item.value); err != nil {
				return nil, err
			}
		}
		digest := sha256.Sum256([]byte(token))
		if err := tx.PutCredential(device, digest[:]); err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(machine, instance, time.Now().UTC())
	})
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	running, cancel := context.WithCancel(ctx)
	ready := make(chan server.Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(running, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(e server.Endpoint) { ready <- e })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	var endpoint server.Endpoint
	select {
	case endpoint = <-ready:
	case err := <-done:
		done <- err
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("schedule fixture server timeout")
	}
	// A persisted pre-start beat is no longer current availability. Exercise
	// the authenticated attach boundary after this server process is ready.
	workers := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, endpoint.URL)
	attach := connect.NewRequest(&pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(machine), InstanceId: string(instance), Version: rpc.Version})
	attach.Header().Set("Authorization", "Bearer "+token)
	if _, err := workers.AttachWorker(ctx, attach); err != nil {
		t.Fatal(err)
	}
	workerRoot := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(workerRoot); err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: endpoint.URL, ServerID: endpoint.ServerID, DeviceID: device, MachineID: machine, PairingID: domain.NewID(), Token: token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	return root, workerRoot, domain.ScheduleDefinition{Name: "CLI schedule", Enabled: true, Prompt: "retained CLI schedule input", ProjectID: project, AgentID: agent, MachineID: machine, Workspace: domain.Worktree, Mode: domain.ExecuteMode, Cron: "0 0 1 1 *", Timezone: "Asia/Seoul", Overlap: domain.ScheduleWaitOverlap}
}

func TestCLIScheduleLifecycleFIFOAfterDeletionAndLocalOrigin(t *testing.T) {
	root, workerRoot, definition := scheduleCLIFixture(t)
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
		code, value := cliRun(t, root, args, raw)
		if code != 0 {
			t.Fatalf("%v: %d %v", args, code, value)
		}
		return value["result"].(map[string]any)
	}
	schedule := func(result map[string]any) map[string]any { return result["schedule"].(map[string]any) }
	revision := func(r map[string]any) string { return strconv.FormatUint(uint64(r["revision"].(float64)), 10) }
	createArgs := []string{"schedule", "create", "--request-id", string(domain.NewID())}
	created := schedule(run(createArgs, definition))
	id := created["id"].(string)
	if replay := run(createArgs, definition); replay["replayed"] != true || schedule(replay)["id"] != id {
		t.Fatal("CLI create retry duplicated schedule")
	}
	next := run([]string{"schedule", "next-run", "--id", id}, nil)
	if next["enabled"] != true || next["next_run_at"] == nil || next["timezone"] != "Asia/Seoul" {
		t.Fatal("CLI omitted next-run state")
	}
	listed := run([]string{"schedule", "list", "--project-id", string(definition.ProjectID), "--enabled", "true", "--limit", "1"}, nil)
	if len(listed["schedules"].([]any)) != 1 {
		t.Fatal("CLI schedule filter missing")
	}
	paused := schedule(run([]string{"schedule", "pause", "--id", id, "--revision", revision(created)}, nil))
	next = run([]string{"schedule", "next-run", "--id", id}, nil)
	if next["enabled"] != false || next["next_run_at"] != nil {
		t.Fatal("paused schedule advertised future run")
	}
	firstArgs := []string{"schedule", "run-now", "--id", id, "--revision", revision(paused), "--request-id", string(domain.NewID())}
	first := run(firstArgs, nil)
	firstOccurrence := first["occurrence"].(map[string]any)
	firstSession := first["session"].(map[string]any)
	if firstOccurrence["data"].(map[string]any)["trigger"] != "manual" || firstSession["data"].(map[string]any)["source"] != "SCHEDULED" {
		t.Fatal("CLI Run now missing manual scheduled provenance")
	}
	if replay := run(firstArgs, nil); replay["replayed"] != true || replay["occurrence"].(map[string]any)["id"] != firstOccurrence["id"] {
		t.Fatal("CLI Run now retry duplicated work")
	}
	current := schedule(run([]string{"schedule", "inspect", "--id", id}, nil))
	second := run([]string{"schedule", "run-now", "--id", id, "--revision", revision(current)}, nil)
	secondOccurrence := second["occurrence"].(map[string]any)
	if second["session"] != nil || secondOccurrence["data"].(map[string]any)["state"] != "waiting" {
		t.Fatal("CLI Wait did not preserve pending occurrence")
	}
	current = schedule(run([]string{"schedule", "get", "--id", id}, nil))
	definition.Prompt = "future edited input"
	edited := schedule(run([]string{"schedule", "edit", "--id", id, "--revision", revision(current)}, definition))
	if edited["data"].(map[string]any)["configuration_revision"] != float64(3) {
		t.Fatal("CLI edit changed wrong configuration revision")
	}
	resumed := schedule(run([]string{"schedule", "resume", "--id", id, "--revision", revision(edited)}, nil))
	deleted := run([]string{"schedule", "delete", "--id", id, "--revision", revision(resumed)}, nil)
	if deleted["deleted"] != true {
		t.Fatal("CLI deletion missing")
	}
	history := run([]string{"schedule", "history", "--id", id}, nil)["occurrences"].([]any)
	if len(history) != 2 {
		t.Fatal("CLI history lost after schedule deletion")
	}
	// Public Stop confirms pre-dispatch cleanup; the joined server loop releases
	// the accepted FIFO successor even though its schedule no longer exists.
	run([]string{"session", "stop", "--id", firstSession["id"].(string), "--revision", revision(firstSession)}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := run([]string{"schedule", "occurrence", "--id", id, "--occurrence-id", secondOccurrence["id"].(string)}, nil)["occurrence"].(map[string]any)["data"].(map[string]any)
		if got["state"] == "active" {
			if got["selection"].(map[string]any)["prompt"] != "retained CLI schedule input" {
				t.Fatal("Wait followed edited prompt")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CLI accepted Wait did not survive source deletion")
		}
		time.Sleep(20 * time.Millisecond)
	}
	local := definition
	local.Workspace = domain.Local
	raw, _ := json.Marshal(local)
	if code, value := cliRun(t, root, []string{"schedule", "create"}, string(raw)); code == 0 || value["error"] == nil {
		t.Fatal("Local schedule omitted originating Worker authority")
	}
	localCreated := schedule(run([]string{"schedule", "create", "--local-worker-dir", workerRoot}, local))
	localOrigin := localCreated["data"].(map[string]any)["local_origin"].(map[string]any)
	if localOrigin["machine_id"] != string(local.MachineID) {
		t.Fatal("CLI Local origin changed machine")
	}
	local.Prompt = "Local edited from a product client"
	localEdited := schedule(run([]string{"schedule", "edit", "--id", localCreated["id"].(string), "--revision", revision(localCreated)}, local))
	if localEdited["data"].(map[string]any)["local_origin"].(map[string]any)["device_id"] != localOrigin["device_id"] {
		t.Fatal("ordinary Local edit relocated origin")
	}
	for _, args := range [][]string{{"schedule", "pause", "--id", localCreated["id"].(string)}, {"schedule", "edit", "--id", localCreated["id"].(string)}, {"schedule", "list", "--limit", "0"}, {"schedule", "list", "--enabled", "maybe"}, {"schedule", "history", "--id", id, "--limit", "201"}, {"schedule", "occurrence", "--id", id}, {"schedule", "resume", "--id", id, "--revision", "1"}} {
		code, value := cliRun(t, root, args, "")
		if code == 0 || value["error"] == nil {
			t.Fatalf("invalid schedule action accepted: %v", args)
		}
	}
}

func TestCLIScheduleCommandsRequireExplicitRunningServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, action := range []string{"list", "create", "history", "run-now"} {
		code, value := cliRun(t, root, []string{"schedule", action}, "")
		if code != 4 || value["error"].(map[string]any)["code"] != string(domain.ServerUnavailable) {
			t.Fatalf("schedule %s started a server or hid missing endpoint", action)
		}
	}
	if !strings.Contains(help, "schedule pause|resume|delete|run-now") {
		t.Fatal("schedule help missing")
	}
}
