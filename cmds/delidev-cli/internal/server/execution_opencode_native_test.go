package server

import (
	"bytes"
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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Account readiness and the claimed assignment are seeded fixture state. The
// authenticated registration, revocable server authority, protected-key lookup,
// relay, pinned native process and original storage inspection are real. This
// does not exercise public dispatch, Worker publication or hosted inference.
func TestManualNativeOpenCodeUsesRegisteredServerRelay(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredOpenCode(t, mode, false, nativeNoPublication) })
	}
}

func TestManualNativeOpenCodeRegisteredRelayRevocation(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, true, nativeNoPublication)
}

func TestManualNativeOpenCodePublishesRegisteredBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredOpenCode(t, mode, false, nativeBindingPublication) })
	}
}

type nativeOpenCodePublication uint8

const (
	nativeNoPublication nativeOpenCodePublication = iota
	nativeBindingPublication
	nativeTextPublication
	nativeReasoningPublication
	nativeReadPublication
	nativeReadFailurePublication
	nativeReadDirectoryPublication
	nativeUsagePublication
	nativeTerminalPublication
	nativeTerminalLostAckPublication
	nativeTerminalAuthFailurePublication
	nativeShellPublication
	nativeShellNonzeroPublication
	nativeShellTimeoutPublication
	nativeShellTruncatedPublication
	nativeCompletionPublication
	nativeCompletionAuthFailurePublication
	nativeTodoPublication
	nativeTodoClearPublication
	nativeShellChangesPublication
)

func TestManualNativeOpenCodePublishesRegisteredText(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredOpenCode(t, mode, false, nativeTextPublication) })
	}
}

func TestManualNativeOpenCodePublishesRegisteredReasoning(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, false, nativeReasoningPublication)
}

func TestManualNativeOpenCodePublishesRegisteredRead(t *testing.T) {
	for _, scenario := range []nativeOpenCodePublication{nativeReadPublication, nativeReadFailurePublication, nativeReadDirectoryPublication} {
		t.Run(fmt.Sprint(scenario), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, scenario) })
	}
}

func TestManualNativeOpenCodePublishesRegisteredUsage(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, false, nativeUsagePublication)
}

func TestManualNativeOpenCodePublishesRegisteredTerminal(t *testing.T) {
	for _, scenario := range []nativeOpenCodePublication{nativeTerminalPublication, nativeTerminalLostAckPublication} {
		t.Run(fmt.Sprint(scenario), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, scenario) })
	}
}

func TestManualNativeOpenCodePublishesRegisteredAuthenticationFailure(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, false, nativeTerminalAuthFailurePublication)
}

func TestManualNativeOpenCodePublishesRegisteredShell(t *testing.T) {
	for _, publication := range []nativeOpenCodePublication{nativeShellPublication, nativeShellNonzeroPublication, nativeShellTimeoutPublication, nativeShellTruncatedPublication} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, publication) })
	}
}

func TestManualNativeOpenCodeReportsOriginalCompletion(t *testing.T) {
	for _, publication := range []nativeOpenCodePublication{nativeCompletionPublication, nativeCompletionAuthFailurePublication} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, publication) })
	}
}

func TestManualNativeOpenCodePublishesRegisteredTodos(t *testing.T) {
	for _, publication := range []nativeOpenCodePublication{nativeTodoPublication, nativeTodoClearPublication} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, publication) })
	}
}

func TestManualNativeOpenCodePublishesRegisteredChanges(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, false, nativeShellChangesPublication)
}

func nativeRegisteredOpenCode(t *testing.T, mode domain.SessionMode, revoke bool, publication nativeOpenCodePublication) {
	todoTool := publication == nativeTodoPublication || publication == nativeTodoClearPublication
	todos := []domain.OpenCodeTodo{{Content: "Original native Todo fixture.", Status: domain.OpenCodeTodoRunning, Priority: domain.OpenCodeTodoHigh}, {Content: "Preserve native cancellation", Status: domain.OpenCodeTodoCancelled, Priority: domain.OpenCodeTodoLow}, {Content: "Native extension", Status: "waiting", Priority: "urgent"}}
	if publication == nativeTodoClearPublication {
		todos = []domain.OpenCodeTodo{}
	}
	publish := publication != nativeNoPublication
	hasTranscript := publication >= nativeTextPublication
	authFailure := publication == nativeTerminalAuthFailurePublication || publication == nativeCompletionAuthFailurePublication
	shellTool := publication == nativeShellPublication || publication == nativeShellNonzeroPublication || publication == nativeShellTimeoutPublication || publication == nativeShellTruncatedPublication || publication == nativeShellChangesPublication
	const shellSentinel = "Original native Shell fixture."
	shellCommand := "printf 'Original native Shell fixture.'; printf 'Original stderr fixture.' >&2"
	if publication == nativeShellNonzeroPublication {
		shellCommand += "; exit 7"
	}
	if publication == nativeShellTimeoutPublication {
		shellCommand += "; sleep 2"
	}
	if publication == nativeShellChangesPublication {
		shellCommand += "; printf 'after native edit\\n' > changed.txt; printf 'new native file\\n' > new.txt"
	}
	readTool := false
	switch publication {
	case nativeReadPublication, nativeReadFailurePublication, nativeReadDirectoryPublication, nativeUsagePublication, nativeTerminalPublication, nativeTerminalLostAckPublication, nativeCompletionPublication:
		readTool = true
	}
	if publication == nativeShellTruncatedPublication {
		shellCommand = `i=0; while [ "$i" -lt 3000 ]; do printf 'Original native Shell fixture.\n'; i=$((i + 1)); done; printf 'Original stderr fixture.' >&2`
	}
	expectedCalls := int32(1)
	if readTool || shellTool || todoTool {
		expectedCalls = 2
	}
	var readPath atomic.Value
	const readSentinel = "Original native Read fixture."
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary with generated state and a scripted provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the selected native executable must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var calls atomic.Int32
	started, ended := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call > expectedCalls {
			t.Error("native registered input repeated inference")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Model    string
			Stream   bool
			Messages []struct {
				Role       string
				Content    json.RawMessage
				ToolCallID string `json:"tool_call_id"`
			}
		}
		if err != nil || json.Unmarshal(raw, &body) != nil || body.Model != "fixture-model" || !body.Stream || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("native registered request lost its exact model or server-owned account key")
		}
		originalInputs := 0
		for _, message := range body.Messages {
			if message.Role != "user" {
				continue
			}
			var text string
			if json.Unmarshal(message.Content, &text) == nil {
				if text == "Fixture prompt" {
					originalInputs++
				}
				continue
			}
			// Native Plan adds its own reminder as a separate text part.
			// Preserve the exact original user text without treating native
			// policy context as a replacement DeliDev instruction.
			var parts []struct{ Type, Text string }
			if err := json.Unmarshal(message.Content, &parts); err != nil {
				t.Error("native registered user content has an unsupported shape")
			}
			for _, part := range parts {
				if part.Type == "text" && part.Text == "Fixture prompt" {
					originalInputs++
				}
			}
		}
		if originalInputs != 1 {
			t.Error("native registered request changed or repeated its original input")
		}
		if call == 1 {
			close(started)
			defer close(ended)
		}
		if revoke {
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
				t.Error("account revocation did not cancel the actual provider request")
			}
			return
		}
		if authFailure {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-Id", "temporary-upstream-fixture-key")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_api_key", "message": "temporary-upstream-fixture-key"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if (readTool || shellTool || todoTool) && call == 1 {
			name, callID := "read", "call_registered_read"
			var args []byte
			if todoTool {
				name, callID = "todowrite", "call_registered_todo"
				args, _ = json.Marshal(map[string]any{"todos": todos})
			} else if shellTool {
				name, callID = "bash", "call_registered_shell"
				input := map[string]any{"command": shellCommand}
				if publication == nativeShellTimeoutPublication {
					input["timeout"] = 50
				}
				args, _ = json.Marshal(input)
			} else {
				args, _ = json.Marshal(map[string]any{"filePath": readPath.Load().(string)})
			}
			delta := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}}}
			for _, choice := range []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}, {"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}} {
				raw, _ := json.Marshal(map[string]any{"id": "chatcmpl-registered-read", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{choice}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		if readTool || shellTool || todoTool {
			callID, sentinel := "call_registered_read", readSentinel
			if shellTool {
				callID, sentinel = "call_registered_shell", shellSentinel
			}
			if todoTool {
				callID, sentinel = "call_registered_todo", "Original native Todo fixture."
				if len(todos) == 0 {
					sentinel = "[]"
				}
			}
			results := 0
			for _, message := range body.Messages {
				if message.Role != "tool" || message.ToolCallID != callID {
					continue
				}
				var result string
				if json.Unmarshal(message.Content, &result) != nil || !strings.Contains(result, sentinel) {
					t.Error("original Read result did not reach the next native provider request")
				}
				results++
			}
			if results != 1 {
				t.Error("native Read result was missing or duplicated")
			}
		}

		if publication == nativeReasoningPublication {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-registered","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Native reasoning fixture."},"finish_reason":null}]}`+"\n\n")
		}
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-registered","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Registered OpenCode fixture completed."},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-registered","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, upstream.URL, domain.OpenCode, domain.OpenAIChat, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false))
	f.registerGrant(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot, workspace := filepath.Join(root, "runtime"), filepath.Join(root, "workspace")
	env, err := harness.PrivateRuntimeEnvironment(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	if publication == nativeShellChangesPublication {
		for _, args := range [][]string{{"init", "-q", "--initial-branch=main"}, {"config", "core.hooksPath", filepath.Join(root, "no-hooks")}} {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = workspace
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("private git fixture: %v %s", err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(workspace, "changed.txt"), []byte("before native edit\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "changed.txt"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "Private fixture"}} {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = workspace
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("private git fixture: %v %s", err, out)
			}
		}
	}
	if readTool {
		path := filepath.Join(workspace, readSentinel)
		if publication == nativeReadDirectoryPublication {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else if publication != nativeReadFailurePublication {
			if err := os.WriteFile(path, []byte(readSentinel+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		readPath.Store(path)
	}
	agent, err := f.input.Configuration.Options.OpenCodePrimaryForInput(mode)
	if err != nil {
		t.Fatal(err)
	}
	settings := opencode.SessionSettings{Title: "Original registered session", Agent: agent, Provider: "delidev", Model: f.input.Configuration.NativeModel, Permission: []opencode.PermissionRule{}}
	var claims []opencode.SessionClaim
	claim := func(ctx context.Context, value opencode.SessionClaim) error {
		if err := value.Validate(); err != nil {
			return err
		}
		switch len(claims) {
		case 0:
			if value.Kind != opencode.CreateSessionMutation || value.RequestID != f.input.ThreadRequestID {
				return executionDenied()
			}
		case 1:
			digest, err := opencode.TextInputClaimDigest(settings, value.MessageID, value.PartID, f.input.Input.Prompt)
			if err != nil || value.Kind != opencode.SubmitInputMutation || value.RequestID != f.input.TurnRequestID || value.BodyDigest != digest {
				return executionDenied()
			}
		case 2:
			if !revoke || value.Kind != opencode.StopOwnedRuntimeMutation || value.InputRequestID != f.input.TurnRequestID || value.SessionID != claims[1].SessionID || value.MessageID != claims[1].MessageID {
				return executionDenied()
			}
		default:
			return executionDenied()
		}
		claims = append(claims, value)
		raw, err := json.Marshal(claims)
		if err != nil {
			return err
		}
		return security.WriteAtomic(filepath.Join(root, "claims.json"), raw)
	}
	var bindings *worker.OpenCodeBindingPublisher
	var executionPublisher *worker.ExecutionPublisher
	var publicationClient *losePublicationAck
	claimsPath := filepath.Join(root, "claims.json")
	if publish {
		cfg := publicationWorkerConfig(t, f)
		cfg.Root = filepath.Join(root, "worker")
		publicationClient = &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json")}
		cfg.Client = publicationClient
		publisher, err := worker.OpenExecutionPublisher(cfg)
		if err != nil {
			t.Fatal(err)
		}
		executionPublisher = publisher
		defer publisher.Close()
		bindings, err = worker.OpenOpenCodeBindingPublisher(publisher)
		if err != nil {
			t.Fatal(err)
		}
		defer bindings.Close()
		claim = bindings.Claim
		claimsPath = filepath.Join(cfg.Root, "jobs", string(f.job), "opencode-claims.json")
	}
	var nativeLogs bytes.Buffer
	nativeLogger := slog.New(slog.NewTextHandler(&nativeLogs, nil))
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(nativeLogs.String())
		}
	})
	config := opencode.APIExecutionConfig{
		Probe:     opencode.ProbeConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: nativeLogger}, Version: opencode.SupportedVersion, Home: filepath.Join(runtimeRoot, "opencode")},
		Workspace: workspace, NativeRoot: filepath.VolumeName(workspace) + string(filepath.Separator), ServerOrigin: f.http.URL, Token: f.token,
		Settings: settings, Rejection: opencode.StopOnInteractionRejection, Claim: claim,
	}
	if publication == nativeShellChangesPublication {
		config.NativeRoot = workspace
	}
	api, err := opencode.OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatalf("open original API: %v", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := api.Close(cleanup); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwnerContext(cleanup, config.Probe.Process.Directory, f.job); err != nil {
			t.Error(err)
		}
	}()
	observed, err := api.InitialSettings(ctx)
	if err != nil || observed.ValidateForInput(f.input.Configuration, mode) != nil {
		t.Fatal("native registered initialization changed the original requested settings")
	}
	session, err := api.CreateSession(ctx, f.input.ThreadRequestID)
	if err != nil {
		t.Fatalf("create original session: %v", err)
	}
	if publish {
		if bindings.BindSession(ctx, f.input.ThreadRequestID, session, observed) == nil {
			t.Fatal("expected lost original binding acknowledgement")
		}
		if err := bindings.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		publicationClient.dropAt = 3
	}
	if _, err := api.StartText(ctx, f.input.TurnRequestID, f.input.Input.Prompt); err != nil {
		t.Fatalf("start original input: %v", err)
	}
	if publish {
		raw, err := security.ReadPrivate(claimsPath, 1<<20)
		var retained struct {
			Claims []opencode.SessionClaim `json:"claims"`
		}
		if err != nil || json.Unmarshal(raw, &retained) != nil {
			t.Fatal("original Worker claims are unavailable")
		}
		claims = retained.Claims
	}
	if revoke {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("native original request did not reach the provider")
		}
		accounts := delidevv1connect.NewAccountServiceClient(f.http.Client(), f.http.URL)
		reply, err := accounts.DisconnectAccount(ctx, ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}))
		if err != nil || len(reply.Msg.CleanupProblemJson) != 0 {
			t.Fatalf("registered native account disconnect did not join relay cleanup: %v", err)
		}
		select {
		case <-ended:
		case <-ctx.Done():
			t.Fatal("provider request survived account disconnection")
		}
		if lease, err := f.service.executionAuthority.Acquire(ctx, f.token); err == nil {
			lease.Release()
			t.Fatal("native execution retained revoked account authority")
		}
		if _, _, retained := f.service.accountSecrets.(*accountTestSecrets).counts(); retained != 0 {
			t.Fatal("revoked native account retained its protected fixture key")
		}
		if _, err := api.ClaimOwnedStop(ctx, domain.NewID()); err != nil {
			t.Fatal(err)
		}
		receipt, err := api.FinishStopCleanup(ctx)
		if err != nil || !receipt.CleanupVerified || receipt.NativeAttempted || receipt.HTTPAccepted || receipt.TerminalObserved || len(claims) != 3 || calls.Load() != 1 {
			t.Fatalf("revoked execution lost original cleanup or fabricated native completion: %v", err)
		}
		return
	}
	var textPublisher *worker.OpenCodeTextPublisher
	var usagePublisher *worker.OpenCodeUsagePublisher
	var eventPublisher *worker.OpenCodeEventPublisher
	publishObservation := func(observation opencode.Observation) error {
		if eventPublisher != nil {
			err := eventPublisher.PublishObservation(ctx, observation)
			if err != nil {
				t.Logf("native fixture event kind=%s", observation.Kind)
				if observation.Part != nil {
					t.Logf("native fixture part kind=%s", observation.Part.Kind)
				}
			}
			return err
		}
		if textPublisher != nil {
			if _, err := textPublisher.PublishObservation(ctx, observation); err != nil {
				return err
			}
		}
		if usagePublisher != nil {
			if _, err := usagePublisher.PublishObservation(ctx, observation); err != nil {
				return err
			}
		}
		return nil
	}
	if publication >= nativeTextPublication {
		// Preserve the original prefix until both live input ownership and
		// independent native storage establish acceptance for publication.
		var prefix []opencode.Observation
		for len(prefix) < 256 {
			observation, err := api.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			prefix = append(prefix, observation)
			progress, err := api.Progress(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if progress.UserSeen && progress.InputPartSeen {
				break
			}
		}
		receipt, err := api.InspectInput(ctx)
		if err != nil || !receipt.Recorded {
			t.Fatal("original native input storage was not confirmed")
		}
		if bindings.AcceptInput(ctx, receipt) == nil {
			t.Fatal("expected lost original acceptance acknowledgement")
		}
		if err := bindings.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}

		if publication >= nativeTerminalPublication {
			eventPublisher, err = worker.OpenOpenCodeEventPublisher(bindings, api)
			if err == nil {
				if _, err := eventPublisher.Complete(ctx); err == nil {
					t.Fatal("unsettled input acquired cleanup completion")
				}
			}
		} else {
			textPublisher, err = worker.OpenOpenCodeTextPublisher(bindings)
			if err == nil && publication == nativeUsagePublication {
				usagePublisher, err = worker.OpenOpenCodeUsagePublisher(textPublisher)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, observation := range prefix {
			if err := publishObservation(observation); err != nil {
				t.Fatal(err)
			}
		}

	}
	var result string
	var final *opencode.NativeMessage
	for count := 0; count < 256; count++ {
		observation, err := api.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := publishObservation(observation); err != nil {
			t.Fatal(err)
		}
		if observation.Part != nil && observation.Part.Kind == opencode.TextPartKind && observation.Part.Text != nil {
			result = observation.Part.Text.Text
		}
		if observation.MessageFinalized {
			final = observation.Message
		}
		progress, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.SettledObserved {
			break
		}
	}
	progress, err := api.Progress(ctx)
	if err != nil || !progress.SettledObserved || progress.NeedsRecovery || final == nil || final.Assistant == nil || (!authFailure && (final.Assistant.Error != nil || result != "Registered OpenCode fixture completed.")) || calls.Load() != expectedCalls || len(claims) != 2 {
		t.Fatal("registered native original input did not settle with its exact result")
	}
	if authFailure {
		problem := final.Assistant.Error
		if problem == nil || problem.Kind != opencode.APIErrorKind || problem.StatusCode == nil || *problem.StatusCode != http.StatusUnauthorized || result != "" {
			t.Fatal("native credential rejection lost its original typed error or fabricated assistant text")
		}
	}
	receipt, err := api.InspectInput(ctx)
	if err != nil || !receipt.Recorded || receipt.SessionID != session || receipt.RequestID != f.input.TurnRequestID || receipt.MessageID != claims[1].MessageID || receipt.PartID != claims[1].PartID {
		t.Fatal("registered native input lost its separately verified original storage")
	}
	expectedOutcome := domain.ExecutionRunning
	terminalOutcome := domain.ExecutionSucceeded
	if authFailure {
		terminalOutcome = domain.ExecutionFailed
	}
	if eventPublisher != nil {
		if publication == nativeTerminalLostAckPublication {
			publicationClient.dropAt = len(publicationClient.calls) + 1
		}
		outcome, err := eventPublisher.PublishTerminal(ctx)
		if publication == nativeTerminalLostAckPublication {
			if _, err := eventPublisher.Complete(ctx); err == nil {
				t.Fatal("lost terminal acknowledgment acquired cleanup completion")
			}
			if err == nil || outcome != "" {
				t.Fatal("lost terminal acknowledgment became confirmed completion")
			}
			original := publicationClient.calls[len(publicationClient.calls)-1]
			if err := executionPublisher.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			if publicationClient.calls[len(publicationClient.calls)-1] != original {
				t.Fatal("terminal acknowledgment replay changed request identity")
			}
		} else if err != nil || outcome != terminalOutcome {
			t.Fatalf("original terminal publication failed: %v", err)
		}
		job, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
		value, decodeErr := store.Decode[domain.Job](job)
		if err != nil || decodeErr != nil || value.State != domain.JobClaimed {
			t.Fatal("native terminal observation fabricated owned cleanup or completion reporting")
		}
		expectedOutcome = terminalOutcome
		if _, err := eventPublisher.PublishTerminal(ctx); err == nil {
			t.Fatal("terminal publication was repeated")
		}
	}
	if publish {
		if !hasTranscript {
			if bindings.AcceptInput(ctx, receipt) == nil {
				t.Fatal("expected lost original acceptance acknowledgement")
			}
			if err := bindings.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if len(publicationClient.calls) < 4 || !hasTranscript && len(publicationClient.calls) != 4 || publicationClient.calls[0] != publicationClient.calls[1] || publicationClient.calls[2] != publicationClient.calls[3] {
			t.Fatal("original native publication replaced an uncertain RPC identity")
		}
		r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := store.Decode[domain.Session](r)
		if err != nil || retained.Execution == nil || (!hasTranscript && retained.Execution.LastSequence != 2 || hasTranscript && retained.Execution.LastSequence < 6) || retained.Execution.NativeThreadID != session || retained.Execution.NativeTurnID != receipt.MessageID || retained.PendingInputs != 0 || retained.PendingInputBytes != 0 || retained.Execution.Observed.OpenCodeAgent != agent || retained.Execution.Outcome != expectedOutcome || retained.Execution.CleanupVerified || authFailure && (retained.Problem == nil || retained.Problem.Code != domain.Unauthenticated) {
			t.Fatal("server binding did not preserve exact native ownership and independent unfinished publication")
		}
	}
	if hasTranscript {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 100})
		var filtered []store.Record
		revisions, patches, changedFiles := 0, 0, 0
		for _, row := range rows {
			m, decodeErr := store.Decode[domain.ExecutionMessage](row)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if m.Artifact != nil && m.Artifact.Started.Kind == domain.OpenCodeRevisionArtifact {
				if m.State != domain.MessageComplete || m.Artifact.Completed == nil || !reflect.DeepEqual(m.Artifact.Started, *m.Artifact.Completed) || m.NativeParentID == receipt.MessageID {
					t.Fatal("native revision lost immutable original closure")
				}
				revisions++
				if m.Artifact.Started.Revision.Source == domain.OpenCodePatchRevision {
					patches++
				}
				continue
			}
			if m.Progress != nil && m.Progress.Kind == domain.OpenCodeChangesProgressKind {
				changes := m.Progress.Changes
				if m.NativeID != "" || m.NativeParentID != "" || changes == nil || changes.Validate() != nil || changes.Source == domain.OpenCodeInputSummary && changes.NativeMessageID != receipt.MessageID {
					t.Fatal("native diff lost its original source")
				}
				for _, diff := range changes.Diffs {
					if diff.File != nil && *diff.File == "changed.txt" && diff.Patch != nil && strings.Contains(*diff.Patch, "before native edit") && strings.Contains(*diff.Patch, "after native edit") {
						changedFiles++
					}
				}
				continue
			}
			filtered = append(filtered, row)
		}
		if publication == nativeShellChangesPublication && (revisions < 4 || patches == 0 || changedFiles == 0) {
			t.Fatalf("native Git change evidence incomplete: revisions=%d patches=%d diffs=%d", revisions, patches, changedFiles)
		}
		rows = filtered
		expected := 2
		if authFailure {
			expected = 1
		}
		if publication == nativeReasoningPublication || readTool || shellTool || todoTool {
			expected = 3
		}
		if todoTool {
			expected = 4
		}
		if err != nil || len(rows) != expected {
			t.Fatal("native text publication lost original user/assistant/reasoning records")
		}
		roles := map[domain.MessageRole]bool{}
		for _, row := range rows {
			message, err := store.Decode[domain.ExecutionMessage](row)
			if err != nil || message.State != domain.MessageComplete || message.NativeTurnID != receipt.MessageID || message.Phase != nil || roles[message.Role] || (message.Role != domain.ProgressMessage && domain.NativeIdentity(message.NativeID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil) {
				t.Fatal("native text publication lost original part identity or closure")
			}
			roles[message.Role] = true
			if message.Role == domain.ProgressMessage && todoTool {
				if message.NativeID != "" || message.NativeParentID != "" || message.Progress == nil || message.Progress.Kind != domain.OpenCodeTodoProgressKind || message.Progress.Todo == nil || !reflect.DeepEqual(message.Progress.Todo.Todos, todos) || domain.NativeIdentity(message.Progress.Todo.NativeEventID).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil {
					t.Fatal("native todo event lost its independent identity or exact list")
				}
				record, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				retained, _ := store.Decode[domain.Session](record)
				if retained.Execution.LatestTodoID != row.ID {
					t.Fatal("latest todo reference was not retained")
				}
			} else if message.Role == domain.ToolMessage && todoTool {
				if message.Tool == nil || message.NativeParentID == "" || message.NativeParentID == receipt.MessageID || message.NativeParentID == progress.AssistantID || message.Tool.Started.Kind != domain.OpenCodeTodoTool || message.Tool.Started.Status != domain.ToolPending || message.Tool.Completed == nil || len(message.Tool.States) < 1 {
					t.Fatal("native todo tool lost its original lifecycle")
				}
				completed := message.Tool.Completed
				if completed.Status != domain.ToolCompleted || completed.Todo == nil || completed.Todo.CallID != "call_registered_todo" || !reflect.DeepEqual(completed.Todo.Input.Todos, todos) || completed.Todo.Metadata == nil || !reflect.DeepEqual(completed.Todo.Metadata.Todos, todos) {
					t.Fatal("native todo tool replaced its applied or result list")
				}
			} else if message.Role == domain.ToolMessage && shellTool {
				if message.Tool == nil || message.NativeParentID == "" || message.NativeParentID == receipt.MessageID || message.NativeParentID == progress.AssistantID || message.Tool.Started.Kind != domain.OpenCodeShellTool || message.Tool.Started.Status != domain.ToolPending || message.Tool.Completed == nil || len(message.Tool.States) < 1 {
					t.Fatal("original Shell lifecycle lost its ownership or observations")
				}
				completed := message.Tool.Completed
				shell := completed.Shell
				if completed.Status != domain.ToolCompleted || shell == nil || shell.Input.Command == nil || *shell.Input.Command != shellCommand || shell.CallID != "call_registered_shell" || shell.Output == nil || !strings.Contains(*shell.Output, shellSentinel) || !strings.Contains(*shell.Output, "Original stderr fixture.") || shell.Metadata == nil || shell.Metadata.Output == nil || !shell.Metadata.ExitObserved || shell.Metadata.Truncated == nil || *shell.Metadata.Truncated != (publication == nativeShellTruncatedPublication) {
					t.Fatal("original Shell output or metadata was replaced")
				}
				if publication == nativeShellTruncatedPublication && (shell.Metadata.OutputPath == nil || !strings.Contains(*shell.Output, *shell.Metadata.OutputPath)) {
					t.Fatal("native clipped output lost its original saved-output reference")
				}
				if publication == nativeShellTimeoutPublication {
					if shell.Metadata.Exit != nil || !strings.Contains(*shell.Output, "shell tool terminated command after exceeding timeout") || shell.Input.Timeout == nil || *shell.Input.Timeout != 50 {
						t.Fatal("native timeout fabricated an exit code or lost its original content")
					}
				} else {
					expectedExit := int64(0)
					if publication == nativeShellNonzeroPublication {
						expectedExit = 7
					}
					if shell.Metadata.Exit == nil || *shell.Metadata.Exit != expectedExit || shell.Input.Timeout != nil {
						t.Fatal("native exit status or omitted timeout was reinterpreted")
					}
				}
			} else if message.Role == domain.ToolMessage {
				if !readTool || message.Tool == nil || message.NativeParentID == "" || message.NativeParentID == receipt.MessageID || message.NativeParentID == progress.AssistantID || message.Tool.Started.Status != domain.ToolPending || message.Tool.Started.Kind != domain.OpenCodeReadTool || message.Tool.Completed == nil || len(message.Tool.States) != 1 || message.Tool.States[0].Snapshot.Status != domain.ToolRunning {
					t.Fatal("original Read proposal/running/result ownership was not preserved")
				}
				completed := message.Tool.Completed
				read := completed.Read
				if read == nil || read.CallID != "call_registered_read" || read.Input.FilePath == nil || *read.Input.FilePath != readPath.Load().(string) {
					t.Fatal("original Read operation was replaced")
				}
				if publication == nativeReadFailurePublication {
					if completed.Status != domain.ToolFailed || read.Error == nil || !strings.Contains(*read.Error, readSentinel) || read.Output != nil {
						t.Fatal("native Read failure became invented output")
					}
				} else if completed.Status != domain.ToolCompleted || read.Output == nil || !strings.Contains(*read.Output, readSentinel) || read.Error != nil || read.Metadata == nil || read.Metadata.Display == nil {
					t.Fatal("native Read result lost original metadata or content")
				}
				if publication == nativeReadDirectoryPublication && (read.Metadata.Display.Kind != domain.OpenCodeReadDirectory || read.Metadata.Display.Entries == nil || len(read.Metadata.Display.Entries) != 0) {
					t.Fatal("empty native directory entries were lost")
				}
			} else if message.Role == domain.ArtifactMessage {
				if publication != nativeReasoningPublication || message.NativeParentID != progress.AssistantID || message.Artifact == nil || message.Artifact.Started.Kind != domain.ReasoningTextArtifact || message.Artifact.Completed == nil || message.Artifact.Completed.Text != "Native reasoning fixture." || message.Artifact.Completed.Summary != nil || message.Artifact.Completed.Content != nil {
					t.Fatal("native reasoning became an invented indexed summary or answer")
				}
			} else if message.Role == domain.UserMessage {
				if message.Text != f.input.Input.Prompt || message.NativeParentID != receipt.MessageID || message.NativeID != receipt.PartID {
					t.Fatal("original user text was replaced")
				}
			} else if message.Role != domain.AssistantMessage || message.Text != result || message.NativeParentID != progress.AssistantID {
				t.Fatal("original assistant text/parent was replaced")
			}
		}
	}
	if usagePublisher != nil || eventPublisher != nil {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
		expected := 4
		if authFailure {
			expected = 1
		}
		if err != nil || len(rows) != expected {
			t.Fatal("original step and final-message observations were omitted or duplicated")
		}
		sourceCounts := map[domain.OpenCodeUsageSource]int{}
		for _, row := range rows {
			observation, err := store.Decode[domain.OpenCodeUsageRecord](row)
			if err != nil || observation.Usage.Validate() != nil || observation.Harness != domain.OpenCode || observation.ExecutionID != f.input.ExecutionID || observation.AccountID != f.input.AccountID || observation.TurnID != receipt.MessageID || observation.Usage.NativeEstimate != "0" {
				t.Fatal("native usage lost original source, estimate or account attribution")
			}
			sourceCounts[observation.Usage.Source]++
			counts := observation.Usage.Counts
			if observation.Usage.NativeParentID == progress.AssistantID && !authFailure {
				if counts.Input != "20" || counts.Output != "4" || counts.Total == nil || *counts.Total != "24" {
					t.Fatal("final step/message usage lost original counters")
				}
			} else if counts.Input != "0" || counts.Output != "0" || counts.Total != nil {
				t.Fatal("native defaulted zero or missing total was reinterpreted")
			}
		}
		expectedSteps, expectedMessages := 2, 2
		if authFailure {
			expectedSteps, expectedMessages = 0, 1
		}
		if sourceCounts[domain.OpenCodeStepUsage] != expectedSteps || sourceCounts[domain.OpenCodeMessageUsage] != expectedMessages {
			t.Fatal("overlapping native usage sources were merged")
		}
	}
	expectedMessages := 2
	if readTool || shellTool || todoTool {
		expectedMessages = 3
	}
	history, err := api.InspectHistory(ctx)
	if err != nil || history.InputID != receipt.MessageID || history.AssistantID != progress.AssistantID || len(history.Messages) != expectedMessages {
		t.Fatalf("registered native conversation lost its original stored comparison: %v", err)
	}
	raw, err := os.ReadFile(claimsPath)
	if err != nil || bytes.Contains(raw, []byte(f.token)) || bytes.Contains(raw, []byte("temporary-upstream-fixture-key")) || bytes.Contains(raw, []byte(f.input.Input.Prompt)) || strings.Contains(result, f.token) {
		t.Fatal("registered native metadata claims or result disclosed protected content")
	}
	if authFailure {
		for _, kind := range []domain.Kind{domain.SessionKind, domain.MessageKind, domain.UsageKind, domain.InboxKind} {
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: kind, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(rows)
			if err != nil || bytes.Contains(encoded, []byte(f.token)) || bytes.Contains(encoded, []byte("temporary-upstream-fixture-key")) {
				t.Fatal("native authentication failure persisted reflected protected content")
			}
		}
	}
	if publication == nativeCompletionPublication || publication == nativeCompletionAuthFailurePublication {
		completion, err := eventPublisher.Complete(ctx)
		if err != nil || completion.ValidateForHarness(domain.OpenCode) != nil || completion.Version != 1 || completion.NativeCheckpointDigest != "" || completion.Outcome != terminalOutcome || completion.ExecutionID != f.input.ExecutionID || completion.InputID != f.input.InputID || string(completion.NativeThreadID) != session || string(completion.NativeTurnID) != receipt.MessageID {
			t.Fatalf("original completion did not join acknowledged terminal and owned cleanup: %v", err)
		}
		again, err := eventPublisher.Complete(ctx)
		if err != nil || again != completion || calls.Load() != expectedCalls {
			t.Fatal("original completion was replaced or repeated native input")
		}
		request, response := f.reportCompletion(t, completion)
		if response.Msg.Replayed {
			t.Fatal("first completion report was fabricated as replay")
		}
		repeated, err := f.client.ReportWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, request))
		if err != nil || !repeated.Msg.Replayed {
			t.Fatal("original completion receipt was not replayed")
		}
		record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		retained, decodeErr := store.Decode[domain.Session](record)
		if err != nil || decodeErr != nil || retained.Execution == nil || !retained.Execution.CleanupVerified || retained.Execution.Outcome != terminalOutcome || retained.ActiveExecutionID != "" || retained.Dispatch != domain.DispatchPaused || retained.NextExecutionIntent != "" {
			t.Fatal("completion lost original terminal facts or fabricated continuation readiness")
		}
		record, err = f.service.Store.Get(ctx, domain.JobKind, f.job)
		job, decodeErr := store.Decode[domain.Job](record)
		expectedState := domain.JobSucceeded
		if authFailure {
			expectedState = domain.JobFailed
		}
		var reported domain.ExecutionCompletion
		if err != nil || decodeErr != nil || job.State != expectedState || domain.Decode(job.Output, &reported) != nil || reported != completion || authFailure && (job.Problem == nil || job.Problem.Code != domain.Unauthenticated) {
			t.Fatal("original report lost completion ownership or native failure")
		}
	}

}
