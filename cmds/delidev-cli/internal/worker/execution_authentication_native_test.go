// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type managedExecutionFixtureFault string

const (
	managedFixtureCapture        managedExecutionFixtureFault = "capture"
	managedFixtureScan           managedExecutionFixtureFault = "scan"
	managedFixtureStartupCleanup managedExecutionFixtureFault = "startup-cleanup"
)

func init() {
	if len(os.Args) < 2 || os.Args[1] != "-c" || os.Args[len(os.Args)-1] != "app-server" || !slices.Contains(os.Args, `forced_login_method="chatgpt"`) {
		return
	}
	// This test binary is the controlled native process. It supports only this
	// private managed execution profile and never reaches a provider account.
	// Tagged lifecycle drivers retain their separate fixture by requiring direct
	// native arguments here, rather than intercepting their wrapper arguments.
	home := os.Getenv("CODEX_HOME")
	faultBytes, _ := os.ReadFile(filepath.Join(home, "fixture-fault"))
	fault := managedExecutionFixtureFault(faultBytes)
	var thread map[string]any
	reads := 0
	encoder := json.NewEncoder(os.Stdout)
	write := func(id json.RawMessage, result any) {
		_ = encoder.Encode(map[string]any{"id": id, "result": result})
	}
	notify := func(method string, params any) {
		_ = encoder.Encode(map[string]any{"method": method, "params": params})
	}
	turn := func(id domain.ID, status codex.TurnStatus) map[string]any {
		return map[string]any{"id": id, "items": []any{}, "itemsView": "notLoaded", "status": status, "startedAt": nil, "completedAt": nil, "durationMs": nil, "error": nil}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(81)
		}
		switch request.Method {
		case "initialize":
			marker, err := os.OpenFile(filepath.Join(home, "startup-initialized"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil || marker.Close() != nil {
				os.Exit(86)
			}
			platform, family := runtime.GOOS, "unix"
			if platform == "darwin" {
				platform = "macos"
			} else if platform == "windows" {
				family = "windows"
			}
			agent := "delidev/" + codex.SupportedVersion + " (fixture)"
			if fault == managedFixtureStartupCleanup {
				agent = "unsupported native fixture"
				if os.WriteFile(filepath.Join(home, "history.jsonl"), []byte("synthetic-worker-refresh-first"), 0600) != nil {
					os.Exit(85)
				}
			}
			write(request.ID, map[string]any{"codexHome": home, "platformFamily": family, "platformOs": platform, "userAgent": agent})
		case "initialized":
		case "thread/loaded/list":
			write(request.ID, map[string]any{"data": []string{}, "nextCursor": nil})
		case "thread/list":
			// Root completion performs the same state-DB-only descendant inventory
			// as production. This managed-auth fixture has no children, but it must
			// answer that read before terminal authentication capture and Close.
			var params struct {
				Ancestor    string   `json:"ancestorThreadId"`
				Limit       int      `json:"limit"`
				Cursor      *string  `json:"cursor"`
				SourceKinds []string `json:"sourceKinds"`
				StateDBOnly bool     `json:"useStateDbOnly"`
			}
			raw, err := json.Marshal(request.Params)
			if err != nil || json.Unmarshal(raw, &params) != nil || params.Ancestor != string(thread["id"].(domain.ID)) || params.Limit != 128 || params.Cursor != nil || !slices.Equal(params.SourceKinds, []string{"subAgent", "subAgentThreadSpawn", "subAgentOther"}) || !params.StateDBOnly {
				os.Exit(87)
			}
			write(request.ID, map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil})
		case "config/read":
			write(request.ID, map[string]any{"config": map[string]any{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}}, "origins": nil, "layers": nil})
		case "thread/start":
			id := domain.NewID()
			thread = map[string]any{"id": id, "sessionId": id, "cliVersion": codex.SupportedVersion, "cwd": request.Params["cwd"], "modelProvider": "openai", "createdAt": int64(1), "updatedAt": int64(1), "ephemeral": false, "preview": "", "projectId": nil, "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}, "historyMode": "legacy", "extra": nil, "canAcceptDirectInput": true}
			write(request.ID, map[string]any{"thread": thread, "model": request.Params["model"], "modelProvider": "openai", "cwd": request.Params["cwd"], "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandbox": map[string]any{"type": "readOnly"}, "reasoningEffort": nil, "serviceTier": nil, "instructionSources": []string{}, "runtimeWorkspaceRoots": []string{}, "activePermissionProfile": nil, "multiAgentMode": "explicitRequestOnly"})
		case "thread/read":
			write(request.ID, map[string]any{"thread": thread})
		case "turn/start":
			id := domain.NewID()
			write(request.ID, map[string]any{"turn": turn(id, codex.TurnRunning)})
			notify("turn/started", map[string]any{"threadId": thread["id"], "turn": turn(id, codex.TurnRunning)})
			content := []any{map[string]any{"type": "text", "text": request.Params["input"].([]any)[0].(map[string]any)["text"], "text_elements": []any{}}}
			item := map[string]any{"type": "userMessage", "id": "fixture-user-item", "clientId": request.Params["clientUserMessageId"], "content": content}
			notify("item/started", map[string]any{"threadId": thread["id"], "turnId": id, "startedAtMs": 1, "item": item})
			notify("item/completed", map[string]any{"threadId": thread["id"], "turnId": id, "completedAtMs": 1, "item": item})
			if security.WriteAtomic(filepath.Join(home, "auth.json"), workerSubscriptionBundle("rotated")) != nil {
				os.Exit(82)
			}
			if fault == managedFixtureScan {
				if os.WriteFile(filepath.Join(home, "history.jsonl"), []byte("synthetic-worker-refresh-rotated"), 0600) != nil {
					os.Exit(86)
				}
			}
			notify("turn/completed", map[string]any{"threadId": thread["id"], "turn": turn(id, codex.TurnCompleted)})
		case "account/read":
			reads++
			if os.WriteFile(filepath.Join(home, "bundle-read-count"), []byte(strconv.Itoa(reads)), 0600) != nil {
				os.Exit(83)
			}
			var account any = map[string]any{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}
			if fault == managedFixtureCapture {
				account = nil
			}
			write(request.ID, map[string]any{"account": account, "requiresOpenaiAuth": true})
		default:
			os.Exit(84)
		}
	}
	os.Exit(0)
}

type managedExecutionPublicationRPC struct {
	earlyExecutionRegistrationRPC
	events         []domain.ExecutionEvent
	nativeHome     string
	fault          managedExecutionFixtureFault
	startupReports []*pb.ExecutionStartupObservation
}

func (f *managedExecutionPublicationRPC) ReportExecutionStartup(_ context.Context, req *connect.Request[pb.ReportExecutionStartupRequest]) (*connect.Response[pb.ReportExecutionStartupResponse], error) {
	f.startupReports = append(f.startupReports, req.Msg.Observation)
	return connect.NewResponse(&pb.ReportExecutionStartupResponse{Observation: req.Msg.Observation}), nil
}

func (f *managedExecutionPublicationRPC) RegisterExecution(ctx context.Context, req *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	if f.fault != "" {
		if err := os.WriteFile(filepath.Join(f.nativeHome, "fixture-fault"), []byte(f.fault), 0600); err != nil {
			return nil, err
		}
	}
	return f.earlyExecutionRegistrationRPC.RegisterExecution(ctx, req)
}

func (f *managedExecutionPublicationRPC) PublishExecution(_ context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	var event domain.ExecutionEvent
	if err := domain.Decode(req.Msg.EventJson, &event); err != nil || event.Validate() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.events = append(f.events, event)
	return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: event.Sequence}), nil
}

func TestManagedExecutionCapturesRotatedBundleBeforeNativeClose(t *testing.T) {
	for _, version := range []int{1, 4} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			f := newCheckpointFixture(t)
			f.input.ExecutionID = domain.NewID()
			f.input.Configuration.Subscription = true
			f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			f.input.Installation.ResolvedPath, err = filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			if version == 4 {
				f.input.Version = 4
				f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: f.input.Installation.ResolvedPath}
				f.input.Installation = domain.Installation{}
			}
			manager := &workspace.Manager{Root: f.root}
			preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := manager.Prepare(context.Background(), preparation)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, _ = json.Marshal(preparation)
			f.input.Manifest, _ = json.Marshal(manifest)
			f.job.Input, _ = json.Marshal(f.input)
			f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
			document, _ := json.Marshal(f.job)
			resource := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 9, SessionId: string(f.input.SessionID), DocumentJson: document}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			authentication := &earlyExecutionSubscriptionRPC{bundle: bundle, finished: make(chan *pb.FinishSubscriptionRequest, 1)}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(authentication)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, err := security.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			client := &managedExecutionPublicationRPC{}
			publication := &PublicationConfig{Credential: credential, Instance: f.job.InstanceID, Assignment: resource, Client: client}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			output, err := executeSession(ctx, Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), execution: publication, executionContext: ctx}, f.jobID, f.job)
			if !slices.ContainsFunc(client.events, func(event domain.ExecutionEvent) bool { return event.Kind == domain.ExecutionTurnFinished }) {
				t.Fatal("controlled native fixture did not reach terminal publication", err)
			}
			if version == 4 {
				// This regression owns startup admission. Terminal checkpoint
				// acceptance is separate from one initialized original process.
				if len(client.startupReports) < 1 || client.startupReports[0].State != pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_READY || len(client.startupReports) > 2 {
					t.Fatal("v4 startup did not publish original ready evidence", client.startupReports)
				}
				home := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
				if _, statErr := os.Stat(filepath.Join(home, "startup-initialized")); statErr != nil {
					t.Fatal("original native process did not initialize", statErr)
				}
				if err != nil && domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("unexpected post-terminal fixture failure", err)
				}
				return
			}
			if err != nil {
				for _, event := range client.events {
					t.Log("acknowledged native event", event.Kind)
				}
				t.Fatal("managed native completion did not retain successful authentication", err)
			}
			var result domain.ExecutionCompletion
			if domain.Decode(output, &result) != nil || result.Outcome != domain.ExecutionSucceeded || !result.CleanupVerified {
				t.Fatal("managed native completion lost its original terminal result")
			}
			select {
			case finish := <-authentication.finished:
				defer clear(finish.Bundle)
				if !finish.Succeeded || !finish.CleanupConfirmed || subscription.Refreshed(bundle, finish.Bundle) != nil {
					t.Fatal("managed completion lost its latest bundle or cleanup evidence")
				}
				assertManagedWorkerFilesRedacted(t, f.root, bundle, finish.Bundle)
			default:
				t.Fatal("managed completion omitted protected write-back")
			}
			home := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
			if _, err := os.Lstat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("managed completion retained plaintext authentication")
			}
			reads, err := security.ReadPrivate(filepath.Join(home, "bundle-read-count"), 16)
			if err != nil || string(reads) != "1" {
				t.Fatal("terminal cleanup did not capture the native bundle exactly once")
			}
			if _, err := os.Stat(filepath.Join(home, "startup-initialized")); err != nil {
				t.Fatal("original native process did not initialize", err)
			}
		})
	}
}

func TestManagedExecutionAcknowledgedFencePreservesStartedJournal(t *testing.T) {
	for _, fault := range []managedExecutionFixtureFault{managedFixtureCapture, managedFixtureScan, managedFixtureStartupCleanup} {
		t.Run(string(fault), func(t *testing.T) {
			f := newCheckpointFixture(t)
			f.input.ExecutionID = domain.NewID()
			f.input.Configuration.Subscription = true
			f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			f.input.Installation.ResolvedPath, err = filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			manager := &workspace.Manager{Root: f.root}
			preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := manager.Prepare(context.Background(), preparation)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, _ = json.Marshal(preparation)
			f.input.Manifest, _ = json.Marshal(manifest)
			f.job.Input, _ = json.Marshal(f.input)
			f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
			document, _ := json.Marshal(f.job)
			resource := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 9, SessionId: string(f.input.SessionID), DocumentJson: document}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			authentication := &earlyExecutionSubscriptionRPC{bundle: bundle, finished: make(chan *pb.FinishSubscriptionRequest, 1)}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(authentication)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, err := security.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			client := &managedExecutionPublicationRPC{nativeHome: filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex"), fault: fault}

			if err := security.PrivateDir(filepath.Join(f.root, "jobs")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			config := Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
			err = runAndReportJob(ctx, config, client, credential, f.job.InstanceID, assignment{context: ctx, cancel: func() {}}, resource, f.job)
			var uncertain *managedExecutionUncertain
			if !errors.As(err, &uncertain) || client.reported {
				t.Fatal("acknowledged fenced finish authorized ordinary job reporting", err)
			}
			raw, readErr := security.ReadPrivate(filepath.Join(f.root, "jobs", string(f.jobID)+".json"), 2<<20)
			var retained journal
			if readErr != nil || domain.Decode(raw, &retained) != nil || retained.State != journalStarted || retained.Problem != nil || len(retained.Output) != 0 {
				t.Fatal("acknowledged fenced finish replaced the original started claim", readErr)
			}
			select {
			case finish := <-authentication.finished:
				defer clear(finish.Bundle)
				if finish.Succeeded && finish.CleanupConfirmed {
					t.Fatal("fixture did not exercise uncertain native authentication")
				}
				if fault == managedFixtureCapture && finish.Succeeded {
					t.Fatal("capture fixture did not exercise final bundle capture failure")
				}
				if fault == managedFixtureScan && (!finish.Succeeded || finish.CleanupConfirmed) {
					t.Fatal("scan fixture did not isolate retained-file cleanup failure")
				}
			default:
				t.Fatal("uncertain execution omitted its protected fenced Finish")
			}
		})
	}
}
