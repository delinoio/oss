// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestWorkspaceStorageRejectsMalformedReportedDigests(t *testing.T) {
	for _, action := range []pb.WorkspaceStorageAction{pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE, pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP} {
		for _, malformed := range []string{strings.Repeat("A", 64), strings.Repeat("z", 64), strings.Repeat("a", 63) + " "} {
			t.Run(action.String()+"/"+malformed[:1]+malformed[63:], func(t *testing.T) {
				f := newStorageFixture(t)
				previewID := ""
				if action == pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP {
					preview, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
					if err != nil {
						t.Fatal(err)
					}
					f.execute(preview.Msg.Job)
					previewID = preview.Msg.Job.Id
				}
				accepted, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(action, "", previewID, "")))
				if err != nil {
					t.Fatal(err)
				}
				claimed := f.claim(accepted.Msg.Job)
				var job domain.Job
				var input workspace.StorageRequest
				if domain.Decode(claimed.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil {
					t.Fatal("invalid storage assignment")
				}
				output, err := f.manager.Storage(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				if output.Snapshot == nil {
					output.PreviewDigest = malformed
				} else {
					output.Snapshot.SHA256 = malformed
				}
				raw, _ := json.Marshal(output)
				reported, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: claimed.Id, ExpectedRevision: claimed.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
				if err != nil || domain.Decode(reported.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobUncertain {
					t.Fatal("malformed digest settled a reported job", err)
				}
				state, err := store.Decode[domain.Session](f.sessionRecord())
				if err != nil || state.Storage.State != domain.WorkspaceStorageUncertain || state.Storage.SnapshotID != "" || state.Dispatch != domain.DispatchPaused {
					t.Fatal("malformed digest published workspace availability", err)
				}
				if input.SnapshotID != "" {
					if _, err := f.service.Store.Get(context.Background(), domain.SnapshotKind, input.SnapshotID); domain.SafeError(err).Code != domain.NotFound {
						t.Fatal("malformed snapshot metadata persisted", err)
					}
				}
				// Exact explicit recovery can still observe the original local proof;
				// invalid report bytes must not poison the recoverable copy's digest.
				recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", claimed.Id)))
				if err != nil {
					t.Fatal(err)
				}
				valid := f.execute(recovery.Msg.Job)
				if valid.Snapshot != nil && !storageDigestValid(valid.Snapshot.SHA256) {
					t.Fatal("recovery lost canonical snapshot metadata")
				}
			})
		}
	}
}
