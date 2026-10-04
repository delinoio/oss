// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestNativeCompactionLargeOriginalAssignmentSettlesWithoutTruncation(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			f := newFirstDispatchFixtureForHarness(t, harness, domain.ExecuteMode)
			prompt := strings.Repeat(`"`, domain.MaxPromptBytes)
			if _, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id, Prompt: prompt})); err != nil {
				t.Fatal("valid original input rejected", err)
			}
			c := &continuationFixture{firstDispatchFixture: f, thread: domain.NewID()}
			if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			c.claim(t)
			if harness == domain.OpenCode {
				c.thread, c.turn = "ses_01960dcbe1faabcdefghijklmn", "msg_01960dcbe1faABCDEFGHIJKLMN"
				c.completeOpenCode(t, domain.ExecutionSucceeded)
			} else {
				c.complete(t, domain.ExecutionSucceeded)
			}
			f.workerStream.Close()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.large-compaction-capability", nil, func(tx *store.Tx) (any, error) {
				r, m, err := activeMachine(tx, f.selection.MachineID)
				if err != nil {
					return nil, err
				}
				capability := domain.CodexSessionCompactionV1
				if harness == domain.OpenCode {
					capability = domain.OpenCodeSessionCompactionV1
				}
				m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSessionCompactionV1, capability)
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			req := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID())}
			r, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, req))
			if err != nil || len(r.Msg.Job.DocumentJson) <= 1<<20 {
				t.Fatal("large action admission", err)
			}
			var job domain.Job
			var input domain.SessionCompactionInput
			if domain.DecodeCompactionJob(r.Msg.Job.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil || input.Assignment.Input.Prompt != prompt || input.Restore.Input.Prompt != prompt {
				t.Fatal("action lost original or restore evidence")
			}
			replay, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, req))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != r.Msg.Job.Id {
				t.Fatal("large action acceptance replay", err)
			}
			claimed := claimQueuedCompaction(t, f, r.Msg.Job)
			output := publicCompactionResult(input, domain.ID(claimed.Id), false)
			raw, _ := json.Marshal(output)
			// Controlled native result fixture exercises real authenticated
			// settlement; it does not establish native history/account acceptance.
			_, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
			if err != nil {
				t.Fatal("large action settlement was stranded", err)
			}
		})
	}
}

func claimQueuedCompaction(t *testing.T, f *firstDispatchFixture, resource *pb.Resource) *pb.Resource {
	t.Helper()
	var claimed store.Record
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.large-compaction-claim", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, domain.ID(resource.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
		claimed, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return resourceForTest(claimed)
}
