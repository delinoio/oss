// SPDX-License-Identifier: Apache-2.0
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
	"path/filepath"
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

func TestManualNativeOpenCodeForegroundChildPublishesOriginalServerOwnership(t *testing.T) {
	nativeForegroundChildPublication(t, false)
}

func TestManualNativeOpenCodeForegroundChildStopJoinsOwnedCleanup(t *testing.T) {
	nativeForegroundChildPublication(t, true)
}

func nativeForegroundChildPublication(t *testing.T, stopChild bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native child with temporary SQLite and scripted provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var calls atomic.Int32
	var canceled atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string
			Stream bool
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body) != nil || body.Model != "fixture-model" || !body.Stream || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" {
			t.Error("native child escaped the original account relay")
			w.WriteHeader(400)
			return
		}
		call := calls.Add(1)
		if call > 3 {
			t.Error("native child repeated inference")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			args, _ := json.Marshal(map[string]any{"description": "Inspect private fixture", "prompt": "Reply with private child fixture.", "subagent_type": "general"})
			delta := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "original-foreground-task", "type": "function", "function": map[string]any{"name": "task", "arguments": string(args)}}}}
			for _, choice := range []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}, {"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}} {
				raw, _ := json.Marshal(map[string]any{"id": "chatcmpl-task", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{choice}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-final","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original foreground child response"},"finish_reason":null}]}`+"\n\n")
		if stopChild && call == 2 {
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				canceled.Store(true)
			case <-ctx.Done():
				t.Error("original child provider survived Stop")
			}
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-final","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":6,"total_tokens":36}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, provider.URL, domain.OpenCode, domain.OpenAIChat, func(input *domain.ExecutionJobInput) { input.Input.Mode = domain.ExecuteMode }, false))
	f.registerGrant(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot, workspace := filepath.Join(root, "runtime"), filepath.Join(root, "workspace")
	if err := security.PrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	pc := publicationWorkerConfig(t, f)
	var diagnostics bytes.Buffer
	pc.Logger = slog.New(slog.NewJSONHandler(&diagnostics, nil))
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(diagnostics.String())
		}
	})
	pc.Root = filepath.Join(root, "worker")
	publisher, err := worker.OpenExecutionPublisher(pc)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	binding, err := worker.OpenOpenCodeBindingPublisher(publisher)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	nativeRoot, err := opencode.GlobalWorkspaceRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	config := opencode.APIExecutionConfig{Probe: opencode.ProbeConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: pc.Logger}, Version: opencode.SupportedVersion, Home: filepath.Join(runtimeRoot, "opencode")}, Workspace: workspace, Root: nativeRoot, ServerOrigin: f.http.URL, Token: f.token, Settings: opencode.SessionSettings{Title: "Original child fixture", Agent: opencode.BuildAgent, Provider: "delidev", Model: f.input.Configuration.NativeModel, Permission: []opencode.PermissionRule{}}, Rejection: opencode.StopOnInteractionRejection, Claim: binding.Claim}
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
	}()
	settings, err := api.InitialSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := api.CreateSession(ctx, f.input.ThreadRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.BindSession(ctx, f.input.ThreadRequestID, session, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := api.StartText(ctx, f.input.TurnRequestID, f.input.Input.Prompt); err != nil {
		t.Fatal(err)
	}
	var prefix []opencode.Observation
	for len(prefix) < 256 {
		o, err := api.Next(ctx)
		if err != nil {
			t.Fatalf("original root progress: %v", err)
		}
		prefix = append(prefix, o)
		p, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.UserSeen && p.InputPartSeen {
			break
		}
	}
	receipt, err := api.InspectInput(ctx)
	if err != nil || !receipt.Recorded {
		t.Fatal("original input lost native acceptance", err)
	}
	if err := binding.AcceptInput(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	events, err := worker.OpenOpenCodeEventPublisher(binding, api)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range prefix {
		if err := events.PublishObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	stopped := false
	for count := 0; count < 1024; count++ {
		p, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.SettledObserved {
			break
		}
		o, err := api.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := events.PublishObservation(ctx, o); err != nil {
			kind := opencode.PartKind("")
			if o.Part != nil {
				kind = o.Part.Kind
			}
			t.Fatalf("original publication kind=%s part=%s child_facts=%d: %v", o.Kind, kind, len(o.Children), err)
		}
		if stopChild && !stopped && len(o.Children) > 0 && calls.Load() == 2 {
			record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			if _, err := client.ControlSession(ctx, ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(record.ID), ExpectedRevision: record.Revision}, Action: pb.SessionAction_SESSION_ACTION_STOP})); err != nil {
				t.Fatal(err)
			}
			if receipt, err := events.RequestStop(ctx, domain.NewID()); err != nil || !receipt.NativeAttempted || receipt.CleanupVerified {
				t.Fatal("original root Stop was missing or cleanup fabricated", err)
			}
			stopped = true
		}
	}
	if _, err := events.PublishTerminal(ctx); err != nil {
		t.Fatal(err)
	}
	completion, err := events.Complete(ctx)
	if err != nil || completion.Version != 1 || !completion.CleanupVerified {
		t.Fatal("child completion lost cleanup or acquired a checkpoint", err)
	}
	retained, err := events.RetainCompletion(ctx)
	if err != nil || retained != completion || retained.NativeCheckpointDigest != "" {
		t.Fatal("child history acquired continuation", err)
	}
	if stopChild && (!stopped || !canceled.Load()) {
		t.Fatal("root Stop did not join the original child provider")
	}
	expectedCalls := int32(3)
	if stopChild {
		expectedCalls = 2
	}
	if calls.Load() != expectedCalls {
		t.Fatal("native child or parent inference duplicated")
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		rows, err := tx.List(store.Filter{Kind: domain.SubagentKind, SessionID: f.input.SessionID, Limit: 100})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatal("independently proved child was missing or duplicated")
		}
		child, err := store.Decode[domain.SubagentRecord](rows[0])
		if err != nil {
			return err
		}
		if child.Harness != domain.OpenCode || child.Observation.OpenCodeTool == nil || child.Observation.ParentID != child.RootID || !child.Observation.Status.Terminal() || !stopChild && (child.Observation.Output == nil || child.Observation.Usage == nil) || child.Sources[0].Source != domain.OpenCodeTaskSource {
			t.Fatal("original child sources, telemetry or parent tool changed")
		}
		if stopChild && child.Observation.Source == domain.OpenCodeChildCleanupSource && (child.Observation.Status != domain.SubagentInterrupted || child.Observation.OpenCodeCleanup == nil || !child.Observation.OpenCodeCleanup.CleanupVerified) {
			t.Fatal("unfinished native child history acquired fabricated settlement")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
