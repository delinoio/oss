// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func queuedTitleLaneFixture(t *testing.T) (*publicationFixture, domain.Session) {
	t.Helper()
	f := recoveredAutomaticTitleFixture(t)
	_, recovery := acceptRecovery(t, f)
	completeRecovery(t, f, recovery.ExecutionRecoveryJob)
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.TitleState != domain.TitleQueued || session.TitleJobID == "" {
		t.Fatal("title was not queued under original authority", session.TitleState, err)
	}
	return f, session
}

func TestPrimaryWorkExcludesQueuedAndAuxiliaryClaimedTitles(t *testing.T) {
	for _, auxiliary := range []bool{false, true} {
		name := "queued"
		if auxiliary {
			name = "auxiliary-claimed"
		}
		t.Run(name, func(t *testing.T) {
			f, session := queuedTitleLaneFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			worker := security.Identity{Token: f.workerToken}
			var titleRevision uint64
			if auxiliary {
				lane, err := f.client.WatchAuxiliaryWork(ctx, ownerRequest(worker, &pb.WatchAuxiliaryWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
				if err != nil {
					t.Fatal(err)
				}
				defer lane.Close()
				if !lane.Receive() || !lane.Msg().Heartbeat {
					t.Fatal("auxiliary readiness missing", lane.Err())
				}
				if !lane.Receive() || lane.Msg().Job == nil || lane.Msg().Job.Id != string(session.TitleJobID) {
					t.Fatal("original title was not exclusively assigned", lane.Err())
				}
				titleRevision = lane.Msg().Job.Revision
			}
			ordinary := domain.NewID()
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.primary-ordinary", nil, func(tx *store.Tx) (any, error) {
				return tx.PutJob(ordinary, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: f.input.MachineID, Input: []byte(`{"path":"/isolated/fixture","preferred_remote":"origin"}`), AcceptedAt: time.Now().UTC()})
			})
			if err != nil {
				t.Fatal(err)
			}
			primary, err := f.client.WatchWork(ctx, ownerRequest(worker, &pb.WatchWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
			if err != nil {
				t.Fatal(err)
			}
			defer primary.Close()
			if !primary.Receive() || !primary.Msg().Heartbeat {
				t.Fatal("primary readiness missing", primary.Err())
			}
			if !primary.Receive() || primary.Msg().Job == nil || primary.Msg().Job.Id != string(ordinary) {
				t.Fatal("primary received title instead of ordinary work", primary.Msg(), primary.Err())
			}
			titleRow, err := f.service.Store.Get(context.Background(), domain.JobKind, session.TitleJobID)
			if err != nil {
				t.Fatal(err)
			}
			title, err := store.Decode[domain.Job](titleRow)
			if err != nil {
				t.Fatal(err)
			}
			state := domain.JobQueued
			titleState := domain.TitleQueued
			if auxiliary {
				state = domain.JobClaimed
				titleState = domain.TitleRunning
				if titleRow.Revision != titleRevision || title.InstanceID != f.instance || title.AssignedDeviceID != f.device {
					t.Fatal("primary changed original auxiliary ownership")
				}
			}
			current, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			value, err := store.Decode[domain.Session](current)
			if err != nil || title.State != state || value.TitleState != titleState {
				t.Fatal("primary admitted or changed title state", err, title.State, value.TitleState)
			}
			// A blocked auxiliary inference cannot block targeted primary cancellation.
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.primary-cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(ordinary) })
			if err != nil {
				t.Fatal(err)
			}
			if !primary.Receive() || primary.Msg().CancelJobId != string(ordinary) {
				t.Fatal("primary control lane stalled behind title", primary.Msg(), primary.Err())
			}
			if !auxiliary {
				if _, err := claimTitleJob(context.Background(), f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID); err != nil {
					t.Fatal("primary exclusion prevented original auxiliary admission", err)
				}
			}
		})
	}
}

func TestAuxiliaryTitleAdmissionKeepsCapacityAndOldWorkerExclusion(t *testing.T) {
	for _, scenario := range []string{"one-per-worker", "four-per-server", "old-worker"} {
		t.Run(scenario, func(t *testing.T) {
			f, session := queuedTitleLaneFixture(t)
			if scenario == "old-worker" {
				_, err := f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Version: rpc.Version}))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				lane, err := f.client.WatchAuxiliaryWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.WatchAuxiliaryWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
				if err == nil {
					defer lane.Close()
					if lane.Receive() {
						t.Fatal("old Worker received title lane")
					}
					err = lane.Err()
				}
				if connect.CodeOf(err) != connect.CodeUnimplemented {
					t.Fatal("old Worker exclusion changed", err)
				}
				return
			}
			count := 1
			if scenario == "four-per-server" {
				count = 4
			}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.title-capacity", nil, func(tx *store.Tx) (any, error) {
				for i := 0; i < count; i++ {
					machine := f.input.MachineID
					if count == 4 {
						machine = domain.NewID()
					}
					if _, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: machine, InstanceID: domain.NewID(), AssignedDeviceID: domain.NewID(), Input: []byte(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
						return nil, err
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := claimTitleJob(context.Background(), f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID); domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatal("title capacity admission changed", err)
			}
			row, err := f.service.Store.Get(context.Background(), domain.JobKind, session.TitleJobID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](row)
			if err != nil || job.State != domain.JobQueued {
				t.Fatal("full lane consumed durable queued title", err, job.State)
			}
		})
	}
}
