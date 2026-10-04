// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func TestWorkspaceStorageReportPreservesAcceptedSnapshotMetadata(t *testing.T) {
	for _, field := range []string{"unchanged", "size", "created-at"} {
		t.Run(field, func(t *testing.T) {
			f := newStorageFixture(t)
			create, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE, "", "", "")))
			if err != nil {
				t.Fatal(err)
			}
			created := f.execute(create.Msg.Job)
			inspect, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_INSPECT, string(created.Snapshot.ID), "", "")))
			if err != nil {
				t.Fatal(err)
			}
			assigned := f.claim(inspect.Msg.Job)
			var job domain.Job
			var input workspace.StorageRequest
			if domain.Decode(assigned.DocumentJson, &job) != nil || workspace.DecodeStorageRequest(job.Input, &input) != nil || input.SnapshotMetadata == nil {
				t.Fatal("missing accepted metadata")
			}
			output, err := f.manager.Storage(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if field == "size" {
				output.Snapshot.SizeBytes++
			}
			if field == "created-at" {
				output.Snapshot.CreatedAt = output.Snapshot.CreatedAt.Add(time.Second)
			}
			raw, _ := json.Marshal(output)
			reported, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			if domain.Decode(reported.Msg.Job.DocumentJson, &job) != nil {
				t.Fatal("invalid report")
			}
			want := domain.JobUncertain
			if field == "unchanged" {
				want = domain.JobSucceeded
			}
			if job.State != want {
				t.Fatal("report rewrote accepted metadata", job.State)
			}
			err = f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
				record, err := tx.Get(domain.SnapshotKind, created.Snapshot.ID)
				if err != nil {
					return err
				}
				snapshot, err := store.Decode[workspace.SnapshotMetadata](record)
				if err != nil {
					return err
				}
				if snapshot.SizeBytes != created.Snapshot.SizeBytes || !snapshot.CreatedAt.Equal(created.Snapshot.CreatedAt) {
					t.Fatal("accepted snapshot record changed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// A failed restore/delete recovery validates the same original pinned
			// metadata even though its observation is projected as inspection.
			input.Action = workspace.StorageRestore
			recovery := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover, Preparation: input.Preparation, Manifest: input.Manifest, Recovery: &workspace.StorageRecovery{Original: input}}
			output.OperationID, output.Action, output.RecoveredJobID, output.RecoveredJobState = recovery.OperationID, recovery.Action, input.OperationID, domain.JobFailed
			raw, _ = json.Marshal(output)
			err = validateWorkspaceStorageResult(recovery, raw)
			if (err == nil) != (field == "unchanged") {
				t.Fatal("failed recovery bypassed pinned metadata", err)
			}
		})
	}
}
