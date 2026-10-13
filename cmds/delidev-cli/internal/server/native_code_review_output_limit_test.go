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

// nativeReviewExitedFixture claims an original native review and publishes its
// Ready, Entered and Exited observations so a terminal result can settle it.
type nativeReviewExitedFixture struct {
	*firstDispatchFixture
	client    delidevv1connect.SessionServiceClient
	claim     store.Record
	reviewJob string
	session   string
	input     domain.NativeCodeReviewInput
	selection domain.NativeCodeReviewSelection
	thread    domain.ID
	turn      domain.ID
}

func newNativeReviewExitedFixture(t *testing.T) *nativeReviewExitedFixture {
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
	if err != nil {
		t.Fatal(err)
	}
	var input domain.NativeCodeReviewInput
	if job.Type != domain.NativeCodeReviewJob || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil {
		t.Fatal("stored native review assignment is invalid")
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
		t.Fatal("original review claim", err)
	}
	digest := sha256.Sum256([]byte(apiproxy.TokenPrefix + "output-limit-review-token"))
	if _, err = f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]})); err != nil {
		t.Fatal(err)
	}
	fixture := &nativeReviewExitedFixture{firstDispatchFixture: f, client: client, claim: claim, reviewJob: accepted.Msg.Review.Job.Id, session: f.change.Session.Id, input: input, thread: domain.NewID(), turn: domain.NewID()}
	fixture.selection = domain.NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)}
	for _, step := range []struct {
		state domain.NativeCodeReviewState
		item  string
	}{{domain.NativeReviewReady, ""}, {domain.NativeReviewEntered, "original-entered"}, {domain.NativeReviewExited, "original-exited"}} {
		req := &pb.PublishNativeCodeReviewRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, State: rpc.NativeReviewStateWire(step.state), Selection: rpc.NativeReviewSelectionWire(fixture.selection), ThreadId: string(fixture.thread), ItemId: step.item}
		if step.state != domain.NativeReviewReady {
			req.TurnId = string(fixture.turn)
		}
		if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, req)); err != nil {
			t.Fatal("publish", step.state, err)
		}
	}
	return fixture
}

func (n *nativeReviewExitedFixture) result(findings []domain.NativeCodeReviewFinding) domain.NativeCodeReviewResult {
	return domain.NativeCodeReviewResult{Version: 1, ActionID: n.input.ActionID, Selection: n.selection, ThreadID: domain.NativeIdentity(n.thread), TurnID: domain.NativeIdentity(n.turn), EnteredItemID: "original-entered", ExitedItemID: "original-exited", Findings: findings, Explanation: "Bounded explanation.", Correctness: "patch is incorrect", Confidence: 0.5, RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true}
}

func TestNativeReviewLargeResultUnderOutputCeilingSettlesWithExactFindings(t *testing.T) {
	ctx := context.Background()
	f := newNativeReviewExitedFixture(t)
	findings := []domain.NativeCodeReviewFinding{}
	for range 64 {
		// Escaped characters count at their six-byte encoded size.
		findings = append(findings, domain.NativeCodeReviewFinding{Title: "Preserve original evidence", Body: strings.Repeat("<", 2000), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 3, EndLine: 4})
	}
	result := f.result(findings)
	output, err := json.Marshal(result)
	if err != nil || len(output) >= domain.MaxWorkerJobOutputBytes || len(output) < domain.MaxWorkerJobOutputBytes/2 {
		t.Fatalf("fixture must be a large result under the ceiling: %d %v", len(output), err)
	}
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(f.claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: output})); err != nil {
		t.Fatal("result under the ceiling did not settle", err)
	}
	view, err := f.client.GetNativeCodeReview(ctx, ownerRequest(f.identity, &pb.GetNativeCodeReviewRequest{SessionId: f.session, JobId: f.reviewJob}))
	if err != nil || view.Msg.Review.Result == nil || len(view.Msg.Review.Result.Findings) != 64 {
		t.Fatal("settled review lost findings", err)
	}
	for _, finding := range view.Msg.Review.Result.Findings {
		if finding.Body != strings.Repeat("<", 2000) || finding.Title != "Preserve original evidence" || finding.Path != "src/main.go" {
			t.Fatal("settled finding changed")
		}
	}
}

func TestNativeReviewOverCeilingResultIsNotAcceptedAsSuccess(t *testing.T) {
	ctx := context.Background()
	f := newNativeReviewExitedFixture(t)
	findings := []domain.NativeCodeReviewFinding{}
	for range 64 {
		findings = append(findings, domain.NativeCodeReviewFinding{Title: strings.Repeat("<", 1024), Body: strings.Repeat("<", 8192), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 3, EndLine: 4})
	}
	output, _ := json.Marshal(f.result(findings))
	if len(output) <= domain.MaxWorkerJobOutputBytes {
		t.Fatal("fixture must exceed the ceiling")
	}
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(f.claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: output})); err == nil {
		t.Fatal("over-limit result settled as success")
	}
	// The Worker's bounded failure path reports a typed problem instead.
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(f.claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Problem: &pb.ErrorDetail{Code: string(domain.ResourceExhausted)}})); err != nil {
		t.Fatal("typed resource exhaustion did not settle the claimed review", err)
	}
	view, err := f.client.GetNativeCodeReview(ctx, ownerRequest(f.identity, &pb.GetNativeCodeReviewRequest{SessionId: f.session, JobId: f.reviewJob}))
	if err != nil || view.Msg.Review.State != rpc.NativeReviewStateWire(domain.NativeReviewUncertain) || view.Msg.Review.Result != nil {
		t.Fatal("over-limit review was not retained as uncertain without a result", err)
	}
}
