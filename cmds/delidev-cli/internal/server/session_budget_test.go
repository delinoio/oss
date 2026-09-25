package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func TestBudgetRPCIncompleteEvidenceExactRetryAndAcceptedWork(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	row, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: string(row.ID), ExpectedRevision: row.Revision, RequestId: string(domain.NewID())}, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "USD", Threshold: "0.0000975"}}}
	set, err := client.SetSessionBudget(ctx, ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if set.Msg.View.State != pb.BudgetState_BUDGET_STATE_ALLOW_INCOMPLETE || set.Msg.View.SelectedCurrency.KnownAmount != "" || set.Msg.View.Session.Revision != row.Revision+1 {
		t.Fatal("unknown became zero/compliant", set.Msg.View)
	}
	var before, after domain.Session
	if domain.Decode(row.Data, &before) != nil || domain.Decode(set.Msg.View.Session.DocumentJson, &after) != nil || after.ActiveExecutionID != before.ActiveExecutionID || after.Dispatch != before.Dispatch || after.PendingInputs != before.PendingInputs {
		t.Fatal("budget canceled accepted work")
	}
	priceClient := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
	if _, err = priceClient.SetModelPricing(ctx, ownerRequest(f.service.Identity, &pb.SetModelPricingRequest{Mutation: &pb.Mutation{Id: string(f.input.Configuration.ModelID), RequestId: string(domain.NewID())}, ExpectedModelRevision: 1, Basis: publicPrice("USD")})); err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	counts := reportedCounts(18)
	event := f.event(domain.ExecutionResponseUsageObserved, 3)
	event.ObservationID = domain.NewID()
	event.ResponseUsage = &domain.NativeResponseUsage{ResponseDigest: strings.Repeat("d", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing}
	f.publish(t, event)
	view, err := client.GetSessionBudget(ctx, ownerRequest(f.service.Identity, &pb.GetSessionBudgetRequest{SessionId: string(row.ID)}))
	if err != nil || view.Msg.View.State != pb.BudgetState_BUDGET_STATE_THRESHOLD_REACHED || view.Msg.View.SelectedCurrency.KnownAmount != "0.0000975" {
		t.Fatal("inclusive threshold lost", err)
	}
	// Original in-flight work can still publish after the threshold is reached.
	event.Sequence++
	event.ObservationID = domain.NewID()
	event.ResponseUsage.ResponseDigest = strings.Repeat("e", 64)
	f.publish(t, event)
	current, err := client.GetSessionBudget(ctx, ownerRequest(f.service.Identity, &pb.GetSessionBudgetRequest{SessionId: string(row.ID)}))
	if err != nil || current.Msg.View.SelectedCurrency.KnownAmount != "0.000195" {
		t.Fatal("budget interrupted accepted execution", err)
	}
	remove := &pb.SetSessionBudgetRequest{Mutation: acctMutation(current.Msg.View.Session, domain.NewID()), Change: &pb.SetSessionBudgetRequest_Remove{Remove: true}}
	removed, err := client.SetSessionBudget(ctx, ownerRequest(f.service.Identity, remove))
	if err != nil || removed.Msg.View.State != pb.BudgetState_BUDGET_STATE_DISABLED {
		t.Fatal("explicit removal", err)
	}
	replay, err := client.SetSessionBudget(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.View.Budget != nil {
		t.Fatal("old receipt reinstalled budget", err)
	}
	changed := proto.Clone(request).(*pb.SetSessionBudgetRequest)
	changed.GetBudget().Threshold = "9"
	if _, err = client.SetSessionBudget(ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed request reused", err)
	}
	for _, change := range []*pb.SetSessionBudgetRequest{{Mutation: acctMutation(removed.Msg.View.Session, domain.NewID())}, {Mutation: acctMutation(removed.Msg.View.Session, domain.NewID()), Change: &pb.SetSessionBudgetRequest_Remove{Remove: false}}} {
		if _, err = client.SetSessionBudget(ctx, ownerRequest(f.service.Identity, change)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("implicit removal", err)
		}
	}
	workerRead := connect.NewRequest(&pb.GetSessionBudgetRequest{SessionId: string(row.ID)})
	workerRead.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err = client.GetSessionBudget(ctx, workerRead); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker budget read", err)
	}
	workerWrite := connect.NewRequest(request)
	workerWrite.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err = client.SetSessionBudget(ctx, workerWrite); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker budget write", err)
	}
	paired, device := pairedQuestionClient(t, f)
	if _, err = client.GetSessionBudget(ctx, ownerRequest(paired, &pb.GetSessionBudgetRequest{SessionId: string(row.ID)})); err != nil {
		t.Fatal(err)
	}
	if _, err = client.SetSessionBudget(ctx, ownerRequest(paired, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("foreign actor replay", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err = devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err = f.service.GetSessionBudget(stale, connect.NewRequest(&pb.GetSessionBudgetRequest{SessionId: string(row.ID)})); err == nil {
		t.Fatal("revoked read")
	}
	if _, err = f.service.SetSessionBudget(stale, connect.NewRequest(request)); err == nil {
		t.Fatal("revoked write")
	}
}
func TestBudgetGatesAutomaticQueuedAndEmptyResumeWithoutConsumingInput(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	ctx := context.Background()
	client := sessionClient(f.accountFixture)
	// Retained historical evidence is seeded directly; this test exercises server
	// acceptance, not native telemetry or late Worker publication authority.
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.historical-budget-evidence", nil, func(tx *store.Tx) (any, error) {
		basis, err := tokenPricing(publicPrice("USD"))
		if err != nil {
			return nil, err
		}
		if _, err = tx.PutPricing(f.input.Configuration.ModelID, 0, domain.NewID(), basis); err != nil {
			return nil, err
		}
		counts := reportedCounts(18)
		_, _, err = tx.PutResponseUsage(domain.NewID(), domain.ResponseUsageRecord{SessionID: f.input.SessionID, ExecutionID: f.input.ExecutionID, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, ProviderID: f.input.Configuration.ProviderID, ModelID: f.input.Configuration.ModelID, Harness: domain.Codex, Version: domain.CodexProtocolVersion, ThreadID: string(f.thread), TurnID: string(f.turn), Sequence: 3, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing}})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	row := f.refresh(t)
	budget, err := client.SetSessionBudget(ctx, ownerRequest(f.identity, &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: string(row.ID), ExpectedRevision: row.Revision, RequestId: string(domain.NewID())}, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "USD", Threshold: "0.0000975"}}}))
	if err != nil {
		t.Fatal(err)
	}
	before := f.refresh(t)
	if _, err = client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(budget.Msg.View.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME})); rpc.ClientError(err).Code != domain.BudgetReached {
		t.Fatal("empty resume bypassed budget", err)
	}
	if now := f.refresh(t); now.Revision != before.Revision {
		t.Fatal("blocked empty Resume mutated session")
	}
	queued := f.enqueue(t, "retain this queued input", domain.ExecuteMode)
	if err = f.service.dispatchExecution(ctx, f.refresh(t)); domain.SafeError(err).Code != domain.BudgetReached {
		t.Fatal("automatic dispatch bypassed budget", err)
	}
	retained, err := f.service.Store.Get(ctx, domain.QueueKind, domain.ID(queued.Id))
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.Decode[domain.QueuedInput](retained)
	if err != nil || input.Delivery != domain.InputQueued || input.NativeRequestID != "" || input.ExecutionID != "" {
		t.Fatal("budget consumed input", err)
	}
	blocked := f.refresh(t)
	session, err := store.Decode[domain.Session](blocked)
	if err != nil || session.ActiveExecutionID != "" || session.PendingInputs != 1 || session.Problem == nil || session.Problem.Code != domain.BudgetReached {
		t.Fatal("blocking reason/state lost", err)
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	paused := f.refresh(t)
	if _, err = client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{Id: string(paused.ID), ExpectedRevision: paused.Revision, RequestId: string(domain.NewID())}, Action: pb.SessionAction_SESSION_ACTION_RESUME})); rpc.ClientError(err).Code != domain.BudgetReached {
		t.Fatal("queued Resume bypassed budget", err)
	}
	raised, err := client.SetSessionBudget(ctx, ownerRequest(f.identity, &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: string(paused.ID), ExpectedRevision: paused.Revision, RequestId: string(domain.NewID())}, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "USD", Threshold: "0.000097500000001"}}}))
	if err != nil || raised.Msg.View.State != pb.BudgetState_BUDGET_STATE_ALLOW_INCOMPLETE {
		t.Fatal("exact fractional raise", err)
	}
	if domain.Decode(raised.Msg.View.Session.DocumentJson, &session) != nil || session.Dispatch != domain.DispatchPaused || session.PendingInputs != 1 {
		t.Fatal("budget edit resumed paused session")
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	if f.input.InputID != domain.ID(queued.Id) || f.input.Input.Prompt != "retain this queued input" {
		t.Fatal("Resume changed pending input")
	}
}
func TestBudgetIncompleteInitialExecutionAndCreationConfiguration(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	client := sessionClient(f.accountFixture)
	row := f.refresh(t)
	result, err := client.SetSessionBudget(ctx, ownerRequest(f.identity, &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: string(row.ID), ExpectedRevision: row.Revision, RequestId: string(domain.NewID())}, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "EUR", Threshold: "1"}}}))
	if err != nil || result.Msg.View.State != pb.BudgetState_BUDGET_STATE_ALLOW_INCOMPLETE {
		t.Fatal("missing telemetry denied configuration", err)
	}
	if err = f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal("incomplete evidence blocked initial turn", err)
	}
	selection := f.selection
	selection.EstimatedCostBudget = &domain.EstimatedCostBudget{Currency: "USD", Threshold: "10.000000000000001"}
	_, created := createSessionFixture(t, f.accountFixture, selection)
	view, err := client.GetSessionBudget(ctx, ownerRequest(f.identity, &pb.GetSessionBudgetRequest{SessionId: created.Session.Id}))
	if err != nil || view.Msg.View.Budget.Threshold != "10.000000000000001" {
		t.Fatal("creation lost budget", err)
	}
}
