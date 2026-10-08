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
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"testing"
	"time"
)

func skillReaderFixture(t *testing.T) (*firstDispatchFixture, context.Context, *connect.ServerStreamForClient[pb.WatchWorkspaceReadsResponse]) {
	t.Helper()
	f := newFirstDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	_, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.skill-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSkillsV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if e != nil {
		t.Fatal(e)
	}
	stream, e := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { stream.Close() })
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing joined reader", stream.Err())
	}
	return f, ctx, stream
}
func TestSkillInventoryUsesOriginalWorkerActorAndKeylessContext(t *testing.T) {
	f, ctx, stream := skillReaderFixture(t)
	client := delidevv1connect.NewSkillServiceClient(http.DefaultClient, f.endpoint.URL)
	done := make(chan error, 1)
	go func() {
		r, e := client.ListSkills(ctx, ownerRequest(f.identity, &pb.ListSkillsRequest{MachineId: f.machine.Id, AgentId: f.agent.Id}))
		if e == nil && (len(r.Msg.Skills) != 1 || r.Msg.Skills[0].Selection.WorkerDeviceId != string(f.workerDevice)) {
			e = domain.Fail(domain.Internal, "invalid fixture result", "")
		}
		done <- e
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var request workspace.ReadRequest
	if domain.Decode(stream.Msg().RequestJson, &request) != nil || request.Skills == nil || request.Skills.ActorID != f.service.Identity.ServerID || request.Skills.WorkerDeviceID != f.workerDevice || request.Skills.WorkerInstanceID != domain.ID(f.workerInstance) || request.Skills.SessionID != "" || len(request.Skills.Selections) != 0 || request.Manifest.Version != 0 {
		t.Fatal("inventory acquired wrong authority", request)
	}
	result := domain.SkillReadResult{Entries: []domain.SkillEntry{{WorkerDeviceID: f.workerDevice, InventoryID: domain.NewID(), SkillID: domain.NewID(), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "add-issue", Description: "fixture", Provenance: "user"}}}
	raw, _ := json.Marshal(result)
	_, e := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(request.ID), DocumentJson: raw}))
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestSkillsRejectWorkerClientAndUnnegotiatedMachine(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	client := delidevv1connect.NewSkillServiceClient(http.DefaultClient, f.endpoint.URL)
	query := &pb.ListSkillsRequest{MachineId: f.machine.Id, AgentId: f.agent.Id}
	if _, e := client.ListSkills(ctx, ownerRequest(f.workerIdentity, query)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("Worker acquired owner inventory", e)
	}
	if _, e := client.ListSkills(ctx, ownerRequest(f.identity, query)); e == nil {
		t.Fatal("unnegotiated Worker advertised skills")
	}
}
func TestSkillSelectionRejectsMalformedAndDuplicateBindings(t *testing.T) {
	request := string(domain.NewID())
	item := &pb.SkillSelection{WorkerDeviceId: string(domain.NewID()), InventoryId: string(domain.NewID()), SkillId: string(domain.NewID()), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if _, e := skillBindings([]*pb.SkillSelection{item, item}, request); e == nil {
		t.Fatal("duplicate selected skill accepted")
	}
	if _, e := skillBindings([]*pb.SkillSelection{nil}, request); e == nil {
		t.Fatal("nil selection accepted")
	}
}

func TestSkillAcceptanceBindsOriginalRequestAndRejectsFieldUnawareEdit(t *testing.T) {
	f, ctx, stream := skillReaderFixture(t)
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.endpoint.URL)
	selection := &pb.SkillSelection{WorkerDeviceId: string(f.workerDevice), InventoryId: string(domain.NewID()), SkillId: string(domain.NewID()), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	request := &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: f.request.DocumentJson, LocalWorkerToken: f.request.LocalWorkerToken, Skills: []*pb.SkillSelection{selection}}
	result := make(chan *pb.SessionChange, 1)
	failures := make(chan error, 1)
	go func() {
		response, err := client.CreateSession(ctx, ownerRequest(f.identity, request))
		if err != nil {
			failures <- err
			return
		}
		result <- response.Msg.Change
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var read workspace.ReadRequest
	if domain.Decode(stream.Msg().RequestJson, &read) != nil || read.Skills == nil || len(read.Skills.Selections) != 1 || string(read.Skills.Selections[0].SnapshotID) != request.RequestId {
		t.Fatal("unbound acceptance", read)
	}
	raw, _ := json.Marshal(domain.SkillReadResult{Entries: []domain.SkillEntry{}})
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(read.ID), DocumentJson: raw})); err != nil {
		t.Fatal(err)
	}
	var change *pb.SessionChange
	select {
	case change = <-result:
	case err := <-failures:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var input domain.QueuedInput
	if domain.Decode(change.Input.DocumentJson, &input) != nil || len(input.Skills) != 1 || string(input.Skills[0].SnapshotID) != request.RequestId {
		t.Fatal("receipt lost selected binding", change)
	}
	replay, err := client.CreateSession(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Input.Id != change.Input.Id {
		t.Fatal("exact original acceptance was not replayed", err)
	}
	_, err = client.EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{SessionId: change.Session.Id, Prompt: "plain old-client edit", Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: change.Input.Id, ExpectedRevision: change.Input.Revision}}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("field-unaware edit dropped bound skills", err)
	}
}
