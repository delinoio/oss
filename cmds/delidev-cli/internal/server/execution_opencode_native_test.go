package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredOpenCode(t, mode, false, false) })
	}
}

func TestManualNativeOpenCodeRegisteredRelayRevocation(t *testing.T) {
	nativeRegisteredOpenCode(t, domain.ExecuteMode, true, false)
}

func TestManualNativeOpenCodePublishesRegisteredBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredOpenCode(t, mode, false, true) })
	}
}

func nativeRegisteredOpenCode(t *testing.T, mode domain.SessionMode, revoke, publish bool) {
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
		if calls.Add(1) != 1 {
			t.Error("native registered input repeated inference")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Model    string
			Stream   bool
			Messages []struct {
				Role    string
				Content json.RawMessage
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
		close(started)
		defer close(ended)
		if revoke {
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
				t.Error("account revocation did not cancel the actual provider request")
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
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
		defer publisher.Close()
		bindings, err = worker.OpenOpenCodeBindingPublisher(publisher)
		if err != nil {
			t.Fatal(err)
		}
		defer bindings.Close()
		claim = bindings.Claim
		claimsPath = filepath.Join(cfg.Root, "jobs", string(f.job), "opencode-claims.json")
	}
	config := opencode.APIExecutionConfig{
		Probe:     opencode.ProbeConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: f.service.logger}, Version: opencode.SupportedVersion, Home: filepath.Join(runtimeRoot, "opencode")},
		Workspace: workspace, NativeRoot: filepath.VolumeName(workspace) + string(filepath.Separator), ServerOrigin: f.http.URL, Token: f.token,
		Settings: settings, Rejection: opencode.StopOnInteractionRejection, Claim: claim,
	}
	api, err := opencode.OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
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
		t.Fatal(err)
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
	var result string
	var final *opencode.NativeMessage
	for count := 0; count < 256; count++ {
		observation, err := api.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Part != nil && observation.Part.Text != nil {
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
	if err != nil || !progress.SettledObserved || progress.NeedsRecovery || final == nil || final.Assistant == nil || final.Assistant.Error != nil || result != "Registered OpenCode fixture completed." || calls.Load() != 1 || len(claims) != 2 {
		t.Fatal("registered native original input did not settle with its exact result")
	}
	receipt, err := api.InspectInput(ctx)
	if err != nil || !receipt.Recorded || receipt.SessionID != session || receipt.RequestID != f.input.TurnRequestID || receipt.MessageID != claims[1].MessageID || receipt.PartID != claims[1].PartID {
		t.Fatal("registered native input lost its separately verified original storage")
	}
	if publish {
		if bindings.AcceptInput(ctx, receipt) == nil {
			t.Fatal("expected lost original acceptance acknowledgement")
		}
		if err := bindings.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if len(publicationClient.calls) != 4 || publicationClient.calls[0] != publicationClient.calls[1] || publicationClient.calls[2] != publicationClient.calls[3] {
			t.Fatal("original native publication replaced an uncertain RPC identity")
		}
		r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := store.Decode[domain.Session](r)
		if err != nil || retained.Execution == nil || retained.Execution.LastSequence != 2 || retained.Execution.NativeThreadID != session || retained.Execution.NativeTurnID != receipt.MessageID || retained.PendingInputs != 0 || retained.PendingInputBytes != 0 || retained.Execution.Observed.OpenCodeAgent != agent || retained.Execution.Outcome != domain.ExecutionRunning {
			t.Fatal("server binding did not preserve exact native ownership and independent unfinished publication")
		}
	}
	history, err := api.InspectHistory(ctx)
	if err != nil || history.InputID != receipt.MessageID || history.AssistantID != progress.AssistantID || len(history.Messages) != 2 {
		t.Fatalf("registered native conversation lost its original stored comparison: %v", err)
	}
	raw, err := os.ReadFile(claimsPath)
	if err != nil || bytes.Contains(raw, []byte(f.token)) || bytes.Contains(raw, []byte("temporary-upstream-fixture-key")) || bytes.Contains(raw, []byte(f.input.Input.Prompt)) || strings.Contains(result, f.token) {
		t.Fatal("registered native metadata claims or result disclosed protected content")
	}
}
