// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type managedSidechatProcess struct {
	Thread map[string]any
	Turn   map[string]any
	Fault  string
}

// The original test executable acts as a bounded native protocol process. Its
// marker is confined to this fixture's private Worker scope, never a real account.
func runManagedSidechatProcess(home string) bool {
	root := filepath.Dir(filepath.Dir(filepath.Dir(home)))
	raw, err := os.ReadFile(filepath.Join(root, "managed-sidechat-fixture.json"))
	if err != nil {
		return false
	}
	var f managedSidechatProcess
	if json.Unmarshal(raw, &f) != nil {
		os.Exit(91)
	}
	child := false
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	write := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "result": result})
	}
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage
			Method string
			Params map[string]any
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(92)
		}
		switch req.Method {
		case "initialize":
			platform, family := runtime.GOOS, "unix"
			if platform == "darwin" {
				platform = "macos"
			}
			if platform == "windows" {
				family = "windows"
			}
			write(req.ID, map[string]any{"codexHome": home, "platformFamily": family, "platformOs": platform, "userAgent": "delidev/" + codex.SupportedVersion + " (fixture)"})
		case "initialized":
		case "thread/loaded/list":
			write(req.ID, map[string]any{"data": []string{}, "nextCursor": nil})
		case "config/read":
			features := map[string]any{}
			for i, arg := range os.Args {
				if arg == "-c" && i+1 < len(os.Args) {
					v := os.Args[i+1]
					if strings.HasPrefix(v, "features.") {
						kv := strings.SplitN(strings.TrimPrefix(v, "features."), "=", 2)
						features[kv[0]] = kv[1] == "true"
					}
				}
			}
			write(req.ID, map[string]any{"config": map[string]any{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}, "features": features, "sandbox_mode": "read-only", "approval_policy": "never", "approvals_reviewer": "user", "allow_login_shell": false, "web_search": "disabled", "notify": []any{}, "mcp_servers": map[string]any{}, "plugins": map[string]any{}, "hooks": map[string]any{}}, "origins": nil, "layers": nil})
		case "experimentalFeature/list":
			features := []any{}
			for i, arg := range os.Args {
				if arg == "-c" && i+1 < len(os.Args) && strings.HasPrefix(os.Args[i+1], "features.") {
					kv := strings.SplitN(strings.TrimPrefix(os.Args[i+1], "features."), "=", 2)
					features = append(features, map[string]any{"name": kv[0], "stage": "stable", "displayName": nil, "description": nil, "announcement": nil, "enabled": kv[1] == "true", "defaultEnabled": false})
				}
			}
			write(req.ID, map[string]any{"data": features, "nextCursor": nil})
		case "thread/read":
			write(req.ID, map[string]any{"thread": f.Thread})
		case "thread/goal/get":
			write(req.ID, map[string]any{"goal": nil})
		case "thread/queue/list":
			write(req.ID, map[string]any{"data": []any{}, "nextCursor": nil})
		case "thread/turns/list":
			if child && f.Fault == "changed-tool-prefix" {
				items := f.Turn["items"].([]any)
				items[1].(map[string]any)["command"] = "false"
			}
			write(req.ID, map[string]any{"data": []any{f.Turn}, "nextCursor": nil, "backwardsCursor": nil})
		case "thread/fork":
			child = true
			marker := filepath.Join(home, "fork-sent")
			if os.WriteFile(marker, nil, 0600) != nil {
				os.Exit(93)
			}
			if f.Fault == "native-response-loss" {
				os.Exit(0)
			}
			parent := f.Thread["id"]
			id := string(domain.NewID())
			f.Thread["id"], f.Thread["sessionId"], f.Thread["forkedFromId"], f.Thread["cwd"] = id, id, parent, req.Params["cwd"]
			if security.WriteAtomic(filepath.Join(home, "auth.json"), workerSubscriptionBundle("rotated")) != nil {
				os.Exit(94)
			}
			if f.Fault == "cleanup" {
				_ = os.WriteFile(filepath.Join(home, "history.jsonl"), []byte("synthetic-worker-refresh-rotated"), 0600)
			}
			write(req.ID, map[string]any{"thread": f.Thread, "model": req.Params["model"], "modelProvider": "openai", "cwd": req.Params["cwd"], "approvalPolicy": req.Params["approvalPolicy"], "approvalsReviewer": req.Params["approvalsReviewer"], "sandbox": map[string]any{"type": "readOnly"}, "reasoningEffort": nil, "serviceTier": nil, "instructionSources": []string{}, "runtimeWorkspaceRoots": []string{}, "activePermissionProfile": nil, "multiAgentMode": "explicitRequestOnly"})
		case "account/read":
			write(req.ID, map[string]any{"account": map[string]any{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}, "requiresOpenaiAuth": true})
		default:
			os.Exit(95)
		}
	}
	if child {
		_ = os.WriteFile(filepath.Join(home, "original-process-exited"), nil, 0600)
	}
	return true
}

type managedSidechatRPC struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	generation      domain.ID
	home            string
	takes, finishes int
	finish          *pb.FinishSubscriptionRequest
	loss            bool
	t               *testing.T
}

func (f *managedSidechatRPC) TakeSubscription(_ context.Context, r *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.takes++
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: r.Msg.Mutation.RequestId, LeaseRevision: 3, GenerationId: string(f.generation), Bundle: workerSubscriptionBundle("first")}), nil
}
func (f *managedSidechatRPC) FinishSubscription(_ context.Context, r *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.finishes++
	f.finish = r.Msg
	if r.Msg.CleanupConfirmed {
		if _, err := os.Stat(filepath.Join(f.home, "auth.json")); !os.IsNotExist(err) {
			f.t.Error("Finish preceded original authentication removal")
		}
		if _, err := os.Stat(filepath.Join(f.home, "original-process-exited")); err != nil {
			f.t.Error("Finish preceded joined original process exit")
		}
	}
	if f.loss {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

func TestManagedSidechatWorkerOriginalForkAuthentication(t *testing.T) {
	for _, fault := range []string{"success", "finish-response-loss", "native-response-loss", "cleanup", "retry-success", "retry-native-response-loss", "retry-finish-response-loss"} {
		t.Run(fault, func(t *testing.T) {
			retry := strings.HasPrefix(fault, "retry-")
			fault = strings.TrimPrefix(fault, "retry-")
			f := newCheckpointFixture(t)
			ctx := context.Background()
			f.input.Configuration.Subscription = true
			f.input.Configuration.SubscriptionService = domain.SubscriptionChatGPT
			f.input.Configuration.ProviderID = ""
			f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
			binary, _ := os.Executable()
			f.input.Installation.ResolvedPath, _ = filepath.EvalSymlinks(binary)
			manager := &workspace.Manager{Root: f.root}
			prep := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := manager.Prepare(ctx, prep)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := manager.ClaimFirstExecution(ctx, f.jobID, f.input.ExecutionID, prep, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, f.input.Manifest = mustForkJSON(prep), mustForkJSON(manifest)
			f.job.Input = mustForkJSON(f.input)
			f.bound.Effective.Provider = "openai"
			f.bound.Effective.Cwd = manifest.PrimaryPath
			f.ref.Subscription = true
			f.ref.ConfigurationDigest = f.input.ConfigurationDigest
			f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
			f.ref.WorkspaceRoots = nativeWorkspaceRoots(manifest)
			if err = f.retain(); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
			sessions := filepath.Join(home, "sessions")
			if err = security.PrivateDir(sessions); err != nil {
				t.Fatal(err)
			}
			rollout := filepath.Join(sessions, "original.jsonl")
			if err = os.WriteFile(rollout, []byte("synthetic native retained rollout"), 0600); err != nil {
				t.Fatal(err)
			}
			thread := map[string]any{"id": string(checkpoint.Native.ThreadID), "sessionId": string(checkpoint.Native.SessionID), "cliVersion": codex.SupportedVersion, "cwd": manifest.PrimaryPath, "modelProvider": "openai", "createdAt": 1, "updatedAt": 1, "ephemeral": false, "preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}, "historyMode": "legacy", "extra": nil, "canAcceptDirectInput": true, "path": rollout}
			turn := map[string]any{"id": string(checkpoint.Native.TurnID), "items": []any{map[string]any{"type": "userMessage", "id": "fixture-user", "clientId": string(checkpoint.Native.Inputs[0].ID), "content": []any{map[string]any{"type": "text", "text": f.input.Input.Prompt, "text_elements": []any{}}}}}, "itemsView": "full", "status": "completed", "startedAt": nil, "completedAt": nil, "durationMs": nil, "error": nil}
			if err = os.WriteFile(filepath.Join(f.root, "managed-sidechat-fixture.json"), mustForkJSON(managedSidechatProcess{Thread: thread, Turn: turn, Fault: fault}), 0600); err != nil {
				t.Fatal(err)
			}
			input := domain.ForkJobInput{Version: 3, Purpose: domain.SidechatFork, SourceSessionID: f.input.SessionID, SourceRevision: 1, ChildSessionID: domain.NewID(), RuntimeID: domain.NewID(), NativeRequestID: domain.NewID(), Name: "Sidechat fixture", Workspace: domain.GeneralChat, SourceJobID: f.jobID, SourceAssignment: f.input, Completion: f.ref.Completion, SubscriptionGeneration: domain.NewID(), Snapshot: domain.InitialExecution{Configuration: f.input.Configuration, ConfigurationDigest: f.input.ConfigurationDigest, InitialAccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID}, Progress: domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, LastSequence: f.completion.LastSequence, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), Outcome: domain.ExecutionSucceeded, CleanupVerified: true}}
			if err = input.Validate(); err != nil {
				t.Fatal(err)
			}
			owner, instance := domain.NewID(), domain.NewID()
			job := domain.Job{Type: domain.ForkSessionJob, State: domain.JobClaimed, MachineID: f.input.MachineID, InstanceID: instance, Input: mustForkJSON(input)}
			auth := &managedSidechatRPC{generation: input.SubscriptionGeneration, home: filepath.Join(f.root, "runtimes", string(input.RuntimeID), "codex"), loss: fault == "finish-response-loss", t: t}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(auth)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, _ := security.RandomToken()
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			config := Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), execution: &PublicationConfig{Credential: credential, Instance: instance, Assignment: &pb.Resource{Id: string(owner), Revision: 7}}}
			var retainedPrep, retainedManifest json.RawMessage
			if retry {
				cp, cm, e := manager.PrepareSidechatReference(ctx, domain.NewID(), input.ChildSessionID, prep, manifest)
				if e != nil {
					t.Fatal(e)
				}
				retainedPrep, retainedManifest = mustForkJSON(cp), mustForkJSON(cm)
				input.Retry = &domain.SidechatRetryFork{WorkerInstanceID: instance, WorkerDeviceID: credential.DeviceID, GenerationID: domain.NewID(), ChildRevision: 3, QuestionID: domain.NewID(), QuestionRevision: 1, PreviousJobID: domain.NewID(), PreviousExecutionID: domain.NewID(), ChildPreparation: retainedPrep, ChildManifest: retainedManifest}
				job.Input = mustForkJSON(input)
			}
			output, err := forkSession(ctx, config, owner, job)
			if auth.takes != 1 || auth.finishes != 1 {
				t.Fatalf("protected original claim counts take=%d finish=%d error=%v", auth.takes, auth.finishes, err)
			}
			if fault == "success" {
				var result domain.ForkJobResult
				if err != nil || domain.Decode(output, &result) != nil || result.ValidateIdentity(input) != nil || !result.CleanupVerified || result.ManagedFinish != domain.ID(auth.finish.Mutation.RequestId) {
					t.Fatal("lost protected Finish before paused-child result", err)
				}
				if retry && (!bytes.Equal(result.Preparation, retainedPrep) || !bytes.Equal(result.Manifest, retainedManifest)) {
					t.Fatal("retry replaced original child metadata")
				}
				if !auth.finish.CleanupConfirmed || subscription.Refreshed(workerSubscriptionBundle("first"), auth.finish.Bundle) != nil {
					t.Fatal("lost rotated bundle/cleanup")
				}
			}
			if fault != "success" {
				var uncertainty *managedExecutionUncertain
				if err == nil || len(output) != 0 || !errors.As(err, &uncertainty) {
					t.Fatal("uncertain child published", err)
				}
			}
			takes, finishes := auth.takes, auth.finishes
			again, retryErr := forkSession(ctx, config, owner, job)
			if retryErr == nil || len(again) != 0 || auth.takes != takes || auth.finishes != finishes {
				t.Fatal("retained original child permitted replacement claim/publication", retryErr)
			}
			if fault == "success" || fault == "finish-response-loss" {
				if _, e := os.Stat(filepath.Join(auth.home, "auth.json")); !os.IsNotExist(e) {
					t.Fatal("original protected authentication retained", e)
				}
				assertManagedWorkerFilesRedacted(t, f.root, workerSubscriptionBundle("first"), workerSubscriptionBundle("rotated"))
			}
			if auth.finish != nil {
				clear(auth.finish.Bundle)
			}
		})
	}
}

func TestManagedIndependentForkWorkerOriginalAuthenticationAndTools(t *testing.T) {
	for _, fault := range []string{"success", "finish-response-loss", "native-response-loss", "cleanup", "changed-tool-prefix"} {
		t.Run(fault, func(t *testing.T) {
			f := newCheckpointFixture(t)
			ctx := context.Background()
			f.input.Configuration.Subscription = true
			f.input.Configuration.SubscriptionService = domain.SubscriptionChatGPT
			f.input.Configuration.ProviderID = ""
			f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
			binary, _ := os.Executable()
			f.input.Installation.ResolvedPath, _ = filepath.EvalSymlinks(binary)
			manager := &workspace.Manager{Root: f.root}
			prep := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := manager.Prepare(ctx, prep)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := manager.ClaimFirstExecution(ctx, f.jobID, f.input.ExecutionID, prep, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, f.input.Manifest = mustForkJSON(prep), mustForkJSON(manifest)
			f.job.Input = mustForkJSON(f.input)
			f.bound.Effective.Provider = "openai"
			f.bound.Effective.Cwd = manifest.PrimaryPath
			f.ref.Subscription = true
			f.ref.ConfigurationDigest = f.input.ConfigurationDigest
			f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
			f.ref.WorkspaceRoots = nativeWorkspaceRoots(manifest)
			if err = f.retain(); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
			sessions := filepath.Join(home, "sessions")
			if err = security.PrivateDir(sessions); err != nil {
				t.Fatal(err)
			}
			rollout := filepath.Join(sessions, "original.jsonl")
			if err = os.WriteFile(rollout, []byte("synthetic native retained rollout"), 0600); err != nil {
				t.Fatal(err)
			}
			thread := map[string]any{"id": string(checkpoint.Native.ThreadID), "sessionId": string(checkpoint.Native.SessionID), "cliVersion": codex.SupportedVersion, "cwd": manifest.PrimaryPath, "modelProvider": "openai", "createdAt": 1, "updatedAt": 1, "ephemeral": false, "preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}, "historyMode": "legacy", "extra": nil, "canAcceptDirectInput": true, "path": rollout}
			turn := map[string]any{"id": string(checkpoint.Native.TurnID), "items": []any{map[string]any{"type": "userMessage", "id": "fixture-user", "clientId": string(checkpoint.Native.Inputs[0].ID), "content": []any{map[string]any{"type": "text", "text": f.input.Input.Prompt, "text_elements": []any{}}}}}, "itemsView": "full", "status": "completed", "startedAt": nil, "completedAt": nil, "durationMs": nil, "error": nil}
			turn["items"] = append(turn["items"].([]any), map[string]any{"type": "commandExecution", "id": "settled-command", "status": "completed", "command": "true", "cwd": manifest.PrimaryPath, "source": "agent", "commandActions": []any{}, "exitCode": 0}, map[string]any{"type": "fileChange", "id": "settled-patch", "status": "completed", "changes": []any{}})
			if err = os.WriteFile(filepath.Join(f.root, "managed-sidechat-fixture.json"), mustForkJSON(managedSidechatProcess{Thread: thread, Turn: turn, Fault: fault}), 0600); err != nil {
				t.Fatal(err)
			}
			input := domain.ForkJobInput{Version: 1, Purpose: domain.IndependentFork, SourceSessionID: f.input.SessionID, SourceRevision: 1, ChildSessionID: domain.NewID(), RuntimeID: domain.NewID(), NativeRequestID: domain.NewID(), Name: "Sidechat fixture", Workspace: domain.GeneralChat, SourceJobID: f.jobID, SourceAssignment: f.input, Completion: f.ref.Completion, SubscriptionGeneration: domain.NewID(), Snapshot: domain.InitialExecution{Configuration: f.input.Configuration, ConfigurationDigest: f.input.ConfigurationDigest, InitialAccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID}, Progress: domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, LastSequence: f.completion.LastSequence, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), Outcome: domain.ExecutionSucceeded, CleanupVerified: true}}
			if err = input.Validate(); err != nil {
				t.Fatal(err)
			}
			owner, instance := domain.NewID(), domain.NewID()
			job := domain.Job{Type: domain.ForkSessionJob, State: domain.JobClaimed, MachineID: f.input.MachineID, InstanceID: instance, Input: mustForkJSON(input)}
			auth := &managedSidechatRPC{generation: input.SubscriptionGeneration, home: filepath.Join(f.root, "runtimes", string(input.RuntimeID), "codex"), loss: fault == "finish-response-loss", t: t}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(auth)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, _ := security.RandomToken()
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			config := Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), execution: &PublicationConfig{Credential: credential, Instance: instance, Assignment: &pb.Resource{Id: string(owner), Revision: 7}}}
			output, err := forkSession(ctx, config, owner, job)
			if auth.takes != 1 || auth.finishes != 1 {
				t.Fatalf("protected original claim counts take=%d finish=%d error=%v", auth.takes, auth.finishes, err)
			}
			if fault == "success" {
				var result domain.ForkJobResult
				if err != nil || domain.Decode(output, &result) != nil || result.ValidateIdentity(input) != nil || !result.CleanupVerified || result.ManagedFinish != domain.ID(auth.finish.Mutation.RequestId) {
					t.Fatal("lost protected Finish before paused-child result", err)
				}
				if !auth.finish.CleanupConfirmed || subscription.Refreshed(workerSubscriptionBundle("first"), auth.finish.Bundle) != nil {
					t.Fatal("lost rotated bundle/cleanup")
				}
			}
			if fault == "changed-tool-prefix" {
				if err == nil || len(output) != 0 || !auth.finish.CleanupConfirmed {
					t.Fatal("changed settled native tool prefix published or released without original cleanup", err)
				}
			}
			if fault != "success" && fault != "changed-tool-prefix" {
				var uncertainty *managedExecutionUncertain
				if err == nil || len(output) != 0 || !errors.As(err, &uncertainty) {
					t.Fatal("uncertain child published", err)
				}
			}
			takes, finishes := auth.takes, auth.finishes
			again, retryErr := forkSession(ctx, config, owner, job)
			if retryErr == nil || len(again) != 0 || auth.takes != takes || auth.finishes != finishes {
				t.Fatal("retained original child permitted replacement claim/publication", retryErr)
			}
			if fault == "success" || fault == "finish-response-loss" {
				if _, e := os.Stat(filepath.Join(auth.home, "auth.json")); !os.IsNotExist(e) {
					t.Fatal("original protected authentication retained", e)
				}
				assertManagedWorkerFilesRedacted(t, f.root, workerSubscriptionBundle("first"), workerSubscriptionBundle("rotated"))
			}
			if auth.finish != nil {
				clear(auth.finish.Bundle)
			}
		})
	}
}
