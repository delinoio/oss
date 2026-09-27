package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type claudePublicRecoveryCase struct {
	turn              int
	missingCheckpoint bool
}
type claudePublicRecoveryFixture struct {
	endpoint string
	profile  claudePublicRecoveryCase
	lost     chan struct{}
	allow    atomic.Bool
	calls    *atomic.Int32
	started  bool
}

func TestManualNativeClaudeCompletedExecutionRecovery(t *testing.T) {
	for _, scenario := range []claudePublicCase{claudePublicContinuation, claudePublicRead, claudePublicReadError, claudePublicQuestionHistory, claudePublicBash, claudePublicWrite, claudePublicEdit} {
		for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
			if claudePublicEffectName(scenario) != "" && mode == domain.PlanMode {
				continue
			}
			for _, turn := range []int{1, 2} {
				t.Run(fmt.Sprintf("%s/%s/turn-%d", scenario, mode, turn), func(t *testing.T) {
					nativeClaudePublicDispatch(t, mode, scenario, claudePublicRecoveryCase{turn: turn})
				})
			}
		}
	}
}

func TestManualNativeClaudeRecoveryRequiresOriginalCheckpoint(t *testing.T) {
	for _, turn := range []int{1, 2} {
		t.Run(fmt.Sprintf("turn-%d", turn), func(t *testing.T) {
			nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicContinuation, claudePublicRecoveryCase{turn: turn, missingCheckpoint: true})
		})
	}
}

func newClaudePublicRecoveryFixture(t *testing.T, f *firstDispatchFixture, calls *atomic.Int32, profile claudePublicRecoveryCase, perTurn int32) *claudePublicRecoveryFixture {
	t.Helper()
	fixture := &claudePublicRecoveryFixture{profile: profile, lost: make(chan struct{}, 1), calls: calls}
	handler := f.service.Handler(nil, true)
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == delidevv1connect.WorkerServiceReportWorkProcedure && calls.Load() >= int32(profile.turn)*perTurn && !fixture.allow.Load() {
			select {
			case fixture.lost <- struct{}{}:
			default:
			}
			http.Error(w, "Scripted lost original report before acceptance", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fault.Close)
	fixture.endpoint = fault.URL
	return fixture
}

func startClaudePublicFixtureWorker(t *testing.T, ctx context.Context, root string) func() {
	t.Helper()
	running, stop := context.WithCancel(ctx)
	done, ready := make(chan struct{}), make(chan domain.ID, 1)
	go func() {
		defer close(done)
		_ = worker.Run(running, worker.Config{Root: root, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), Ready: func(id domain.ID) {
			select {
			case ready <- id:
			default:
			}
		}})
	}()
	join := func() { stop(); <-done }
	t.Cleanup(join)
	select {
	case <-ready:
	case <-done:
		t.Fatal("Worker failed before readiness")
	case <-ctx.Done():
		t.Fatal("Worker did not attach")
	}
	return join
}

func (r *claudePublicRecoveryFixture) ready() bool {
	if r.started {
		return false
	}
	select {
	case <-r.lost:
		r.started = true
		return true
	default:
		return false
	}
}

func (r *claudePublicRecoveryFixture) replaceWorker(t *testing.T, ctx context.Context, f *firstDispatchFixture) func() {
	t.Helper()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.expire-claude-recovery-worker", nil, func(tx *store.Tx) (any, error) {
		instance, _, err := tx.WorkerInstance(f.selection.MachineID)
		if err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	r.allow.Store(true)
	return startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
}

func (r *claudePublicRecoveryFixture) reconcile(t *testing.T, ctx context.Context, f *firstDispatchFixture, assignment *pb.Resource) bool {
	t.Helper()
	beforeCalls := r.calls.Load()
	path := filepath.Join(f.workerRoot, "jobs", assignment.Id+".json")
	original, err := security.ReadPrivate(path, 2<<20)
	var operation struct {
		ReportID domain.ID       `json:"report_id"`
		Output   json.RawMessage `json:"output"`
	}
	var completion domain.ExecutionCompletion
	if err != nil || json.Unmarshal(original, &operation) != nil || operation.ReportID.Validate() != nil || domain.Decode(operation.Output, &completion) != nil || completion.Version != 2 {
		t.Fatal("original completed report missing", err)
	}
	checkpointPath := filepath.Join(f.workerRoot, "runtimes", string(completion.ExecutionID), "native-completion.json")
	checkpoint, err := security.ReadPrivate(checkpointPath, 9<<20)
	if err != nil {
		t.Fatal(err)
	}
	if r.profile.missingCheckpoint {
		if err := os.Remove(checkpointPath); err != nil {
			t.Fatal(err)
		}
	}
	client := sessionClient(f.accountFixture)
	sr := f.refresh(t)
	req := &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, ExpectedExecutionId: string(completion.ExecutionID)}
	response, err := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, req))
	if err != nil || response.Msg.Change.ExecutionRecoveryJob == nil {
		t.Fatal("public Claude recovery refused original comparison", err)
	}
	var recovery domain.Job
	var comparison domain.ExecutionRecoveryRequest
	if domain.Decode(response.Msg.Change.ExecutionRecoveryJob.DocumentJson, &recovery) != nil || domain.Decode(recovery.Input, &comparison) != nil || comparison.Validate() != nil || comparison.Harness != domain.ClaudeCode || comparison.Claude == nil || comparison.Claude.ClaimVersion != uint32(r.profile.turn) {
		t.Fatal("original Claude comparison profile changed")
	}
	var evidence domain.ExecutionRecoveryEvidence
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		row, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(response.Msg.Change.ExecutionRecoveryJob.Id))
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil {
			t.Fatal(err)
		}
		if job.State.Terminal() || job.State == domain.JobUncertain {
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || session.Execution == nil || session.Dispatch != domain.DispatchPaused || session.NextExecutionIntent != "" || session.Outcome != domain.ExecutionSucceeded || r.calls.Load() != beforeCalls {
				t.Fatal("recovery changed original outcome or authorized input", err, job.State, job.Problem)
			}
			if r.profile.missingCheckpoint {
				if job.State == domain.JobSucceeded || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || session.Recovery != domain.NeedsRecovery || session.Execution.CleanupVerified {
					t.Fatal("missing checkpoint became completion proof", job.State, job.Problem)
				}
			} else if job.State != domain.JobSucceeded || session.Recovery != domain.NoRecovery || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || domain.Decode(job.Output, &evidence) != nil || evidence.Validate(comparison) != nil || evidence.ReportID != operation.ReportID || evidence.Completion != completion {
				t.Fatal("recovery did not preserve paused original completion", job.State, job.Problem)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Claude recovery did not finish")
		}
	}
	after, err := security.ReadPrivate(path, 2<<20)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("recovery rewrote original operation", err)
	}
	if r.profile.missingCheckpoint {
		if _, err := os.Lstat(checkpointPath); !os.IsNotExist(err) {
			t.Fatal("recovery reconstructed missing evidence")
		}
		return false
	}
	assertClaudeRecoveryComparison(t, ctx, f.workerRoot, comparison, evidence)
	after, err = security.ReadPrivate(checkpointPath, 9<<20)
	if err != nil || !bytes.Equal(checkpoint, after) {
		t.Fatal("recovery rewrote native checkpoint", err)
	}
	if replay, err := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, req)); err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionRecoveryJob.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
		t.Fatal("recovery receipt did not replay exactly", err)
	}
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err == nil {
		t.Fatal("paused recovery authorized automatic dispatch")
	}
	if r.calls.Load() != beforeCalls {
		t.Fatal("recovery authorized automatic input")
	}
	return true
}

func assertClaudeRecoveryComparison(t *testing.T, ctx context.Context, root string, request domain.ExecutionRecoveryRequest, expected domain.ExecutionRecoveryEvidence) {
	t.Helper()
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(request.Preparation, &preparation) != nil || domain.Decode(request.Manifest, &manifest) != nil {
		t.Fatal("invalid original workspace comparison")
	}
	digest, _ := hex.DecodeString(request.PromptDigest)
	ref := worker.CompletedExecutionRef{
		Harness: request.Harness, Claude: request.Claude, ServerID: request.ServerID, DeviceID: request.DeviceID, InstanceID: request.InstanceID,
		AssignmentRevision: request.AssignmentRevision, AssignmentDigest: request.AssignmentDigest, Preparation: preparation, Manifest: manifest,
		Checkpoint: worker.ExecutionCheckpointRef{JobID: request.JobID, SessionID: request.SessionID, MachineID: request.MachineID,
			HistoryExecutionID: request.HistoryExecutionID, AssignmentInputDigest: request.AssignmentInputDigest, ConfigurationDigest: request.ConfigurationDigest,
			AccountID: request.AccountID, ConnectionID: request.ConnectionID, Completion: request.Completion, InputMode: request.InputMode, AcceptedInputs: request.AcceptedInputs},
	}
	copy(ref.Checkpoint.PromptDigest[:], digest)
	manager := &workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	for range 2 {
		proof, err := worker.InspectCompletedExecution(ctx, manager, ref)
		if err != nil || proof != expected {
			t.Fatal("original recovery evidence changed on repeated read", err)
		}
	}
	for _, change := range []func(*worker.CompletedExecutionRef){
		func(r *worker.CompletedExecutionRef) { r.Harness = domain.Codex },
		func(r *worker.CompletedExecutionRef) { r.Claude = nil },
		func(r *worker.CompletedExecutionRef) { r.Claude.Version = "unverified" },
		func(r *worker.CompletedExecutionRef) { r.Claude.Executable += "-foreign" },
		func(r *worker.CompletedExecutionRef) { r.Claude.Model += "-foreign" },
		func(r *worker.CompletedExecutionRef) { r.Claude.Effort = "high" },
		func(r *worker.CompletedExecutionRef) {
			r.Claude.Permission = domain.ClaudePermissionMode("bypassPermissions")
		},
		func(r *worker.CompletedExecutionRef) { r.Claude.InstructionsDigest = strings.Repeat("ab", 32) },
		func(r *worker.CompletedExecutionRef) { r.Claude.BindingRequestID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.Claude.InputRequestID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.InstanceID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.DeviceID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.AssignmentRevision++ },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.HistoryExecutionID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.AssignmentInputDigest = strings.Repeat("ab", 32) },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.Completion.LastSequence++ },
		func(r *worker.CompletedExecutionRef) { r.Manifest.PrimaryPath = filepath.Dir(r.Manifest.PrimaryPath) },
	} {
		next, native := ref, *ref.Claude
		next.Claude = &native
		change(&next)
		if _, err := worker.InspectCompletedExecution(ctx, manager, next); err == nil {
			t.Fatal("changed original comparison evidence accepted")
		}
	}
}
