// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestManualNativePublicOpenCodeGeneralChatFork(t *testing.T) {
	for _, fault := range []string{"", "parent-delete", "missing-claim", "delete-unstarted-child", "unsupported-canonical"} {
		t.Run(fault, func(t *testing.T) { nativePublicOpenCodeFork(t, fault) })
	}
}
func nativePublicOpenCodeFork(t *testing.T, fault string) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit isolated pinned native General Chat fork")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "fixture-model", "object": "model"}}})
			return
		}
		var body struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "" || json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body) != nil || body.Model != "fixture-model" {
			t.Error("fork escaped original provider selection")
			w.WriteHeader(403)
			return
		}
		n := calls.Add(1)
		if n > 4 {
			t.Error("fork preparation replayed inference")
			w.WriteHeader(403)
			return
		}
		if n == 3 && (!bytes.Contains(body.Messages, []byte("Child explicit first input")) || !bytes.Contains(body.Messages, []byte("Original native answer 1")) || !bytes.Contains(body.Messages, []byte("Original native answer 2"))) {
			t.Error("fork lost complete original native context")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{map[string]any{"id": fmt.Sprintf("chat_fork_%d", n), "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": fmt.Sprintf("Original native answer %d", n)}, "finish_reason": nil}}}, map[string]any{"id": fmt.Sprintf("chat_fork_%d", n), "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}}} {
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	f := newFirstDispatchFixtureProfile(t, domain.OpenCode, domain.ExecuteMode, binary, provider.URL, "fixture-model")
	// Setup uses the shared fixture's accepted real workspace preparation.
	// Retain its exact original assignment/report journal for deletion acceptance;
	// native execution and fork claims below are produced only by the real Worker.
	var assigned store.Record
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		assigned, err = tx.JobAssignment(domain.ID(f.change.WorkspaceJob.Id))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	originalJob, err := store.Decode[domain.Job](assigned)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := f.service.Store.Get(ctx, domain.JobKind, assigned.ID)
	if err != nil {
		t.Fatal(err)
	}
	completedJob, err := store.Decode[domain.Job](completed)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(assigned.Data)
	journalRaw, _ := json.Marshal(map[string]any{"version": 1, "job_id": assigned.ID, "instance_id": originalJob.InstanceID, "revision": assigned.Revision, "digest": hex.EncodeToString(digest[:]), "state": "reported", "report_id": domain.NewID(), "output": completedJob.Output})
	if err := security.PrivateDir(filepath.Join(f.workerRoot, "jobs")); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "jobs", string(assigned.ID)+".json"), journalRaw); err != nil {
		t.Fatal(err)
	}
	deletionCtx, stopDeletions := context.WithCancel(ctx)
	deletionsDone := make(chan struct{})
	go func() { defer close(deletionsDone); f.service.runSessionDeletions(deletionCtx) }()
	defer func() { stopDeletions(); <-deletionsDone }()
	f.workerStream.Close()
	if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.endpoint.URL, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	stop := startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	defer func() { stop() }()
	wait := func(id domain.ID) domain.Job {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			row, err := f.service.Store.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			j, err := store.Decode[domain.Job](row)
			if err != nil {
				t.Fatal(err)
			}
			if j.State.Terminal() || j.State == domain.JobUncertain {
				return j
			}
			select {
			case <-ctx.Done():
				t.Fatal("original fork fixture did not settle")
			case <-tick.C:
			}
		}
	}
	client := sessionClient(f.accountFixture)
	row := f.refresh(t)
	first, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil {
		t.Fatal(err)
	}
	if j := wait(domain.ID(first.Msg.Change.ExecutionJob.Id)); j.State != domain.JobSucceeded {
		t.Fatal("original source", j.Problem)
	}
	secondRaw, _ := json.Marshal(domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Original second retained input"})
	second, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: string(row.ID), DocumentJson: secondRaw}))
	if err != nil || second.Msg.Change.ExecutionJob == nil {
		t.Fatal("original second input", err)
	}
	var secondJob domain.Job
	var secondAssignment domain.ExecutionJobInput
	if domain.Decode(second.Msg.Change.ExecutionJob.DocumentJson, &secondJob) != nil || domain.Decode(secondJob.Input, &secondAssignment) != nil || secondAssignment.InputID != domain.ID(second.Msg.Change.Input.Id) {
		row = f.refresh(t)
		resume, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
		if err != nil {
			t.Fatal("source second Resume", err)
		}
		second.Msg.Change.ExecutionJob = resume.Msg.Change.ExecutionJob
	}
	if j := wait(domain.ID(second.Msg.Change.ExecutionJob.Id)); j.State != domain.JobSucceeded {
		t.Fatal("original second completion", j.Problem)
	}
	row = f.refresh(t)
	source, err := store.Decode[domain.Session](row)
	if err != nil || source.Execution == nil {
		t.Fatal("source missing")
	}
	sourceDirectory := filepath.Join(f.workerRoot, "workspaces", string(row.ID), "chat")
	for _, name := range []string{".hidden-original", "executable-original"} {
		mode := os.FileMode(0600)
		if strings.HasPrefix(name, "executable") {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(sourceDirectory, name), []byte("Private independent source bytes"), mode); err != nil {
			t.Fatal(err)
		}
	}
	if fault == "unsupported-canonical" {
		if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.unsupported-source-transcript", nil, func(tx *store.Tx) (any, error) {
			messages, err := tx.List(store.Filter{Kind: domain.MessageKind, SessionID: row.ID, Limit: 200})
			if err != nil || len(messages) == 0 {
				return nil, domain.Fail(domain.Internal, "Fixture transcript is absent.", "")
			}
			value, err := store.Decode[domain.ExecutionMessage](messages[0])
			if err != nil {
				return nil, err
			}
			value.State = domain.MessageStreaming
			return tx.Put(domain.MessageKind, messages[0].ID, messages[0].Revision, row.ID, row.ProjectID, value)
		}); err != nil {
			t.Fatal(err)
		}
		blocked := &pb.ForkSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedTurnId: source.Execution.NativeTurnID, Name: "Unsupported source", Workspace: pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT}
		if _, err := client.ForkSession(ctx, ownerRequest(f.identity, blocked)); err == nil || calls.Load() != 2 {
			t.Fatal("unsupported public transcript gained native preparation", err)
		}
		jobs, err := f.service.Store.List(ctx, store.Filter{Kind: domain.JobKind, SessionID: row.ID, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range jobs {
			job, err := store.Decode[domain.Job](record)
			if err != nil || job.Type == domain.ForkSessionJob {
				t.Fatal("unsupported source allocated fork authority", err)
			}
		}
		return
	}
	req := &pb.ForkSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedTurnId: source.Execution.NativeTurnID, Name: "Independent OpenCode child", Workspace: pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT}
	accepted, err := client.ForkSession(ctx, ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal("fork admission", err)
	}
	replay, err := client.ForkSession(ctx, ownerRequest(f.identity, req))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("fork admission replay", err)
	}
	if j := wait(domain.ID(accepted.Msg.Job.Id)); j.State != domain.JobSucceeded {
		t.Fatal("native fork", j.Problem)
	}
	result, err := client.GetSessionFork(ctx, ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: accepted.Msg.Job.Id}))
	if err != nil || result.Msg.Session == nil {
		t.Fatal("fork not published", err)
	}
	var child domain.Session
	if domain.Decode(result.Msg.Session.DocumentJson, &child) != nil || child.Fork == nil || child.Fork.NativeTurnID == "" || child.Fork.NativeTurnID == child.Fork.SourceTurnID || child.Dispatch != domain.DispatchPaused || child.PendingInputs != 0 || calls.Load() != 2 {
		t.Fatal("fork fabricated selection, input or inference")
	}
	after := f.refresh(t)
	if row.Revision != after.Revision || !bytes.Equal(row.Data, after.Data) {
		t.Fatal("fork changed source session")
	}
	inherited, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(result.Msg.Session.Id), Limit: 200})
	if err != nil || len(inherited) != 4 {
		t.Fatal("inherited canonical transcript missing", err, len(inherited))
	}
	for _, m := range inherited {
		value, err := store.Decode[domain.ExecutionMessage](m)
		if err != nil || value.Inherited == nil || value.Inherited.SessionID != row.ID || value.ExecutionID != child.Fork.RuntimeID || value.InputID != "" || value.NativeThreadID != string(child.Fork.NativeThreadID) {
			t.Fatal("inherited transcript borrowed input/accounting authority")
		}
		source, err := f.service.Store.Get(ctx, domain.MessageKind, value.Inherited.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		original, err := store.Decode[domain.ExecutionMessage](source)
		if err != nil {
			t.Fatal(err)
		}
		originalTiming, _ := json.Marshal(original.TurnTiming)
		inheritedTiming, _ := json.Marshal(value.TurnTiming)
		if !bytes.Equal(originalTiming, inheritedTiming) || value.Role == domain.UserMessage && (value.TurnTiming == nil || value.TurnTiming.TerminalAt == nil) {
			t.Fatal("Fork changed original completed turn timing")
		}
	}
	childDirectory := filepath.Join(f.workerRoot, "workspaces", result.Msg.Session.Id, "chat")
	if err := os.WriteFile(filepath.Join(childDirectory, ".hidden-original"), []byte("Private child edit"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(sourceDirectory, ".hidden-original"))
	if err != nil || string(original) != "Private independent source bytes" {
		t.Fatal("child modified parent files")
	}
	if fault == "parent-delete" || fault == "delete-unstarted-child" {
		target := resourceForTest(row)
		if fault == "delete-unstarted-child" {
			target = result.Msg.Session
		}
		deletion, err := client.DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: acctMutation(target, domain.NewID())}))
		if err != nil {
			t.Fatal("original permanent deletion", err)
		}
		tick := time.NewTicker(20 * time.Millisecond)
		for {
			status, err := client.GetSessionDeletion(ctx, ownerRequest(f.identity, &pb.GetSessionDeletionRequest{SessionId: target.Id}))
			if err != nil {
				t.Fatal(err)
			}
			if status.Msg.Job.Id != deletion.Msg.Job.Id {
				t.Fatal("deletion changed original ownership")
			}
			if status.Msg.Job.State == pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("permanent deletion did not settle")
			case <-tick.C:
			}
		}
		tick.Stop()
		if fault == "delete-unstarted-child" {
			if _, err := os.Lstat(childDirectory); !os.IsNotExist(err) {
				t.Fatal("unstarted child files survived deletion")
			}
			if _, err := os.Lstat(filepath.Join(f.workerRoot, "runtimes", string(child.Fork.RuntimeID))); !os.IsNotExist(err) {
				t.Fatal("unstarted child native runtime survived deletion")
			}
			if _, err := os.Lstat(sourceDirectory); err != nil {
				t.Fatal("child deletion removed parent")
			}
			return
		}
		if _, err := os.Lstat(sourceDirectory); !os.IsNotExist(err) {
			t.Fatal("parent files not removed")
		}
	}
	stop()
	if fault == "missing-claim" {
		if err := os.Remove(filepath.Join(f.workerRoot, "runtimes", string(child.Fork.RuntimeID), "fork-claim-0.json")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.expire-closed-worker", nil, func(tx *store.Tx) (any, error) {
		instance, _, err := tx.WorkerInstance(f.selection.MachineID)
		if err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	stop = startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	inputRaw, _ := json.Marshal(domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Child explicit first input"})
	next, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: result.Msg.Session.Id, DocumentJson: inputRaw}))
	if err != nil || next.Msg.Change.ExecutionJob != nil {
		t.Fatal("paused child implicitly dispatched", err)
	}
	childRow, err := f.service.Store.Get(ctx, domain.SessionKind, domain.ID(result.Msg.Session.Id))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(childRow), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil {
		t.Fatal("child Resume", err)
	}
	if j := wait(domain.ID(resumed.Msg.Change.ExecutionJob.Id)); fault == "missing-claim" {
		if j.State != domain.JobUncertain || j.Problem == nil || j.Problem.Code != domain.RecoveryRequired || calls.Load() != 2 {
			t.Fatal("missing original fork claim gained inference", j.Problem, calls.Load())
		}
		return
	} else if j.State != domain.JobSucceeded {
		t.Fatal("first native fork continuation", j.Problem)
	}
	childRow, err = f.service.Store.Get(ctx, domain.SessionKind, domain.ID(result.Msg.Session.Id))
	if err != nil {
		t.Fatal(err)
	}
	if domain.Decode(childRow.Data, &child) != nil || child.Execution == nil || child.Execution.NativeThreadID != string(child.Fork.NativeThreadID) || child.Execution.NativeTurnID == string(child.Fork.NativeTurnID) || !child.Execution.CleanupVerified || calls.Load() != 3 {
		t.Fatal("child first input did not prove actual independent selection")
	}
	// A later ordinary input must restore the child lineage without the original
	// source workspace or original source runtime, including after parent deletion.
	laterRaw, _ := json.Marshal(domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Child later ordinary input"})
	later, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: result.Msg.Session.Id, DocumentJson: laterRaw}))
	if err != nil || later.Msg.Change.ExecutionJob == nil {
		t.Fatal("later independent input", err)
	}
	var laterJob domain.Job
	var laterAssignment domain.ExecutionJobInput
	if domain.Decode(later.Msg.Change.ExecutionJob.DocumentJson, &laterJob) != nil || domain.Decode(laterJob.Input, &laterAssignment) != nil || laterAssignment.InputID != domain.ID(later.Msg.Change.Input.Id) {
		latest, err := f.service.Store.Get(ctx, domain.SessionKind, domain.ID(result.Msg.Session.Id))
		if err != nil {
			t.Fatal(err)
		}
		resume, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(latest), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
		if err != nil {
			t.Fatal("later child Resume", err)
		}
		later.Msg.Change.ExecutionJob = resume.Msg.Change.ExecutionJob
	}
	if j := wait(domain.ID(later.Msg.Change.ExecutionJob.Id)); j.State != domain.JobSucceeded || calls.Load() != 4 {
		t.Fatal("later independent completion", j.Problem, calls.Load())
	}
	usageClient := delidevv1connect.NewUsageServiceClient(http.DefaultClient, f.endpoint.URL)
	summary, err := usageClient.GetUsageSummary(ctx, ownerRequest(f.identity, &pb.GetUsageSummaryRequest{SessionId: result.Msg.Session.Id, AccountingProfile: pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1}))
	if err != nil {
		t.Fatal(err)
	}
	var units uint32
	for _, u := range summary.Msg.NativeAccounting {
		if u.Totals.Kind == pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_OPENCODE_STEP {
			units += u.Totals.Units
		}
	}
	if units != 2 {
		t.Fatal("inherited usage charged again", units)
	}
	t.Log("actual pinned native OpenCode, authenticated Connect/SQLite/Worker, independent file/history clone and paused first child Resume across Worker restart; scripted provider/current platform only")
}
