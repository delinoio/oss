// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
)

func TestNativeShellProgressPreservesOriginalProcess(t *testing.T) {
	before := domain.NativeShellObservation{Version: 1, ActionID: domain.NewID(), NativeThreadID: domain.NewID(), Delivery: domain.NativeShellAcknowledged, Sequence: 1, Processes: []domain.NativeShellProcess{{ItemID: "original", Command: "pwd", Cwd: "/original", Status: domain.NativeShellRunning, Output: "/"}}}
	after := before
	after.Sequence++
	after.Processes = append([]domain.NativeShellProcess{}, before.Processes...)
	after.Processes[0].Output = "/original"
	if !nativeShellProgress(before, after) {
		t.Fatal("monotonic original output rejected")
	}
	after.Processes[0].ItemID = "replacement"
	if nativeShellProgress(before, after) {
		t.Fatal("replacement process accepted")
	}
	after.Processes[0] = before.Processes[0]
	after.Processes[0].Output = "changed"
	if nativeShellProgress(before, after) {
		t.Fatal("changed original output accepted")
	}
	after.Processes = nil
	if nativeShellProgress(before, after) {
		t.Fatal("original process erased")
	}
}
func TestNativeShellCannotBeAuthorizedByMissingPrincipal(t *testing.T) {
	if nativeShellActor(context.Background()) == nil {
		t.Fatal("unauthenticated shell authority accepted")
	}
}

func TestNativeShellDurableOriginalRequestAndImmutableClaim(t *testing.T) {
	for _, scenario := range []string{"completed-after-publication", "queued-cancel", "claimed-cancel", "lost-report"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, domain.Codex)
			f := c.firstDispatchFixture
			f.workerStream.Close()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.shell-capability", nil, func(tx *store.Tx) (any, error) {
				r, m, e := activeMachine(tx, f.selection.MachineID)
				if e != nil {
					return nil, e
				}
				m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexNativeShellV1)
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			client := sessionClient(f.accountFixture)
			before := f.refresh(t)
			request := domain.NewID()
			run := &pb.RunNativeShellRequest{Mutation: acctMutation(resourceForTest(before), request), Command: "printf original", FullAccessConfirmed: true}
			if _, err := client.RunNativeShell(ctx, ownerRequest(f.workerIdentity, run)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("Worker synthesized human action: %v", err)
			}
			stale := *run
			stale.Mutation = acctMutation(resourceForTest(before), domain.NewID())
			stale.Mutation.ExpectedRevision++
			if _, err := client.RunNativeShell(ctx, ownerRequest(f.identity, &stale)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatalf("stale session accepted: %v", err)
			}
			accepted, err := client.RunNativeShell(ctx, ownerRequest(f.identity, run))
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Msg.Job.Id != string(request) {
				t.Fatal("lost Run cannot recover original request")
			}
			recovered, err := client.GetNativeShell(ctx, ownerRequest(f.identity, &pb.GetNativeShellRequest{JobId: string(request)}))
			if err != nil || recovered.Msg.Job.Id != accepted.Msg.Job.Id {
				t.Fatal("read-only original recovery failed", err)
			}
			replay, err := client.RunNativeShell(ctx, ownerRequest(f.identity, run))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
				t.Fatal("durable original receipt changed", err)
			}
			var job domain.Job
			var input domain.SessionCompactionInput
			if domain.DecodeCompactionJob(accepted.Msg.Job.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil {
				t.Fatal("invalid original source")
			}
			if scenario == "queued-cancel" {
				reply, e := client.CancelNativeShell(ctx, ownerRequest(f.identity, &pb.CancelNativeShellRequest{Mutation: acctMutation(accepted.Msg.Job, domain.NewID())}))
				if e != nil {
					t.Fatal(e)
				}
				var canceled domain.Job
				if domain.DecodeCompactionJob(reply.Msg.Job.DocumentJson, &canceled) != nil || canceled.State != domain.JobCanceled {
					t.Fatal("queued cancellation did not prove no-send")
				}
				return
			}
			claimed, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-shell", nil, func(tx *store.Tx) (any, error) {
				r, e := tx.Get(domain.JobKind, request)
				if e != nil {
					return nil, e
				}
				job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
				return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
			})
			if e != nil {
				t.Fatal(e)
			}
			var claim store.Record
			if json.Unmarshal(claimed.Data, &claim) != nil {
				t.Fatal("invalid frozen claim")
			}
			var manifest workspace.Manifest
			domain.Decode(input.Assignment.Manifest, &manifest)
			observed := domain.NativeShellObservation{Version: 1, ActionID: input.ActionID, NativeThreadID: domain.ID(input.Completion.NativeThreadID), NativeTurnID: domain.NewID(), Delivery: domain.NativeShellAcknowledged, Terminal: true, Processes: []domain.NativeShellProcess{{ItemID: "original-shell", Command: input.Shell.Command, Cwd: manifest.PrimaryPath, Status: domain.NativeShellCompleted, Output: "original"}}, Sequence: 1}
			foreign := observed
			foreign.Processes = append([]domain.NativeShellProcess{}, observed.Processes...)
			foreign.Processes[0].Command = "foreign command"
			foreignRaw, _ := json.Marshal(foreign)
			if _, e := f.workerClient.PublishNativeShell(ctx, ownerRequest(f.workerIdentity, &pb.PublishNativeShellRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, ObservationJson: foreignRaw})); connect.CodeOf(e) != connect.CodeFailedPrecondition {
				t.Fatalf("foreign command adopted original claim: %v", e)
			}
			raw, _ := json.Marshal(observed)
			publication, e := f.workerClient.PublishNativeShell(ctx, ownerRequest(f.workerIdentity, &pb.PublishNativeShellRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, ObservationJson: raw}))
			if e != nil {
				t.Fatal(e)
			}
			if publication.Msg.Job.Revision <= claim.Revision {
				t.Fatal("live observation did not advance revision")
			}
			if scenario == "claimed-cancel" {
				if _, e := client.CancelNativeShell(ctx, ownerRequest(f.identity, &pb.CancelNativeShellRequest{Mutation: acctMutation(publication.Msg.Job, domain.NewID())})); e != nil {
					t.Fatal(e)
				}
			}
			observed.Sequence++
			observed.CleanupVerified = true
			raw, _ = json.Marshal(observed)
			report := &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}
			if scenario == "lost-report" {
				report.OutputJson = nil
				report.Problem = &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}
			}
			reply, e := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, report))
			if e != nil {
				t.Fatal("original frozen revision rejected after live publication", e)
			}
			var done domain.Job
			domain.DecodeCompactionJob(reply.Msg.Job.DocumentJson, &done)
			if scenario == "lost-report" {
				if done.State != domain.JobUncertain {
					t.Fatal("lost report regained send authority")
				}
			} else if scenario == "claimed-cancel" {
				if done.State != domain.JobCanceled {
					t.Fatal("original canceled cleanup did not settle")
				}
			} else if done.State != domain.JobSucceeded {
				t.Fatal("original cleanup did not settle shell", done.Problem)
			}
		})
	}
}
