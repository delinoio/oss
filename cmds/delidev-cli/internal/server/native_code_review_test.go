// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestNativeReviewCancellationPreservesOriginalLifecycleAndUsageReceipts(t *testing.T) {
	for _, control := range []string{"stop", "archive", "disconnect"} {
		t.Run(control, func(t *testing.T) {
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
			if domain.Decode(c.input.Manifest, &manifest) != nil || len(manifest.Repositories) != 3 {
				t.Fatal("fixture lost original multi-repository ownership")
			}
			source, _ := store.Decode[domain.Session](f.refresh(t))
			target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: manifest.Repositories[0].ID, DiffRevision: strings.Repeat("a", 64)}
			client := sessionClient(f.accountFixture)
			creation := &pb.CreateNativeCodeReviewRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Target: rpc.NativeReviewTargetWire(target)}
			accepted, err := client.CreateNativeCodeReview(ctx, ownerRequest(f.identity, creation))
			if err != nil {
				t.Fatal(err)
			}
			repeated, err := client.CreateNativeCodeReview(ctx, ownerRequest(f.identity, creation))
			if err != nil || !repeated.Msg.Replayed || repeated.Msg.Review.Job.Id != accepted.Msg.Review.Job.Id {
				t.Fatal("creation receipt changed original review", err)
			}
			var job domain.Job
			var input domain.NativeCodeReviewInput
			if domain.DecodeNativeCodeReviewJob(accepted.Msg.Review.Job.DocumentJson, &job) != nil || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil {
				t.Fatal("invalid native review assignment")
			}
			claimed, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-review", nil, func(tx *store.Tx) (any, error) {
				row, err := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Review.Job.Id))
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
			token := apiproxy.TokenPrefix + "original-native-review-token"
			digest := sha256.Sum256([]byte(token))
			_, err = f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
			if err != nil {
				t.Fatal(err)
			}
			selection := domain.NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)}
			thread, turn := domain.NewID(), domain.NewID()
			publication := func(state domain.NativeCodeReviewState, item string) *pb.PublishNativeCodeReviewRequest {
				req := &pb.PublishNativeCodeReviewRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, State: rpc.NativeReviewStateWire(state), Selection: rpc.NativeReviewSelectionWire(selection), ThreadId: string(thread), ItemId: item}
				if state != domain.NativeReviewReady {
					req.TurnId = string(turn)
				}
				return req
			}
			ready := publication(domain.NativeReviewReady, "")
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, ready)); err != nil {
				t.Fatal(err)
			}
			if control == "disconnect" {
				account, err := f.service.Store.Get(ctx, domain.AccountKind, input.Source.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(resourceForTest(account), domain.NewID())}))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				action := pb.SessionAction_SESSION_ACTION_STOP
				if control == "archive" {
					action = pb.SessionAction_SESSION_ACTION_ARCHIVE
				}
				if _, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Action: action})); err != nil {
					t.Fatal(err)
				}
			}
			if lease, err := f.service.executionAuthority.Acquire(ctx, token); err == nil {
				lease.Release()
				t.Fatal("cancellation restored review inference authority")
			}
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, publication(domain.NativeReviewReady, ""))); err == nil {
				t.Fatal("canceled review admitted new Ready authority")
			}
			entered := publication(domain.NativeReviewEntered, "original-entered")
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, entered)); err != nil {
				t.Fatal("cancellation erased observed entered state", err)
			}
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, entered)); err != nil {
				t.Fatal("entered receipt could not replay", err)
			}
			foreign := publication(domain.NativeReviewExited, "foreign-exited")
			foreign.TurnId = string(domain.NewID())
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, foreign)); err == nil {
				t.Fatal("foreign original evidence was adopted")
			}
			usage := publication(domain.NativeReviewEntered, "")
			usage.Usage = rpc.NativeReviewUsageWire(domain.NativeResponseUsage{ResponseDigest: strings.Repeat("d", 64), CostEvidence: domain.UsageCostMissing}, 1, domain.CodexProtocolVersion)
			for range 2 {
				if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, usage)); err != nil {
					t.Fatal("cancellation erased original measured usage", err)
				}
			}
			if _, err := f.workerClient.PublishNativeCodeReview(ctx, ownerRequest(f.workerIdentity, publication(domain.NativeReviewExited, "original-exited"))); err != nil {
				t.Fatal("cancellation erased observed exited state", err)
			}
			result := domain.NativeCodeReviewResult{Version: 1, ActionID: input.ActionID, Selection: selection, ThreadID: domain.NativeIdentity(thread), TurnID: domain.NativeIdentity(turn), EnteredItemID: "original-entered", ExitedItemID: "original-exited", Findings: []domain.NativeCodeReviewFinding{}, Correctness: "patch is correct", RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true}
			output, _ := json.Marshal(result)
			if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: output})); err != nil {
				t.Fatal("original cleanup/result evidence lost", err)
			}
			view, err := client.GetNativeCodeReview(ctx, ownerRequest(f.identity, &pb.GetNativeCodeReviewRequest{SessionId: string(f.refresh(t).ID), JobId: string(claim.ID)}))
			if err != nil || view.Msg.Review.Result == nil || !view.Msg.Review.UsageComplete {
				t.Fatal("retained result lost independent usage", err)
			}
			after, _ := store.Decode[domain.Session](f.refresh(t))
			if after.Outcome != source.Outcome || after.ExecutionSelection() != source.ExecutionSelection() || !reflect.DeepEqual(after.Execution, source.Execution) {
				t.Fatal("review evidence rewrote original conversation")
			}
			if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
				values, err := tx.NativeCodeReviewUsage(claim.ID)
				if err == nil && len(values) != 1 {
					t.Fatal("original usage receipt charged twice")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
