package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// This test uses only the public CLI configuration and lifecycle path. Native
// execution is opt-in and uses a fresh Worker environment plus a loopback
// scripted keyless provider, never an existing login or external account.
func TestManualNativeCLIFirstDispatch(t *testing.T) {
	testManualNativeCLI(t, false, nativeDefaultWorkspaces)
}

func TestManualNativeCLISteer(t *testing.T) {
	testManualNativeCLI(t, true, nativeDefaultWorkspaces)
}

func TestManualNativeCLIMultipleRepositories(t *testing.T) {
	testManualNativeCLI(t, false, nativeMultipleWorkspaces)
}

type nativeCLIWorkspaceProfile uint8

const (
	nativeDefaultWorkspaces nativeCLIWorkspaceProfile = iota
	nativeMultipleWorkspaces
	nativeLocalWorkspaces
)

func TestManualNativeCLILocalRepositories(t *testing.T) {
	testManualNativeCLI(t, false, nativeLocalWorkspaces)
}

func testManualNativeCLI(t *testing.T, steerScenario bool, profile nativeCLIWorkspaceProfile) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex and private loopback fixture only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("select an absolute native executable")
	}
	type nativeScenario struct {
		mode         domain.SessionMode
		workspace    domain.WorkspaceType
		repositories int
	}
	scenarios := []nativeScenario{{domain.ExecuteMode, domain.GeneralChat, 0}, {domain.PlanMode, domain.GeneralChat, 0}, {domain.ExecuteMode, domain.Worktree, 1}}
	if profile == nativeMultipleWorkspaces {
		scenarios = []nativeScenario{{domain.ExecuteMode, domain.Worktree, 2}, {domain.PlanMode, domain.Worktree, 2}}
	}
	if profile == nativeLocalWorkspaces {
		scenarios = []nativeScenario{{domain.ExecuteMode, domain.Local, 2}, {domain.PlanMode, domain.Local, 1}}
	}
	for _, scenario := range scenarios {
		mode := scenario.mode
		multipleRepositories := scenario.repositories > 1
		t.Run(string(scenario.workspace)+"/"+string(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			root := filepath.Join(t.TempDir(), "server")
			ready := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil))}, func(server.Endpoint) { close(ready) })
			}()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("server did not start")
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
					t.Fatalf("public CLI %v failed: %d %v", args, code, result)
				}
				return result["result"].(map[string]any)
			}
			revision := func(r map[string]any) string { return strconv.FormatUint(uint64(r["revision"].(float64)), 10) }
			grant := run([]string{"device", "create-pairing", "--type", "worker", "--name", "private native CLI fixture"}, nil)
			codeBytes, err := os.ReadFile(grant["code_file"].(string))
			if err != nil {
				t.Fatal(err)
			}
			workerRoot := filepath.Join(t.TempDir(), "worker")
			code, paired := cliRun(t, root, []string{"worker", "pair", "--worker-dir", workerRoot, "--code-stdin"}, string(codeBytes))
			if code != 0 {
				t.Fatal(paired)
			}
			machine := paired["result"].(map[string]any)["machine_id"].(string)
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerReady, workerDone := make(chan struct{}), make(chan error, 1)
			go func() {
				workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), Ready: func(domain.ID) { close(workerReady) }})
			}()
			defer func() {
				stopWorker()
				if err := <-workerDone; err != nil && domain.SafeError(err).Code != domain.Canceled {
					t.Error(err)
				}
			}()
			select {
			case <-workerReady:
			case <-ctx.Done():
				t.Fatal("Worker did not attach")
			}
			machineRecord := run([]string{"machine", "get", "--id", machine}, nil)
			selections := domain.ExecutableSelections{Executables: []domain.ExecutableSelection{{Harness: domain.Codex, Path: binary}}}
			for _, h := range []domain.Harness{domain.ClaudeCode, domain.OpenCode, domain.GrokBuild} {
				selections.Executables = append(selections.Executables, domain.ExecutableSelection{Harness: h, Path: filepath.Join(t.TempDir(), "intentionally-unavailable")})
			}
			discovery := run([]string{"machine", "discover", "--id", machine, "--revision", revision(machineRecord), "--input", "-", "--protocol", "--wait"}, selections)
			observed := discovery["machine"].(map[string]any)["data"].(map[string]any)["installations"].([]any)
			verified := false
			for _, entry := range observed {
				v := entry.(map[string]any)
				if v["harness"] == string(domain.Codex) {
					verified = v["protocol_verified"] == true
				}
			}
			if !verified {
				t.Fatal("actual native protocol was not verified")
			}
			var calls, validations atomic.Int64
			interruptedRequest := make(chan struct{}, 1)
			steerStarted, releaseSteer := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("keyless upstream received a credential")
					http.Error(w, "unexpected authority", 403)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					validations.Add(1)
					io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/responses" {
					t.Error("unexpected upstream operation")
					http.Error(w, "unsupported", 400)
					return
				}
				call := calls.Add(1)
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil || !strings.Contains(string(body), "fixture-model") {
					t.Error("native execution changed accepted input/model")
				}
				prompts := []string{"Public first prompt", "Public second prompt", "Public third prompt", "Public fourth prompt", "Public fifth prompt", "Public interrupted prompt", "Public resumed interrupted prompt"}
				if steerScenario {
					prompts = []string{"Public first prompt", "Public same-turn Steer 한글 🐦", "Public continuation after Steer"}
				}
				if call < 1 || call > int64(len(prompts)) {
					t.Error("unexpected provider inference count")
				} else {
					for _, prompt := range prompts[:call] {
						if strings.Count(string(body), prompt) != 1 {
							t.Error("native history omitted or duplicated an accepted user input")
						}
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if steerScenario && call == 1 {
					fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.created","response":{"id":"resp_public_fixture_1","status":"in_progress"}}`)
					w.(http.Flusher).Flush()
					close(steerStarted)
					select {
					case <-releaseSteer:
					case <-ctx.Done():
						return
					case <-r.Context().Done():
						return
					}
				}
				if call == 6 {
					fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.created","response":{"id":"resp_public_interrupted","status":"in_progress"}}`)
					w.(http.Flusher).Flush()
					interruptedRequest <- struct{}{}
					<-r.Context().Done()
					return
				}
				for index, event := range []any{
					map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_public_fixture_%d", call), "status": "in_progress"}},
					map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_public_fixture_%d", call), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Public execution complete."}}}},
					map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_public_fixture_%d", call), "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
				} {
					if steerScenario && call == 1 && index == 0 {
						continue
					}
					raw, _ := json.Marshal(event)
					fmt.Fprintf(w, "data: %s\n\n", raw)
				}
			}))
			defer upstream.Close()
			defer upstream.CloseClientConnections()
			provider := run([]string{"provider", "create"}, domain.Provider{Name: "Private fixture", Endpoint: upstream.URL, Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})["resource"].(map[string]any)
			account := run([]string{"account", "create"}, domain.Account{Alias: "Private fixture", ProviderID: domain.ID(provider["id"].(string)), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})["resource"].(map[string]any)
			account = run([]string{"account", "connect", "--id", account["id"].(string), "--revision", revision(account), "--keyless"}, nil)["account"].(map[string]any)
			account = run([]string{"account", "validate", "--id", account["id"].(string), "--revision", revision(account)}, nil)["account"].(map[string]any)
			if account["data"].(map[string]any)["health"] != string(domain.AccountReady) {
				t.Fatal("public account validation did not establish readiness")
			}
			model := run([]string{"model", "create"}, domain.Model{Name: "Fixture", NativeID: "fixture-model", ProviderID: domain.ID(provider["id"].(string)), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})["resource"].(map[string]any)
			options := domain.AgentOptions{Permission: domain.PermissionReadOnly}
			if scenario.workspace != domain.GeneralChat {
				options.Permission = domain.PermissionWorkspaceWrite
			}
			agent := run([]string{"agent", "create"}, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: domain.ID(model["id"].(string)), Accounts: []domain.WeightedAccount{{ID: domain.ID(account["id"].(string)), Weight: 1}}, Options: options})["resource"].(map[string]any)
			create := []string{"session", "create", "--request-id", string(domain.NewID()), "--wait"}
			type localCheckout struct{ root, head string }
			var localCheckouts []localCheckout
			if scenario.workspace == domain.Local {
				create = append(create, "--local-worker-dir", workerRoot)
			}
			input := domain.CreateSession{Name: "Public CLI first execution", AgentID: domain.ID(agent["id"].(string)), MachineID: domain.ID(machine), Workspace: scenario.workspace, Mode: mode, Prompt: "Public first prompt"}
			if scenario.workspace != domain.GeneralChat {
				repositories := make([]domain.ID, 0, scenario.repositories)
				for range scenario.repositories {
					checkout, hooks := t.TempDir(), t.TempDir()
					git := func(args ...string) string {
						t.Helper()
						argv := append([]string{"-C", checkout, "-c", "user.name=DeliDev Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=" + hooks}, args...)
						cmd := exec.CommandContext(ctx, "git", argv...)
						cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
						out, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("private fixture Git: %v %s", err, out)
						}
						return strings.TrimSpace(string(out))
					}
					git("init", "-b", "main")
					if err := os.WriteFile(filepath.Join(checkout, "tracked.txt"), []byte("retained fixture"), 0600); err != nil {
						t.Fatal(err)
					}
					git("add", "tracked.txt")
					git("commit", "-m", "fixture")
					commit := git("rev-parse", "HEAD")
					if scenario.workspace == domain.Local {
						git("switch", "-c", "existing-local-branch")
						if err := os.WriteFile(filepath.Join(checkout, "local.txt"), []byte("local commit"), 0600); err != nil {
							t.Fatal(err)
						}
						git("add", "local.txt")
						git("commit", "-m", "existing local change")
						if err := os.WriteFile(filepath.Join(checkout, "tracked.txt"), []byte("keep local dirty tree"), 0600); err != nil {
							t.Fatal(err)
						}
						canonical, err := filepath.EvalSymlinks(checkout)
						if err != nil {
							t.Fatal(err)
						}
						localCheckouts = append(localCheckouts, localCheckout{canonical, git("rev-parse", "HEAD")})
					}
					repo := run([]string{"repository", "create", "--wait"}, domain.Repository{Name: "Private native fixture", Checkouts: []domain.Checkout{{MachineID: domain.ID(machine), Path: checkout}}, Starting: domain.Reference{Type: domain.CommitReference, Name: commit}})["resource"].(map[string]any)
					id := domain.ID(repo["id"].(string))
					repositories = append(repositories, id)
				}
				project := run([]string{"project", "create"}, domain.Project{Name: "Private native fixture", Repositories: repositories, PrimaryRepository: repositories[len(repositories)-1]})["resource"].(map[string]any)
				input.ProjectID = domain.ID(project["id"].(string))
			}
			accepted := run(create, input)
			id := accepted["session"].(map[string]any)["id"].(string)
			var state domain.Session
			var steerArgs []string
			if steerScenario {
				select {
				case <-steerStarted:
				case <-ctx.Done():
					t.Fatal("native first request did not start")
				}
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					raw, _ := json.Marshal(current["data"])
					state = domain.Session{}
					if domain.Decode(raw, &state) != nil || state.Recovery != domain.NoRecovery {
						t.Fatal("native input acceptance failed")
					}
					if state.Execution != nil && state.Execution.NativeTurnID != "" {
						break
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("original input was not accepted")
					}
				}
				queued := run([]string{"session", "enqueue", "--id", id}, domain.SessionInput{Prompt: "Public same-turn Steer 한글 🐦", Mode: mode})["input"].(map[string]any)
				steerArgs = []string{"session", "steer", "--id", id, "--input-id", queued["id"].(string), "--revision", revision(queued), "--execution-id", string(state.ActiveExecutionID), "--turn-id", state.Execution.NativeTurnID, "--request-id", string(domain.NewID())}
				steered := run(steerArgs, nil)
				steerID := steered["steer"].(map[string]any)["id"].(string)
				for {
					current := run([]string{"steer", "get", "--id", steerID}, nil)
					raw, _ := json.Marshal(current["data"])
					var attempt domain.SteerAttempt
					if domain.Decode(raw, &attempt) != nil {
						t.Fatal("invalid Steer state")
					}
					if attempt.State == domain.SteerAccepted {
						break
					}
					if attempt.State != domain.SteerQueued && attempt.State != domain.SteerClaimed {
						t.Fatalf("native Steer failed: %+v", attempt)
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("native Steer was not accepted")
					}
				}
				close(releaseSteer)
			}
			for {
				current := run([]string{"session", "get", "--id", id}, nil)
				raw, _ := json.Marshal(current["data"])
				state = domain.Session{}
				if domain.Decode(raw, &state) != nil {
					t.Fatal("invalid retained session")
				}
				if state.Recovery != domain.NoRecovery || state.Outcome == domain.ExecutionFailed {
					t.Fatalf("public dispatch failed: %+v", state)
				}
				if state.Execution != nil && state.Execution.CleanupVerified {
					break
				}
				select {
				case <-time.After(30 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("public first execution did not complete: %+v", state)
				}
			}
			if state.Outcome != domain.ExecutionSucceeded || state.ActiveExecutionID != "" || state.PendingInputs != 0 || state.InitialExecution == nil || state.InitialExecution.InitialAccountID != domain.ID(account["id"].(string)) || state.Source != domain.ExternalCLISession {
				t.Fatalf("public completion lost ownership: outcome=%s active=%s pending=%d initial=%t selected_account=%t source=%s", state.Outcome, state.ActiveExecutionID, state.PendingInputs, state.InitialExecution != nil, state.InitialExecution != nil && state.InitialExecution.InitialAccountID == domain.ID(account["id"].(string)), state.Source)
			}
			replay := run(create, input)
			if replay["replayed"] != true || replay["execution_job"] == nil {
				t.Fatal("creation replay lost current job")
			}
			job := replay["execution_job"].(map[string]any)
			var value domain.Job
			raw, _ := json.Marshal(job["data"])
			if domain.Decode(raw, &value) != nil || value.State != domain.JobSucceeded {
				t.Fatal("native outcome was not reported")
			}
			if err := process.ReconcileOwner(filepath.Join(workerRoot, "processes"), domain.ID(job["id"].(string))); err != nil {
				t.Fatal("native process cleanup not proven", err)
			}
			checkRoots := func() {
				t.Helper()
				if !multipleRepositories && scenario.workspace != domain.Local {
					return
				}
				var assignment domain.ExecutionJobInput
				if domain.Decode(value.Input, &assignment) != nil {
					t.Fatal("invalid original multi-repository assignment")
				}
				var manifest workspace.Manifest
				if domain.Decode(assignment.Manifest, &manifest) != nil || len(manifest.Repositories) != scenario.repositories || manifest.PrimaryPath != manifest.Repositories[len(manifest.Repositories)-1].Path {
					t.Fatal("prepared manifest changed project order/primary")
				}
				raw, err := os.ReadFile(filepath.Join(workerRoot, "runtimes", string(state.Execution.ExecutionID), "native-completion.json"))
				if err != nil {
					t.Fatal(err)
				}
				var checkpoint worker.CodexExecutionCheckpoint
				var expectedRoots []string
				if multipleRepositories {
					expectedRoots = manifest.WorkspaceRoots()
				}
				if domain.Decode(raw, &checkpoint) != nil || !slices.Equal(checkpoint.Native.Effective.WorkspaceRoots, expectedRoots) || checkpoint.Native.Effective.Cwd != manifest.PrimaryPath {
					t.Fatal("native continuation lost exact repository roots/primary")
				}
				if scenario.workspace == domain.Local {
					if state.LocalOrigin == nil || state.LocalOrigin.MachineID != domain.ID(machine) || state.LocalOrigin.DeviceID == "" {
						t.Fatal("Local origin was lost")
					}
					var preparation workspace.PrepareRequest
					if domain.Decode(assignment.Preparation, &preparation) != nil || preparation.OriginMachineID != domain.ID(machine) {
						t.Fatal("Local preparation lost origin")
					}
					for i, checkout := range localCheckouts {
						if manifest.Repositories[i].Path != checkout.root || manifest.Repositories[i].Owned || manifest.Repositories[i].StartingCommit != checkout.head || preparation.Repositories[i].AutoFetch || preparation.Repositories[i].Starting.Type != "" {
							t.Fatal("Local preparation moved or selected checkout contents")
						}
						git := exec.CommandContext(ctx, "git", "-C", checkout.root, "rev-parse", "HEAD")
						out, err := git.Output()
						if err != nil || strings.TrimSpace(string(out)) != checkout.head {
							t.Fatal("Local HEAD changed", err)
						}
						branch, err := exec.CommandContext(ctx, "git", "-C", checkout.root, "branch", "--show-current").Output()
						if err != nil || strings.TrimSpace(string(branch)) != "existing-local-branch" {
							t.Fatal("Local branch changed", err)
						}
						dirty, err := os.ReadFile(filepath.Join(checkout.root, "tracked.txt"))
						if err != nil || string(dirty) != "keep local dirty tree" {
							t.Fatal("Local dirty file changed", err)
						}
					}
				}
			}
			checkRoots()
			queue := run([]string{"queue", "list", "--session-id", id}, nil)["inputs"].([]any)
			expectedInputs, expectedMessages, expectedCalls := 1, 2, int64(1)
			if steerScenario {
				expectedInputs, expectedMessages, expectedCalls = 2, 4, 2
			}
			if len(queue) != expectedInputs || queue[0].(map[string]any)["data"].(map[string]any)["delivery"] != string(domain.InputAccepted) || queue[0].(map[string]any)["data"].(map[string]any)["mode"] != string(mode) {
				t.Fatal("public queue lost native input acceptance/mode")
			}
			messages := run([]string{"message", "list", "--session-id", id}, nil)["resources"].([]any)
			if len(messages) != expectedMessages {
				t.Fatal("public execution lost native transcript")
			}
			if calls.Load() != expectedCalls || validations.Load() != 1 {
				t.Fatal("public receipt replay repeated validation or inference")
			}
			initialBytes, _ := json.Marshal(state.InitialExecution)
			originalThread := state.Execution.NativeThreadID
			previousExecution := state.Execution.ExecutionID
			waitTurn := func(count int, inputID string) {
				t.Helper()
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					raw, _ := json.Marshal(current["data"])
					state = domain.Session{}
					if domain.Decode(raw, &state) != nil {
						t.Fatal("invalid continuation session")
					}
					if state.Recovery != domain.NoRecovery || state.Outcome == domain.ExecutionFailed {
						t.Fatalf("continuation %d failed: %+v", count, state)
					}
					if state.Execution != nil && state.Execution.InputID == domain.ID(inputID) && state.Execution.CleanupVerified {
						break
					}
					select {
					case <-time.After(30 * time.Millisecond):
					case <-ctx.Done():
						t.Fatalf("continuation %d timed out: %+v", count, state)
					}
				}
				retained, _ := json.Marshal(state.InitialExecution)
				if string(retained) != string(initialBytes) || state.Execution.NativeThreadID != originalThread || state.Execution.ExecutionID == previousExecution || state.Dispatch != domain.DispatchReady || state.Outcome != domain.ExecutionSucceeded || state.ActiveExecutionID != "" || state.NextExecutionIntent != domain.ContinueAutomatically {
					t.Fatal("continuation replaced history, snapshot or ownership")
				}
				checkRoots()
				previousExecution = state.Execution.ExecutionID
				if calls.Load() != int64(count) {
					t.Fatal("continuation replayed or skipped native input", calls.Load(), count)
				}
			}
			enqueue := func(prompt string, mode domain.SessionMode) string {
				return run([]string{"session", "enqueue", "--id", id}, domain.SessionInput{Prompt: prompt, Mode: mode})["input"].(map[string]any)["id"].(string)
			}
			if steerScenario {
				if len(state.Execution.AcceptedInputs) != 2 || state.PendingSteerID != "" {
					t.Fatal("Steer lost ordered acceptance/checkpoint evidence")
				}
				retried := run(steerArgs, nil)
				if retried["replayed"] != true || retried["steer"].(map[string]any)["data"].(map[string]any)["state"] != string(domain.SteerAccepted) {
					t.Fatal("old Steer receipt lost current state")
				}
				followup := enqueue("Public continuation after Steer", domain.ExecuteMode)
				waitTurn(3, followup)
				if validations.Load() != 1 {
					t.Fatal("Steer replaced account validation")
				}
				t.Log("public CLI selected queue -> same-turn native Steer -> exact acceptance/transcript -> owned cleanup -> process-replacement FIFO continuation; no seeded readiness or external inference")
				return
			}
			// Complete two FIFO turns in fresh native processes on the original history.
			second := enqueue("Public second prompt", domain.PlanMode)
			third := enqueue("Public third prompt", domain.ExecuteMode)
			_ = second
			waitTurn(3, third)
			control := func(action string) map[string]any {
				t.Helper()
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					code, result := cliRun(t, root, []string{"session", action, "--id", id, "--revision", revision(current)}, "")
					if code == 0 {
						return result["result"].(map[string]any)
					}
					if result["error"].(map[string]any)["code"] != string(domain.Conflict) {
						t.Fatalf("public control failed: %v", result)
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("control revision never stabilized")
					}
				}
			}
			control("stop")
			fourth := enqueue("Public fourth prompt", domain.ExecuteMode)
			select {
			case <-time.After(1200 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal("fixture deadline")
			}
			if calls.Load() != 3 {
				t.Fatal("enqueue resumed a stopped session")
			}
			control("resume")
			waitTurn(4, fourth)
			// An explicit empty Resume retains authorization for the next queued entry.
			control("stop")
			resumed := control("resume")["session"].(map[string]any)["data"].(map[string]any)
			if resumed["next_execution_intent"] != string(domain.ContinueExplicitly) {
				t.Fatal("empty Resume lost its explicit intent")
			}
			fifth := enqueue("Public fifth prompt", domain.PlanMode)
			waitTurn(5, fifth)
			replay = run(create, input)
			if replay["execution_job"].(map[string]any)["id"] == job["id"] {
				t.Fatal("old creation receipt did not join the latest turn")
			}
			queue = run([]string{"queue", "list", "--session-id", id}, nil)["inputs"].([]any)
			messages = run([]string{"message", "list", "--session-id", id}, nil)["resources"].([]any)
			if len(queue) != 5 || len(messages) != 10 || validations.Load() != 1 {
				t.Fatal("continuation lost ordered input/transcript or repeated provider validation")
			}
			for n, entry := range queue {
				data := entry.(map[string]any)["data"].(map[string]any)
				if data["delivery"] != string(domain.InputAccepted) || data["sequence"] != float64(n+1) {
					t.Fatal("FIFO input ownership changed")
				}
			}
			if mode == domain.ExecuteMode && (scenario.workspace == domain.GeneralChat || multipleRepositories) {
				sixth := enqueue("Public interrupted prompt", domain.ExecuteMode)
				select {
				case <-interruptedRequest:
				case <-ctx.Done():
					t.Fatal("interruption fixture did not start")
				}
				// Provider submission can precede the public input-accepted event. Stop
				// only after that durable boundary to test a real retained native interrupt.
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					raw, _ := json.Marshal(current["data"])
					state = domain.Session{}
					_ = domain.Decode(raw, &state)
					if state.Execution != nil && state.Execution.InputID == domain.ID(sixth) && state.Execution.Outcome == domain.ExecutionRunning && state.Execution.LastSequence >= 4 {
						break
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("native input was not accepted")
					}
				}
				control("stop")
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					raw, _ := json.Marshal(current["data"])
					state = domain.Session{}
					_ = domain.Decode(raw, &state)
					if state.Recovery != domain.NoRecovery {
						t.Fatal("native Stop lost cleanup proof")
					}
					if state.Execution != nil && state.Execution.CleanupVerified {
						break
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("native Stop did not finish")
					}
				}
				if state.Execution.Outcome != domain.ExecutionStopped || state.Dispatch != domain.DispatchPaused || state.ActiveExecutionID != "" {
					t.Fatal("native interrupt did not remain paused")
				}
				checkRoots()
				seventh := enqueue("Public resumed interrupted prompt", domain.PlanMode)
				control("resume")
				waitTurn(7, seventh)
				// Changed retained evidence must fail before provider work, with the next
				// claimed input uncertain and its predecessor/history still reachable.
				checkpoint := filepath.Join(workerRoot, "runtimes", string(state.Execution.ExecutionID), "native-completion.json")
				if err := os.WriteFile(checkpoint, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				eighth := enqueue("Public blocked corrupt checkpoint", domain.ExecuteMode)
				for {
					current := run([]string{"session", "get", "--id", id}, nil)
					raw, _ := json.Marshal(current["data"])
					state = domain.Session{}
					_ = domain.Decode(raw, &state)
					if state.Recovery == domain.NeedsRecovery {
						break
					}
					select {
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						t.Fatal("corrupt evidence was not rejected")
					}
				}
				if calls.Load() != 7 || state.Dispatch != domain.DispatchPaused || state.CurrentExecution == nil || state.CurrentExecution.InputID != domain.ID(eighth) || state.PendingInputs != 1 {
					t.Fatal("corrupt checkpoint replayed native input or discarded its claim")
				}
				t.Log("native interruption/Resume and corrupt-checkpoint refusal passed")
			}
			t.Log("Public CLI first execution, FIFO continuation, queued/future Resume, immutable history/settings and receipt joins passed without seeded readiness")
		})
	}
}
