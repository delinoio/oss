// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type appControlFixture struct {
	delidevv1connect.WorkerServiceClient
	t                               *testing.T
	mapper                          *CodexEventPublisher
	control                         *pb.CodexAppsControl
	operation                       domain.CodexAppsOperation
	mode                            string
	claims, reads, revokes, reports int
}

func newAppControlFixture(t *testing.T, mode string) *appControlFixture {
	q := newQuestionControllerFixture(t, "ready")
	f := &appControlFixture{t: t, mapper: q.mapper, mode: mode}
	f.mapper.publisher.config.Client = f
	input := &f.mapper.publisher.input
	input.CodexApps = &domain.CodexAppConfiguration{Version: 1, SessionID: input.SessionID, AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original-app"}}
	f.control = &pb.CodexAppsControl{ExecutionJobId: string(f.mapper.publisher.job), AppsOperationId: string(domain.NewID()), Revision: 1}
	return f
}
func (f *appControlFixture) journal(state responseJournalState) appsControlJournal {
	f.t.Helper()
	p := filepath.Join(f.mapper.publisher.config.Root, "jobs", f.control.ExecutionJobId, "codex-apps", f.control.AppsOperationId+".json")
	raw, err := security.ReadPrivate(p, 4<<20)
	var j appsControlJournal
	if err != nil || domain.Decode(raw, &j) != nil || j.State != state {
		f.t.Fatal("side effect preceded original durable journal", err)
	}
	return j
}
func appControlResource(v domain.CodexAppsOperation) *pb.Resource {
	raw, _ := json.Marshal(v)
	return &pb.Resource{Id: string(v.ID), SessionId: string(v.Original.SessionID), Revision: v.Revision, SchemaVersion: 1, DocumentJson: raw}
}
func (f *appControlFixture) ClaimCodexAppsControl(_ context.Context, r *connect.Request[pb.ClaimCodexAppsControlRequest]) (*connect.Response[pb.ClaimCodexAppsControlResponse], error) {
	f.claims++
	j := f.journal(responsePrepared)
	if r.Msg.Mutation.RequestId != string(j.ClaimID) {
		f.t.Fatal("claim replaced immutable request")
	}
	if f.mode == "claim-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture acknowledgment lost"))
	}
	c := f.mapper.publisher.config
	f.operation = domain.CodexAppsOperation{Version: 1, ID: domain.ID(f.control.AppsOperationId), Revision: 2, RequestID: domain.NewID(), ActorID: domain.NewID(), ClaimID: j.ClaimID, Action: domain.CodexAppsInspect, State: domain.CodexAppsClaimed, Original: f.mapper.publisher.input.CodexApps.Clone(), ExecutionID: f.mapper.publisher.execution, ExecutionJobID: f.mapper.publisher.job, MachineID: c.Credential.MachineID, InstanceID: c.Instance, NativeThreadID: domain.NativeIdentity(f.mapper.thread)}
	if f.mode == "foreign" {
		f.operation.Original.AccountID = domain.NewID()
	}
	if f.mode == "revoke" {
		f.operation.Action = domain.CodexAppsRevoke
		next := f.operation.Original.Clone()
		next.Generation = f.operation.RequestID
		next.AppIDs = []string{}
		f.operation.Next = &next
	}
	raw, _ := json.Marshal(f.operation)
	return connect.NewResponse(&pb.ClaimCodexAppsControlResponse{Operation: appControlResource(f.operation), InputJson: raw, Replayed: f.mode == "replayed"}), nil
}
func (f *appControlFixture) ReadCodexApps(context.Context, string) ([]domain.CodexApp, error) {
	f.reads++
	f.journal(responseSendIntent)
	return []domain.CodexApp{{ID: "original-app", Name: "Original", Discovered: true, Installed: true, Accessible: true, Enabled: f.mode != "revoke", Callable: f.mode != "revoke", Selected: f.mode != "revoke"}}, nil
}
func (f *appControlFixture) RevokeCodexApps(_ context.Context, id domain.ID, next domain.CodexAppConfiguration, _ string) error {
	f.revokes++
	f.journal(responseSendIntent)
	if id != f.operation.RequestID || next.Generation != id || len(next.AppIDs) != 0 {
		f.t.Fatal("native revocation changed original request")
	}
	return nil
}
func (f *appControlFixture) ReportCodexAppsControlResult(_ context.Context, r *connect.Request[pb.ReportCodexAppsControlResultRequest]) (*connect.Response[pb.ReportCodexAppsControlResultResponse], error) {
	f.reports++
	j := f.journal(responseObserved)
	if r.Msg.Mutation.RequestId != string(j.ReportID) || r.Msg.Mutation.ExpectedRevision != f.operation.Revision {
		f.t.Fatal("report replaced original receipt")
	}
	var inventory domain.CodexAppsInventory
	if domain.Decode(r.Msg.OutputJson, &inventory) != nil || inventory.Validate() != nil || inventory.OperationID != f.operation.ID || inventory.ClaimID != f.operation.ClaimID {
		f.t.Fatal("missing original refresh proof")
	}
	f.operation.State = domain.CodexAppsSucceeded
	f.operation.Revision++
	f.operation.Inventory = &inventory
	return connect.NewResponse(&pb.ReportCodexAppsControlResultResponse{Operation: appControlResource(f.operation)}), nil
}
func TestCodexAppsControlFencesLostReplayAndForeignClaimsBeforeNative(t *testing.T) {
	for _, mode := range []string{"claim-lost", "replayed", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			f := newAppControlFixture(t, mode)
			for attempt := 0; attempt < 2; attempt++ {
				if f.mapper.deliverCodexAppsControl(context.Background(), context.Background(), f.control, f, t.TempDir()) == nil {
					t.Fatal("uncertain claim accepted")
				}
			}
			if f.claims != 1 || f.reads != 0 || f.revokes != 0 || f.reports != 0 {
				t.Fatal("retained original claim was replayed or granted native work", f)
			}
		})
	}
}
func TestCodexAppsControlReportsOriginalRefreshOnce(t *testing.T) {
	for _, mode := range []string{"inspect", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			f := newAppControlFixture(t, mode)
			if err := f.mapper.deliverCodexAppsControl(context.Background(), context.Background(), f.control, f, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if f.claims != 1 || f.reads != 1 || f.reports != 1 || f.revokes != map[bool]int{true: 1, false: 0}[mode == "revoke"] {
				t.Fatal("original operation was not completed once")
			}
			if f.mapper.deliverCodexAppsControl(context.Background(), context.Background(), f.control, f, t.TempDir()) == nil || f.claims != 1 || f.reads != 1 {
				t.Fatal("completed original native control replayed")
			}
		})
	}
}
