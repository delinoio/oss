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
	"google.golang.org/protobuf/proto"
	"net/http"
	"testing"
	"time"
)

type appsClaimFixture struct {
	*continuationFixture
	ctx         context.Context
	reader      *connect.ServerStreamForClient[pb.WatchWorkspaceReadsResponse]
	inventory   domain.NativeAppInventory
	interaction domain.ID
	request     *pb.ClaimQuestionResponseRequest
}

// A prepared original session finishes its first turn, then ordinary dispatch
// freezes the selected Apps sidecar into the next assignment. All native data
// and installations here are protocol fixtures; no native process is launched.
func newAppsClaimFixture(t *testing.T, choice string) *appsClaimFixture {
	t.Helper()
	f, ctx, reader := nativeAppsFixture(t)
	scope := domain.NativeAppsAssignmentScope(f.input)
	inventory := domain.NativeAppInventory{Scope: scope, InventoryID: domain.NewID(), Discovered: []domain.NativeAppDiscovery{{ID: "original-app", Name: "Original app", Accessible: true, Enabled: true}}, Installed: []domain.NativeAppInstalled{{ID: "original-app", Enabled: true, Callable: true}}}
	selection := domain.SessionNativeAppSelection{Scope: scope, InventoryID: inventory.InventoryID, Revision: 1, AppIDs: []string{"original-app"}}
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.apps.original-selection", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, scope.SessionID)
		if err != nil {
			return nil, err
		}
		s.NativeApps = &selection
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, "original selected app input", domain.ExecuteMode)
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	if f.input.NativeApps == nil || f.input.NativeApps.Scope != scope || f.input.NativeApps.Revision != 1 {
		t.Fatal("ordinary dispatch did not freeze original selection")
	}
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	a := &appsClaimFixture{continuationFixture: f, ctx: ctx, reader: reader, inventory: inventory, interaction: domain.NewID()}
	tool := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 3, Kind: domain.ExecutionToolStarted, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), Tool: &domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "original-native-item", Snapshot: &domain.ToolSnapshot{Kind: domain.NativeAppsTool, Status: domain.ToolRunning, Apps: &domain.NativeAppToolObservation{AppID: "original-app", Name: "Original app", ToolName: "original_read", ArgumentsPresent: true}}}}
	a.publish(t, tool)
	proof := &domain.NativeAppCallProof{Scope: scope, Selection: *f.input.NativeApps, Inventory: inventory, NativeItemID: "original-native-item", AppID: "original-app"}
	question := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 4, Kind: domain.ExecutionInteractionRequested, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), Interaction: &domain.ExecutionInteractionUpdate{ID: a.interaction, NativeItemID: proof.NativeItemID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-app-approval-request"}, Type: domain.UserQuestionInteraction, NativeApps: proof, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "mcp_tool_call_approval_" + proof.NativeItemID, Header: "Approve app tool call?", Text: "Original app read", Options: []domain.QuestionOption{{Label: "Allow", Description: "Allow original call"}, {Label: "Cancel", Description: "Cancel original call"}}}}}}}
	a.publish(t, question)
	answer := domain.QuestionResponseInput{Answers: map[string][]string{"mcp_tool_call_approval_original-native-item": {choice}}}
	raw, _ := json.Marshal(answer)
	client := delidevv1connect.NewInteractionServiceClient(http.DefaultClient, f.endpoint.URL)
	response, err := client.RespondQuestion(ctx, ownerRequest(f.identity, &pb.RespondQuestionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(a.interaction), ExpectedRevision: 1}, ResponseJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	var retained domain.ExecutionInteraction
	if domain.Decode(response.Msg.Interaction.DocumentJson, &retained) != nil || retained.Response == nil {
		t.Fatal("missing queued original response")
	}
	a.request = &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(a.interaction), ExpectedRevision: response.Msg.Interaction.Revision}, MachineId: f.machine.Id, InstanceId: f.workerInstance, JobId: f.job.Id, ResponseId: string(retained.Response.ID)}
	return a
}
func (a *appsClaimFixture) publish(t *testing.T, e domain.ExecutionEvent) {
	t.Helper()
	raw, _ := json.Marshal(e)
	if _, err := a.workerClient.PublishExecution(a.ctx, ownerRequest(a.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(a.job, domain.NewID()), MachineId: a.machine.Id, InstanceId: a.workerInstance, EventJson: raw})); err != nil {
		t.Fatal(err)
	}
}
func (a *appsClaimFixture) claim(req *pb.ClaimQuestionResponseRequest) (*connect.Response[pb.ClaimQuestionResponseResponse], error) {
	return a.workerClient.ClaimQuestionResponse(a.ctx, ownerRequest(a.workerIdentity, req))
}
func (a *appsClaimFixture) read(t *testing.T) (store.Record, domain.ExecutionInteraction) {
	t.Helper()
	r, err := a.service.Store.Get(a.ctx, domain.InteractionKind, a.interaction)
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		t.Fatal(err)
	}
	return r, v
}
func (a *appsClaimFixture) revoke(t *testing.T) {
	t.Helper()
	_, err := a.service.Store.Mutate(a.ctx, domain.NewID(), "fixture.apps.revoke", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, a.input.SessionID)
		if err != nil {
			return nil, err
		}
		selection := *s.NativeApps
		selection.Revision++
		selection.AppIDs = []string{}
		s.NativeApps = &selection
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func (a *appsClaimFixture) noObservation(t *testing.T) {
	t.Helper()
	a.service.workspaceReadsMu.Lock()
	defer a.service.workspaceReadsMu.Unlock()
	reader := a.service.workspaceReaders[a.input.MachineID]
	if reader != nil && (reader.pending != nil || len(reader.requests) != 0) {
		t.Fatal("replay/cancellation acquired another installed observation")
	}
}

type appsClaimReply struct {
	response *connect.Response[pb.ClaimQuestionResponseResponse]
	err      error
}

func (a *appsClaimFixture) beginAllow(t *testing.T) (workspace.ReadRequest, chan appsClaimReply) {
	t.Helper()
	done := make(chan appsClaimReply, 1)
	go func() { r, err := a.claim(a.request); done <- appsClaimReply{r, err} }()
	if !a.reader.Receive() {
		t.Fatal(a.reader.Err())
	}
	var read workspace.ReadRequest
	if domain.Decode(a.reader.Msg().RequestJson, &read) != nil || read.NativeApps == nil || !read.NativeApps.ForceRefresh || read.NativeApps.Scope != a.inventory.Scope || read.NativeApps.WorkerDeviceID != a.workerDevice || read.NativeApps.WorkerInstanceID != domain.ID(a.workerInstance) {
		t.Fatal("first claim lacks original fresh installed observation", read)
	}
	_, value := a.read(t)
	if value.Response.State != domain.QuestionResponseQueued || value.Response.Claim != nil {
		t.Fatal("release admitted before fresh observation")
	}
	return read, done
}
func (a *appsClaimFixture) report(t *testing.T, read workspace.ReadRequest, inventory domain.NativeAppInventory, problem string) error {
	t.Helper()
	var raw []byte
	if problem == "" {
		raw, _ = json.Marshal(inventory)
	}
	_, err := a.workerClient.ReportWorkspaceRead(a.ctx, ownerRequest(a.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: a.machine.Id, InstanceId: a.workerInstance, ReadId: string(read.ID), DocumentJson: raw, ProblemCode: problem}))
	return err
}
func (a *appsClaimFixture) reply(t *testing.T, done chan appsClaimReply) appsClaimReply {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-a.ctx.Done():
		t.Fatal("claim did not finish", a.ctx.Err())
		return appsClaimReply{}
	}
}

func TestSessionNativeAppsFirstClaimRevocationBeforeAdmission(t *testing.T) {
	a := newAppsClaimFixture(t, "Allow")
	read, done := a.beginAllow(t)
	a.revoke(t)
	if err := a.report(t, read, a.inventory, ""); err != nil {
		t.Fatal(err)
	}
	if r := a.reply(t, done); r.err == nil {
		t.Fatal("revocation during installed read admitted Allow")
	}
	_, v := a.read(t)
	if v.Response.State != domain.QuestionResponseQueued || v.Response.Claim != nil {
		t.Fatal("failed admission retained release authority")
	}
	a.noObservation(t)
}
func TestSessionNativeAppsFirstClaimAdmissionRetainedAfterRevocation(t *testing.T) {
	a := newAppsClaimFixture(t, "Allow")
	read, done := a.beginAllow(t)
	if err := a.report(t, read, a.inventory, ""); err != nil {
		t.Fatal(err)
	}
	r := a.reply(t, done)
	if r.err != nil || r.response.Msg.Replayed {
		t.Fatal("original Allow not admitted", r.err)
	}
	_, before := a.read(t)
	if before.Response.State != domain.QuestionResponseClaimed || before.Response.Claim == nil {
		t.Fatal("missing durable original claim")
	}
	a.revoke(t)
	replay, err := a.claim(a.request)
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("original admission fact lost", err)
	}
	_, after := a.read(t)
	if *after.Response.Claim != *before.Response.Claim {
		t.Fatal("replay replaced original admission")
	}
	a.noObservation(t)
	other := proto.Clone(a.request).(*pb.ClaimQuestionResponseRequest)
	other.Mutation.RequestId = string(domain.NewID())
	other.Mutation.ExpectedRevision = replay.Msg.Interaction.Revision
	if _, err := a.claim(other); err == nil {
		t.Fatal("new request borrowed claimed release")
	}
	a.noObservation(t)
}
func TestSessionNativeAppsFirstClaimCancelAfterRevocation(t *testing.T) {
	a := newAppsClaimFixture(t, "Cancel")
	a.revoke(t)
	r, err := a.claim(a.request)
	if err != nil || r.Msg.Replayed {
		t.Fatal("revoked selection blocked original Cancel", err)
	}
	_, v := a.read(t)
	admitted, err := domain.NativeAppsResponseAdmitsEffect(v)
	if err != nil || admitted || v.Response.State != domain.QuestionResponseClaimed {
		t.Fatal("Cancel acquired connector effect", err)
	}
	a.noObservation(t)
}
func TestSessionNativeAppsFirstClaimInstalledDenials(t *testing.T) {
	for _, condition := range []string{"non-callable", "refresh-failed", "foreign-inventory"} {
		t.Run(condition, func(t *testing.T) {
			a := newAppsClaimFixture(t, "Allow")
			read, done := a.beginAllow(t)
			inventory := a.inventory
			inventory.Installed = append([]domain.NativeAppInstalled(nil), inventory.Installed...)
			problem := ""
			switch condition {
			case "non-callable":
				inventory.Installed[0].Callable = false
			case "refresh-failed":
				problem = string(domain.Unavailable)
			case "foreign-inventory":
				inventory.Scope.AccountID = domain.NewID()
			}
			err := a.report(t, read, inventory, problem)
			if condition == "foreign-inventory" {
				if err == nil {
					t.Fatal("foreign report accepted")
				}
				if err := a.report(t, read, a.inventory, string(domain.Unavailable)); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if r := a.reply(t, done); r.err == nil {
				t.Fatal("unproved installed policy admitted Allow")
			}
			_, v := a.read(t)
			if v.Response.Claim != nil || v.Response.State != domain.QuestionResponseQueued {
				t.Fatal("denied observation advanced release")
			}
			a.noObservation(t)
		})
	}
}
func TestSessionNativeAppsFirstClaimUncertainAndForeignOwner(t *testing.T) {
	for _, condition := range []string{"uncertain", "replacement-worker", "replacement-account", "foreign-machine", "invalid-choice", "foreign-choice"} {
		t.Run(condition, func(t *testing.T) {
			a := newAppsClaimFixture(t, "Allow")
			if condition == "uncertain" {
				read, done := a.beginAllow(t)
				if err := a.report(t, read, a.inventory, ""); err != nil {
					t.Fatal(err)
				}
				if r := a.reply(t, done); r.err != nil {
					t.Fatal(r.err)
				}
			}
			_, err := a.service.Store.Mutate(a.ctx, domain.NewID(), "fixture.apps.owner-change", condition, func(tx *store.Tx) (any, error) {
				switch condition {
				case "uncertain":
					r, err := tx.Get(domain.JobKind, domain.ID(a.job.Id))
					if err != nil {
						return nil, err
					}
					job, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					return nil, finishLostNativeExecution(tx, r, job)
				case "replacement-worker":
					return nil, tx.SetWorkerInstance(a.input.MachineID, domain.NewID(), time.Now().UTC())
				case "replacement-account":
					r, err := tx.Get(domain.AccountKind, a.input.AccountID)
					if err != nil {
						return nil, err
					}
					account, err := store.Decode[domain.Account](r)
					if err != nil {
						return nil, err
					}
					account.Connection.ID = domain.NewID()
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, account)
				case "invalid-choice", "foreign-choice":
					r, err := tx.Get(domain.InteractionKind, a.interaction)
					if err != nil {
						return nil, err
					}
					value, err := store.Decode[domain.ExecutionInteraction](r)
					if err != nil {
						return nil, err
					}
					if condition == "foreign-choice" {
						value.Response.Input.Answers = map[string][]string{"foreign-question": {"Allow"}}
					} else {
						value.Response.Input.Answers["mcp_tool_call_approval_original-native-item"] = []string{"Allow always"}
					}
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			request := proto.Clone(a.request).(*pb.ClaimQuestionResponseRequest)
			if condition == "foreign-machine" {
				request.MachineId = string(domain.NewID())
			}
			if _, err := a.claim(request); err == nil {
				t.Fatal("uncertain/foreign original response reacquired release")
			}
			a.noObservation(t)
			if condition == "uncertain" {
				_, v := a.read(t)
				if v.Response.State != domain.QuestionResponseUncertain || v.Response.Claim == nil {
					t.Fatal("lost owner discarded uncertainty or claim fact")
				}
			}
		})
	}
}
