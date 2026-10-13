// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// archivedNativeReview is a claimed original native review whose session
// accepted Archive while the review ran. Progress stops at the requested state.
type archivedNativeReview struct {
	f       *firstDispatchFixture
	client  delidevv1connect.SessionServiceClient
	claim   store.Record
	input   domain.NativeCodeReviewInput
	archive *pb.ControlSessionRequest
}

func newArchivedNativeReview(t *testing.T, progress domain.NativeCodeReviewState) *archivedNativeReview {
	t.Helper()
	ctx := context.Background()
	f := newFirstDispatchFixtureWorkspaceProfile(t, domain.Codex, domain.PlanMode, "/fixture/codex", "", "fixture-model", domain.Local, openCodeMultipleProject)
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	c := &continuationFixture{firstDispatchFixture: f, thread: domain.NewID()}
	c.claim(t)
	c.complete(t, domain.ExecutionSucceeded)
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.review-capability", nil, func(tx *store.Tx) (any, error) {
		row, machine, err := activeMachine(tx, f.selection.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NativeCodexReviewV1)
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest workspace.Manifest
	if domain.Decode(c.input.Manifest, &manifest) != nil || len(manifest.Repositories) == 0 {
		t.Fatal("fixture lost original workspace ownership")
	}
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: manifest.Repositories[0].ID, DiffRevision: strings.Repeat("a", 64)}
	client := sessionClient(f.accountFixture)
	accepted, err := client.CreateNativeCodeReview(ctx, ownerRequest(f.identity, &pb.CreateNativeCodeReviewRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Target: rpc.NativeReviewTargetWire(target)}))
	if err != nil {
		t.Fatal(err)
	}
	jobRecord, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(accepted.Msg.Review.Job.Id))
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](jobRecord)
	var input domain.NativeCodeReviewInput
	if err != nil || job.Type != domain.NativeCodeReviewJob || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil {
		t.Fatal("stored native review assignment is invalid", err)
	}
	claimed, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-review", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.JobKind, jobRecord.ID)
		if err != nil {
			return nil, err
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
		return tx.PutJob(row.ID, row.Revision, row.SessionID, row.ProjectID, job)
	})
	var claim store.Record
	if err != nil || json.Unmarshal(claimed.Data, &claim) != nil {
		t.Fatal("could not claim review", err)
	}
	token := apiproxy.TokenPrefix + "stale-native-review-token"
	digest := sha256.Sum256([]byte(token))
	if _, err := f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]})); err != nil {
		t.Fatal(err)
	}
	review := &archivedNativeReview{f: f, client: client, claim: claim, input: input}
	selection := domain.NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)}
	thread, turn := domain.NewID(), domain.NewID()
	publish := func(state domain.NativeCodeReviewState, item string) {
		req := &pb.PublishNativeCodeReviewRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, State: rpc.NativeReviewStateWire(state), Selection: rpc.NativeReviewSelectionWire(selection), ThreadId: string(thread), ItemId: item}
		if state != domain.NativeReviewReady {
			req.TurnId = string(turn)
		}
		if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, req)); err != nil {
			t.Fatal("review progress was not accepted", state, err)
		}
	}
	for _, step := range []struct {
		state domain.NativeCodeReviewState
		item  string
	}{{domain.NativeReviewReady, ""}, {domain.NativeReviewEntered, "entered"}, {domain.NativeReviewExited, "exited"}} {
		publish(step.state, step.item)
		if step.state == progress {
			break
		}
	}
	review.archive = &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}
	if _, err := client.ControlSession(ctx, ownerRequest(f.identity, review.archive)); err != nil {
		t.Fatal(err)
	}
	if got := review.session(t).Archive; got != domain.ArchivePending {
		t.Fatalf("archive with a claimed review was not pending: %v", got)
	}
	return review
}

func (r *archivedNativeReview) session(t *testing.T) domain.Session {
	t.Helper()
	session, err := store.Decode[domain.Session](r.f.refresh(t))
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (r *archivedNativeReview) report(t *testing.T, output []byte, problem domain.Code) error {
	t.Helper()
	req := &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(r.claim), domain.NewID()), MachineId: r.f.machine.Id, InstanceId: r.f.workerInstance, OutputJson: output}
	if problem != "" {
		req.Problem = &pb.ErrorDetail{Code: string(problem)}
	}
	_, err := r.f.workerClient.ReportWork(context.Background(), ownerRequest(r.f.workerIdentity, req))
	return err
}

func (r *archivedNativeReview) job(t *testing.T) domain.Job {
	t.Helper()
	row, err := r.f.service.Store.Get(context.Background(), domain.JobKind, r.claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (r *archivedNativeReview) staleProof(t *testing.T, action domain.ID) []byte {
	t.Helper()
	raw, err := json.Marshal(domain.NativeCodeReviewStaleProof{Version: 1, ActionID: action, Stale: true, CleanupVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (r *archivedNativeReview) view(t *testing.T) *pb.NativeCodeReview {
	t.Helper()
	view, err := r.client.GetNativeCodeReview(context.Background(), ownerRequest(r.f.identity, &pb.GetNativeCodeReviewRequest{SessionId: r.f.change.Session.Id, JobId: string(r.claim.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	return view.Msg.Review
}

// activeReview reports the one unfinished native review of the session, if any.
func (r *archivedNativeReview) activeReview(t *testing.T) (domain.ID, bool) {
	t.Helper()
	var id domain.ID
	var found bool
	if err := r.f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		row, _, ok, err := tx.ActiveNativeCodeReview(r.claim.SessionID)
		id, found = row.ID, ok
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id, found
}

func TestNativeReviewStaleCleanupSettlesPendingArchive(t *testing.T) {
	r := newArchivedNativeReview(t, domain.NativeReviewExited)
	if err := r.report(t, r.staleProof(t, r.input.ActionID), ""); err != nil {
		t.Fatal("verified stale cleanup was not accepted", err)
	}
	job := r.job(t)
	if job.State != domain.JobFailed || job.Problem == nil || job.Problem.Code != domain.Conflict || len(job.Output) == 0 {
		t.Fatalf("stale review did not become a terminal conflict: %+v", job)
	}
	var result domain.NativeCodeReviewResult
	if domain.Decode(job.Output, &result) == nil && result.Validate() == nil {
		t.Fatal("stale output is publishable as a current review result")
	}
	session := r.session(t)
	if session.Archive != domain.Archived || session.Dispatch != domain.DispatchPaused || session.NextExecutionIntent != "" {
		t.Fatalf("verified stale cleanup left Archive unsettled: %+v", session)
	}
	view := r.view(t)
	if view.State != rpc.NativeReviewStateWire(domain.NativeReviewStale) || view.Result != nil {
		t.Fatalf("stale review exposed state %v result %v", view.State, view.Result)
	}
	if _, found := r.activeReview(t); found {
		t.Fatal("stale cleanup left or created an unfinished native review")
	}
	// The original Archive control receipt replays unchanged.
	if _, err := r.client.ControlSession(context.Background(), ownerRequest(r.f.identity, r.archive)); err != nil {
		t.Fatal("archive control receipt changed after stale settlement", err)
	}
	if r.session(t).Archive != domain.Archived {
		t.Fatal("archive receipt replay changed the settled session")
	}
	if err := r.report(t, r.staleProof(t, r.input.ActionID), ""); err == nil {
		t.Fatal("terminal stale review accepted a second report with a new request")
	}
}

func TestNativeReviewStaleWithoutVerifiedProofKeepsArchivePending(t *testing.T) {
	for name, tc := range map[string]struct {
		progress domain.NativeCodeReviewState
		output   func(*archivedNativeReview) []byte
		problem  domain.Code
		wantJob  domain.JobState
		reported bool
	}{
		"bare conflict without proof":    {progress: domain.NativeReviewExited, problem: domain.Conflict, wantJob: domain.JobFailed, reported: true},
		"recovery required":              {progress: domain.NativeReviewExited, problem: domain.RecoveryRequired, wantJob: domain.JobUncertain, reported: true},
		"proof for a foreign action":     {progress: domain.NativeReviewExited, output: func(r *archivedNativeReview) []byte { return r.staleProof(t, domain.NewID()) }, wantJob: domain.JobClaimed},
		"proof before native completion": {progress: domain.NativeReviewEntered, output: func(r *archivedNativeReview) []byte { return r.staleProof(t, r.input.ActionID) }, wantJob: domain.JobClaimed},
	} {
		t.Run(name, func(t *testing.T) {
			r := newArchivedNativeReview(t, tc.progress)
			var output []byte
			if tc.output != nil {
				output = tc.output(r)
			}
			err := r.report(t, output, tc.problem)
			if tc.reported && err != nil || !tc.reported && err == nil {
				t.Fatalf("unexpected report outcome %v", err)
			}
			job := r.job(t)
			if job.State != tc.wantJob || tc.reported && len(job.Output) != 0 {
				t.Fatalf("unexpected job after unverified stale report: %+v", job)
			}
			if got := r.session(t).Archive; got != domain.ArchivePending {
				t.Fatalf("unverified cleanup settled Archive: %v", got)
			}
			// An unfinished job still fences replacement; a Failed one has no second review.
			if id, found := r.activeReview(t); found && id != r.claim.ID {
				t.Fatal("unverified cleanup created another native review")
			}
		})
	}
}
