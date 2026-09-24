package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type threadFixture struct {
	mode                   string
	thread                 map[string]any
	turn                   domain.ID
	turnInput              domain.ID
	steerCount             int
	history                json.RawMessage
	historyChangeAfterRead bool
	historyNotification    *fixtureHistoryNotification
}

func (f *threadFixture) handle(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if f.handleContinuation(id, method, raw, write) {
		return true
	}
	if f.handleTurn(id, method, raw, write) {
		return true
	}
	if method != string(startThread) && method != string(resumeThread) && method != string(readThread) {
		return false
	}
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		os.Exit(20)
	}
	if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
		out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(21)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
		_ = out.Close()
	}
	if method == string(readThread) {
		if params["includeTurns"] != false {
			os.Exit(22)
		}
		if f.thread == nil {
			os.Exit(23)
		}
		write(id, map[string]any{"thread": f.thread})
		return true
	}
	if f.mode == "thread-rejected" || f.mode == "thread-internal-error" {
		code := -32602
		if f.mode == "thread-internal-error" {
			code = -32603
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "error": map[string]any{"code": code, "message": "fixture-protected-diagnostic", "data": "fixture-protected-details"}})
		return true
	}
	if params["experimentalRawEvents"] != true {
		os.Exit(26)
	}
	threadID := domain.NewID()
	if method == string(startThread) && (params["historyMode"] != "legacy" || params["ephemeral"] != false || params["allowProviderModelFallback"] != false) {
		os.Exit(25)
	}
	if method == string(resumeThread) {
		if params["excludeTurns"] != true {
			os.Exit(24)
		}
		threadID = domain.ID(params["threadId"].(string))
	}
	f.thread = map[string]any{"id": threadID, "sessionId": threadID, "cliVersion": SupportedVersion, "cwd": params["cwd"], "modelProvider": params["modelProvider"], "createdAt": int64(1), "updatedAt": int64(1), "ephemeral": false, "preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}}
	f.thread["historyMode"] = "legacy"
	f.thread["extra"] = nil
	f.thread["canAcceptDirectInput"] = true
	policy := params["approvalPolicy"]
	if policy == nil {
		policy = "on-request"
	}
	sandbox := "workspaceWrite"
	switch params["sandbox"] {
	case "read-only":
		sandbox = "readOnly"
	case "danger-full-access":
		sandbox = "dangerFullAccess"
	}
	effort := any(nil)
	if config, ok := params["config"].(map[string]any); ok {
		effort = config["model_reasoning_effort"]
	}
	result := map[string]any{"thread": f.thread, "model": params["model"], "modelProvider": params["modelProvider"], "cwd": params["cwd"], "approvalPolicy": policy, "approvalsReviewer": "user", "sandbox": map[string]any{"type": sandbox}, "reasoningEffort": effort, "serviceTier": params["serviceTier"], "instructionSources": []string{}}
	result["runtimeWorkspaceRoots"] = []string{}
	result["activePermissionProfile"] = nil
	result["multiAgentMode"] = "explicitRequestOnly"
	switch f.mode {
	case "thread-model":
		result["model"] = "foreign"
	case "thread-provider":
		result["modelProvider"] = "foreign"
	case "thread-effort":
		result["reasoningEffort"] = "low"
	case "thread-tier":
		result["serviceTier"] = nil
	case "thread-policy":
		result["approvalPolicy"] = "never"
	case "thread-reviewer":
		result["approvalsReviewer"] = "auto_review"
	case "thread-sandbox":
		result["sandbox"] = map[string]any{"type": "dangerFullAccess"}
	case "thread-root":
		result["sandbox"] = map[string]any{"type": "workspaceWrite", "writableRoots": []string{"/foreign"}}
	case "thread-cwd":
		result["cwd"] = "/foreign"
	case "thread-version":
		f.thread["cliVersion"] = "9.9.9"
	case "thread-identity":
		f.thread["id"] = "invalid"
	case "thread-foreign":
		f.thread["id"] = domain.NewID()
	case "thread-unknown-field":
		result["replacementMeaning"] = true
	case "thread-missing-status":
		delete(f.thread, "status")
	case "thread-history":
		f.thread["turns"] = []any{map[string]any{"id": domain.NewID()}}
	case "thread-history-mode":
		f.thread["historyMode"] = "paginated"
	case "thread-runtime-root":
		result["runtimeWorkspaceRoots"] = []string{"/foreign"}
	case "thread-delegation":
		result["multiAgentMode"] = "proactive"
	case "thread-resume-active":
		f.thread["status"] = map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}
	case "thread-late":
		time.Sleep(250 * time.Millisecond)
	}
	write(id, result)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "thread/started", "params": map[string]any{"thread": f.thread}})
	return true
}
func threadSettings(t *testing.T) ThreadSettings {
	t.Helper()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ThreadSettings{Model: "fixture-model", Provider: "fixture-provider", Effort: "high", Cwd: cwd, Instructions: "First template\n\nSecond template: 한글 🐦", Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: "on-request", ServiceTier: "fast"}}
}
func openThreadFixture(t *testing.T, mode string) (*Client, string) {
	t.Helper()
	config := fixtureConfig(t, mode)
	config.Mode = ThreadProtocol
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client, capture
}
func capturedThreads(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("invalid fixture capture")
		}
		result = append(result, row)
	}
	return result
}
func TestThreadBindingUsesExactSettingsAndAdditiveInstructions(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-ready")
	settings := threadSettings(t)
	requestID := domain.NewID()
	result, err := client.StartThread(context.Background(), requestID, settings)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != requestID || result.Thread == nil || result.Effective == nil || result.Effective.Model != settings.Model || result.Effective.Effort == nil || *result.Effective.Effort != settings.Effort {
		t.Fatalf("wrong native binding: %#v", result)
	}
	rows := capturedThreads(t, capture)
	params := rows[0]["params"].(map[string]any)
	if params["developerInstructions"] != settings.Instructions || params["baseInstructions"] != nil || params["model"] != settings.Model || params["modelProvider"] != settings.Provider || params["cwd"] != settings.Cwd || params["sandbox"] != "workspace-write" || params["approvalsReviewer"] != "user" || params["serviceTier"] != "fast" {
		t.Fatalf("wrong native translation: %v", params)
	}
	if params["config"].(map[string]any)["model_reasoning_effort"] != "high" {
		t.Fatal("effort omitted")
	}
	observed, err := client.ReadThread(context.Background(), domain.NewID(), result.Thread.ID)
	if err != nil || observed.ID != result.Thread.ID {
		t.Fatalf("native metadata read failed: %v", err)
	}
	_, err = client.ReadThread(context.Background(), domain.NewID(), domain.NewID())
	if err == nil || domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatalf("foreign read: %v", err)
	}
	_, err = client.StartThread(context.Background(), domain.NewID(), settings)
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("duplicate root: %v", err)
	}
	if len(capturedThreads(t, capture)) != 2 {
		t.Fatal("invalid operation reached native protocol")
	}
}
func TestThreadResumePreservesIdentityAndReportsActiveState(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-resume-active")
	settings := threadSettings(t)
	id := domain.NewID()
	result, err := client.ResumeThread(context.Background(), domain.NewID(), id, settings)
	if err != nil || result.Thread == nil || result.Thread.ID != id || result.Thread.Status.Type != ThreadActive || !slices.Equal(result.Thread.Status.ActiveFlags, []ActiveFlag{WaitingApproval}) {
		t.Fatalf("wrong resumed state: %#v %v", result, err)
	}
	row := capturedThreads(t, capture)[0]
	params := row["params"].(map[string]any)
	if row["method"] != "thread/resume" || params["threadId"] != string(id) || params["excludeTurns"] != true || params["history"] != nil || params["path"] != nil {
		t.Fatalf("resume used replacement history: %v", params)
	}
}
func TestThreadNativeDefaultsAreObservableWithoutInventingThem(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-ready")
	settings := threadSettings(t)
	settings.Effort = ""
	settings.Instructions = ""
	settings.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	result, err := client.StartThread(context.Background(), domain.NewID(), settings)
	if err != nil || result.Effective == nil || result.Effective.Effort != nil || result.Effective.ServiceTier != nil || result.Effective.Sandbox.Type != WorkspaceWrite {
		t.Fatalf("default observation: %#v %v", result, err)
	}
	params := capturedThreads(t, capture)[0]["params"].(map[string]any)
	for _, field := range []string{"config", "developerInstructions", "sandbox", "approvalPolicy", "serviceTier"} {
		if _, ok := params[field]; ok {
			t.Errorf("default override %s", field)
		}
	}
}
func TestThreadInvalidSettingsDoNotConsumeRequestIdentity(t *testing.T) {
	changes := []func(*ThreadSettings){
		func(s *ThreadSettings) { s.Model = "" }, func(s *ThreadSettings) { s.Cwd = "relative" }, func(s *ThreadSettings) { s.Options.SubagentModel = "other" }, func(s *ThreadSettings) { s.Options.SubagentEffort = "high" }, func(s *ThreadSettings) { s.Options.MaxConcurrency = 3 }, func(s *ThreadSettings) { s.Options.ApprovalReviewModel = "other" }, func(s *ThreadSettings) { s.Options.ApprovalPolicy = "invented" }, func(s *ThreadSettings) { s.Options.Permission = "invented" }, func(s *ThreadSettings) { s.Instructions = strings.Repeat("x", (256<<10)+1) },
	}
	client, capture := openThreadFixture(t, "thread-ready")
	settings := threadSettings(t)
	id := domain.NewID()
	for i, change := range changes {
		invalid := settings
		change(&invalid)
		if _, err := client.StartThread(context.Background(), id, invalid); err == nil {
			t.Fatalf("accepted invalid settings %d", i)
		}
	}
	if len(capturedThreads(t, capture)) != 0 {
		t.Fatal("invalid settings were sent")
	}
	if _, err := client.StartThread(context.Background(), id, settings); err != nil {
		t.Fatal(err)
	}
}
func TestThreadChangedEffectiveSettingsAndMalformedRepliesRequireRecovery(t *testing.T) {
	for _, mode := range []string{"model", "provider", "effort", "tier", "policy", "reviewer", "sandbox", "root", "cwd", "version", "identity", "unknown-field", "missing-status", "history", "foreign", "history-mode", "runtime-root", "delegation"} {
		t.Run(mode, func(t *testing.T) {
			client, capture := openThreadFixture(t, "thread-"+mode)
			settings := threadSettings(t)
			_, err := client.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), settings)
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("unsafe native result: %v", err)
			}
			_, err = client.StartThread(context.Background(), domain.NewID(), settings)
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || len(capturedThreads(t, capture)) != 1 {
				t.Fatalf("mismatched response allowed another native start: %v", err)
			}
		})
	}
}
func TestThreadLateAcknowledgmentRetainsOriginalIdentityWithoutRetry(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-late")
	settings := threadSettings(t)
	id := domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.StartThread(ctx, id, settings)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("lost acknowledgment: %v", err)
	}
	events, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	for {
		observation, err := client.NextEvent(events)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Native == nil {
			continue
		}
		event := *observation.Native
		if event.Kind != nativewire.LateResponse {
			continue
		}
		if string(event.ID) != `"`+string(id)+`"` {
			t.Fatal("late response identity changed")
		}
		thread, _, err := decodeBoundThread(event.Response.Result, settings, "", startThread)
		if err != nil || thread == nil {
			t.Fatalf("late response lost state: %v", err)
		}
		break
	}
	_, err = client.StartThread(context.Background(), domain.NewID(), settings)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || len(capturedThreads(t, capture)) != 1 {
		t.Fatal("late acknowledgment authorized another thread")
	}
}
func TestThreadConcurrentBindingsAndCanceledWaiters(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-late")
	settings := threadSettings(t)
	started := make(chan error, 1)
	go func() { _, err := client.StartThread(context.Background(), domain.NewID(), settings); started <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		if info, err := os.Stat(capture); err == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.StartThread(ctx, domain.NewID(), settings)
	if err == nil || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatalf("waiter did not cancel: %v", err)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.StartThread(context.Background(), domain.NewID(), settings)
			if err == nil || domain.SafeError(err).Code != domain.Conflict {
				t.Errorf("concurrent binding: %v", err)
			}
		}()
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if len(capturedThreads(t, capture)) != 1 {
		t.Fatal("concurrent operation created duplicate native threads")
	}
}
func TestThreadErrorsAreRedactedAndOnlyDefiniteRejectionAllowsRetry(t *testing.T) {
	for _, mode := range []string{"rejected", "internal-error"} {
		t.Run(mode, func(t *testing.T) {
			client, capture := openThreadFixture(t, "thread-"+mode)
			settings := threadSettings(t)
			for range 2 {
				_, err := client.StartThread(context.Background(), domain.NewID(), settings)
				if err == nil || strings.Contains(err.Error(), "fixture-protected") {
					t.Fatalf("native diagnostic leaked: %v", err)
				}
				code := domain.InvalidArgument
				if mode == "internal-error" {
					code = domain.RecoveryRequired
				}
				if domain.SafeError(err).Code != code {
					t.Fatalf("wrong rejection: %v", err)
				}
			}
			want := 2
			if mode == "internal-error" {
				want = 1
			}
			if len(capturedThreads(t, capture)) != want {
				t.Fatal("incorrect native retry eligibility")
			}
		})
	}
}

func TestProtocolProbeCannotCreateOrResumeThreads(t *testing.T) {
	config := fixtureConfig(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := threadSettings(t)
	for _, invoke := range []func() error{
		func() error { _, err := client.StartThread(ctx, domain.NewID(), settings); return err },
		func() error { _, err := client.ResumeThread(ctx, domain.NewID(), domain.NewID(), settings); return err },
		func() error { _, err := client.ReadThread(ctx, domain.NewID(), domain.NewID()); return err },
	} {
		if err := invoke(); err == nil || domain.SafeError(err).Code != domain.Unsupported {
			t.Fatalf("probe crossed thread control boundary: %v", err)
		}
	}
	if client.wire.Err() != nil {
		t.Fatal("a rejected operation reached the probe fixture")
	}
}

func TestMismatchedResumeCannotReplaceInspectionAuthority(t *testing.T) {
	client, capture := openThreadFixture(t, "thread-foreign")
	settings := threadSettings(t)
	original := domain.NewID()
	result, err := client.ResumeThread(context.Background(), domain.NewID(), original, settings)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || result.Thread == nil || result.Thread.ID == original {
		t.Fatal("fixture did not return a conflicting identity")
	}
	_, err = client.ReadThread(context.Background(), domain.NewID(), result.Thread.ID)
	if err == nil || domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatalf("foreign reply gained inspection authority: %v", err)
	}
	_, err = client.ReadThread(context.Background(), domain.NewID(), original)
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("original inspection did not validate the native mismatch: %v", err)
	}
	rows := capturedThreads(t, capture)
	if len(rows) != 2 || rows[1]["params"].(map[string]any)["threadId"] != string(original) {
		t.Fatal("the original identity was not retained for inspection")
	}
}

func TestSelectionValidationDoesNotReadCoordinatorFilesystem(t *testing.T) {
	settings := ThreadSettings{Model: "fixture-model", Provider: APIProvider, Cwd: `C:\Worker\session\workspace`, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}}
	if err := ValidateSelection(settings); err != nil {
		t.Fatal("remote wire selection was interpreted on the coordinator filesystem", err)
	}
	settings.Cwd = filepath.Join(t.TempDir(), "not-created")
	if err := ValidateThreadSettings(settings); err == nil {
		t.Fatal("owning native validation skipped filesystem readiness")
	}
	settings.Options.MaxConcurrency = 2
	if err := ValidateSelection(settings); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("coordinator accepted unsupported native settings", err)
	}
}
