// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type backgroundWorkerFixture struct {
	delidevv1connect.WorkerServiceClient
	action                        domain.NativeBackgroundAction
	claims, sends, reads, reports int
}

func (f *backgroundWorkerFixture) ClaimNativeBackground(_ context.Context, r *connect.Request[pb.ClaimNativeBackgroundRequest]) (*connect.Response[pb.ClaimNativeBackgroundResponse], error) {
	f.claims++
	a := f.action
	a.State = domain.NativeBackgroundClaimed
	a.Claim = &domain.NativeShellClaim{RequestID: domain.ID(r.Msg.Mutation.RequestId), MachineID: domain.ID(r.Msg.MachineId), InstanceID: domain.ID(r.Msg.InstanceId), DeviceID: domain.ID("01900000-0000-7000-8000-000000000001")}
	f.action = a
	raw, _ := json.Marshal(a)
	return connect.NewResponse(&pb.ClaimNativeBackgroundResponse{Action: &pb.Resource{Id: string(a.ActionID), Kind: pb.EntityKind_ENTITY_KIND_NATIVE_ACTION, Revision: 2, DocumentJson: raw}}), nil
}
func (f *backgroundWorkerFixture) PublishNativeBackground(_ context.Context, r *connect.Request[pb.PublishNativeBackgroundRequest]) (*connect.Response[pb.PublishNativeBackgroundResponse], error) {
	f.reports++
	var p domain.NativeBackgroundPublication
	if e := domain.Decode(r.Msg.ObservationJson, &p); e != nil {
		return nil, e
	}
	session := domain.Session{NativeBackground: &p.Inventory}
	raw, _ := json.Marshal(session)
	return connect.NewResponse(&pb.PublishNativeBackgroundResponse{Session: &pb.Resource{Id: r.Msg.Mutation.Id, Kind: pb.EntityKind_ENTITY_KIND_SESSION, DocumentJson: raw}}), nil
}
func (f *backgroundWorkerFixture) InspectBackground(context.Context) ([]domain.NativeBackgroundTerminal, error) {
	f.reads++
	return []domain.NativeBackgroundTerminal{}, nil
}
func (f *backgroundWorkerFixture) ControlBackground(_ context.Context, a domain.NativeBackgroundAction) (domain.NativeBackgroundObservation, error) {
	f.sends++
	return domain.NativeBackgroundObservation{ActionID: a.ActionID, ProblemCode: domain.RecoveryRequired, Delivery: domain.NativeBackgroundSendUnknown}, domain.NativeBackgroundRecovery()
}
func TestNativeBackgroundLostAckRetainsOriginalSendFenceWithoutOuterJournal(t *testing.T) {
	job, execution, session := domain.NewID(), domain.NewID(), domain.NewID()
	i := domain.NativeBackgroundInventory{Version: 1, JobID: job, ExecutionID: execution, NativeThreadID: domain.NativeIdentity(domain.NewID()), Sequence: 1, Terminals: []domain.NativeBackgroundTerminal{}, NativeEmpty: true}
	a := domain.NativeBackgroundAction{Version: 1, ActionType: domain.NativeBackgroundActionType, ActionID: domain.NewID(), SessionID: session, JobID: job, ExecutionID: execution, NativeThreadID: i.NativeThreadID, InventorySequence: 1, Operation: domain.NativeBackgroundTerminate, ItemID: "bg-one", ProcessID: "500", Targets: []domain.NativeBackgroundTerminal{{ItemID: "bg-one", ProcessID: "500", Command: "original", Cwd: "/original", Present: true}}, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.NativeBackgroundQueued}
	f := &backgroundWorkerFixture{action: a}
	root := t.TempDir()
	config := &PublicationConfig{Root: root, Client: f, Credential: Credential{ServerID: domain.NewID(), DeviceID: domain.ID("01900000-0000-7000-8000-000000000001")}, Instance: domain.NewID()}
	input := domain.ExecutionJobInput{SessionID: session, ExecutionID: execution, MachineID: domain.NewID()}
	b := &nativeBackgroundController{config: config, input: input, native: f, inventory: i}
	control := &pb.NativeBackgroundControl{JobId: string(job), ActionId: string(a.ActionID), Revision: 1}
	if e := b.deliver(context.Background(), control); e == nil {
		t.Fatal("lost ACK became confirmed delivery")
	}
	if f.claims != 1 || f.sends != 1 || f.reads != 1 || f.reports != 1 {
		t.Fatal("original read-only recovery changed request count")
	}
	if _, e := os.Lstat(filepath.Join(root, "jobs", string(job)+".json")); !os.IsNotExist(e) {
		t.Fatal("outer job journal unexpectedly supplied send authority")
	}
	raw, e := security.ReadPrivate(filepath.Join(root, "jobs", string(job), "native-background", string(a.ActionID)+".json"), 128<<10)
	var retained nativeBackgroundSend
	if e != nil || domain.Decode(raw, &retained) != nil || !retained.SendIntent || retained.Observation == nil || retained.Action.ActionID != a.ActionID || retained.Observation.Acknowledged {
		t.Fatal("send fence lost original uncertainty", e)
	}
	if e := b.deliver(context.Background(), control); e == nil || f.claims != 1 || f.sends != 1 {
		t.Fatal("lost outer journal repeated native claim/send")
	}
}
