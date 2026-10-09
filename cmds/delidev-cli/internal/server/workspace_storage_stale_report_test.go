// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

func TestStorageStaleReportAcknowledgesOnlyCompletedOriginalRecovery(t *testing.T) {
	f := newStorageFixture(t)
	preview, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW, "", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	f.execute(preview.Msg.Job)
	cleanup, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP, "", preview.Msg.Job.Id, "")))
	if err != nil {
		t.Fatal(err)
	}
	assigned := f.claim(cleanup.Msg.Job)
	var job domain.Job
	var input workspace.StorageRequest
	if domain.Decode(assigned.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil {
		t.Fatal("missing original assignment")
	}
	output, err := f.manager.Storage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(output)
	report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}
	// The original result never reached the server. Replacement attachment must
	// retain uncertainty until a separately authorized native recovery settles it.
	_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.expired-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.machine, f.instance, time.Now().UTC().Add(-2*workerLease))
	})
	if err != nil {
		t.Fatal(err)
	}
	f.instance = domain.NewID()
	if _, err := f.worker.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, report)); err == nil {
		t.Fatal("unsettled original report bypassed replacement ownership")
	}
	recovery, err := f.client.RequestWorkspaceStorage(context.Background(), ownerRequest(f.service.Identity, f.request(pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER, "", "", assigned.Id)))
	if err != nil {
		t.Fatal(err)
	}
	f.execute(recovery.Msg.Job)
	var before store.Record
	if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
		var err error
		before, err = tx.Get(domain.JobKind, domain.ID(assigned.Id))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sessionBefore := f.sessionRecord()
	ack, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, report))
	if err != nil {
		t.Fatal("settled original was not acknowledged", err)
	}
	var accepted domain.Job
	if domain.Decode(ack.Msg.Job.DocumentJson, &accepted) != nil || accepted.StorageReconciledBy != domain.ID(recovery.Msg.Job.Id) || ack.Msg.Job.Revision != before.Revision || f.sessionRecord().Revision != sessionBefore.Revision {
		t.Fatal("reconciliation changed original state")
	}
	replay, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, report))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Revision != ack.Msg.Job.Revision {
		t.Fatal("reconciliation receipt did not replay", err)
	}
	wrong := proto.Clone(report).(*pb.ReportWorkRequest)
	wrong.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision + 1}
	if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, wrong)); err == nil {
		t.Fatal("wrong original revision gained acknowledgement")
	}
	wrong.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: assigned.Id, ExpectedRevision: assigned.Revision}
	wrong.InstanceId = string(f.instance)
	if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, wrong)); err == nil {
		t.Fatal("replacement instance gained original acknowledgement")
	}
	altered := proto.Clone(report).(*pb.ReportWorkRequest)
	altered.OutputJson = json.RawMessage(`{"different":true}`)
	if _, err := f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, altered)); err == nil {
		t.Fatal("changed original report reused a receipt")
	}
}
