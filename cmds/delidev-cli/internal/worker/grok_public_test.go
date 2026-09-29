package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type grokPublicResponseFixture struct {
	*openCodeBindingRPC
	c             *GrokBindingPublisher
	mode          string
	sends, claims int
	lost          bool
	delivery      grok.FilePermissionDelivery
}

func (f *grokPublicResponseFixture) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	// Retain the event once, then lose just the original delivery acknowledgement.
	if bytes.Contains(req.Msg.EventJson, []byte(`"approval-delivery-observed"`)) && !f.lost {
		f.lost = true
		f.lose = true
		defer func() { f.lose = false }()
	}
	return f.openCodeBindingRPC.PublishExecution(ctx, req)
}
func (f *grokPublicResponseFixture) ClaimApprovalResponse(ctx context.Context, req *connect.Request[pb.ClaimApprovalResponseRequest]) (*connect.Response[pb.ClaimApprovalResponseResponse], error) {
	f.claims++
	if f.mode == "lost-claim" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost claim acknowledgement"))
	}
	if f.mode == "canceled-claim" {
		return nil, context.Canceled
	}
	r := f.c.interactions[domain.ID(req.Msg.Mutation.Id)]
	claim := &domain.QuestionResponseClaim{ID: domain.ID(req.Msg.Mutation.RequestId), JobID: f.c.reference.JobID, MachineID: f.c.reference.MachineID, InstanceID: f.c.reference.InstanceID, DeviceID: f.c.reference.DeviceID}
	value := domain.ExecutionInteraction{ExecutionID: f.c.reference.ExecutionID, NativeThreadID: string(f.c.thread), NativeTurnID: f.c.turn, NativeItemID: r.tool, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(r.id)}, Type: domain.NativeApprovalInteraction, Grok: &r.request, Closure: domain.InteractionOpen, ApprovalResponse: &domain.ApprovalResponse{ID: domain.ID(req.Msg.ResponseId), State: domain.ApprovalResponseClaimed, Claim: claim, Input: domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowEditsSession}}}}
	copy := *value.Grok
	value.Grok = &copy
	switch f.mode {
	case "foreign-request":
		value.NativeItemID = "foreign-tool"
	case "changed-proposal":
		value.Grok.ProposalDigest = strings.Repeat("ef", 32)
	case "foreign-claim":
		claim.InstanceID = domain.NewID()
	case "wrong-kind":
		value.Type = domain.UserQuestionInteraction
	}
	raw, _ := json.Marshal(value)
	return connect.NewResponse(&pb.ClaimApprovalResponseResponse{Interaction: &pb.Resource{Id: req.Msg.Mutation.Id, Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, SessionId: string(f.c.reference.SessionID), Revision: req.Msg.Mutation.ExpectedRevision + 1, DocumentJson: raw}}), nil
}
func (f *grokPublicResponseFixture) ReplyFilePermission(ctx context.Context, operation, arrival domain.ID, decision grok.FilePermissionDecision) (grok.FilePermissionDelivery, error) {
	if ctx.Err() != nil {
		return grok.FilePermissionDelivery{}, ctx.Err()
	}
	f.sends++
	original := f.c.interactions[arrival]
	body := `{"outcome":{"outcome":"selected","optionId":"allow-edits-session"}}`
	claim := grok.FilePermissionClaim{Version: 1, OwnerID: f.c.reference.JobID, ProductSessionID: f.c.reference.SessionID, InputRequestID: f.c.reference.InputRequestID, RequestID: operation, NativeSessionID: f.c.thread, NativePromptID: f.c.turn, ArrivalID: arrival, ToolID: original.tool, RequestDigest: original.request.RequestDigest, ProposalDigest: original.request.ProposalDigest, Decision: decision, BodyDigest: domain.GrokDigest([]byte(body))}
	if err := f.c.journal.FileReply(ctx, claim); err != nil {
		return grok.FilePermissionDelivery{}, err
	}
	f.delivery = grok.FilePermissionDelivery{Claim: claim, Claimed: true, Attempted: true, Delivered: true}
	if f.mode == "uncertain-send" {
		f.delivery.Delivered = false
		return f.delivery, errors.New("fixture lost native delivery")
	}
	return f.delivery, nil
}
func (f *grokPublicResponseFixture) InspectFilePermission(arrival domain.ID) (grok.FilePermissionDelivery, error) {
	return f.delivery, nil
}
func (f *grokPublicResponseFixture) ReplyQuestion(context.Context, domain.ID, domain.ID, grok.QuestionAnswer) (grok.QuestionDelivery, error) {
	f.t.Fatal("wrong native response family sent")
	return grok.QuestionDelivery{}, nil
}
func (f *grokPublicResponseFixture) InspectQuestion(domain.ID) (grok.QuestionDelivery, error) {
	return grok.QuestionDelivery{}, errors.New("no question")
}
func (f *grokPublicResponseFixture) ReplyPlan(context.Context, domain.ID, domain.ID, grok.PlanOutcome) (grok.PlanDelivery, error) {
	f.t.Fatal("wrong native response family sent")
	return grok.PlanDelivery{}, nil
}
func (f *grokPublicResponseFixture) InspectPlan(domain.ID) (grok.PlanDelivery, error) {
	return grok.PlanDelivery{}, errors.New("no Plan")
}
func grokPublicFileObservation(t *testing.T, c *GrokBindingPublisher, id string, phase string, index uint64) grok.InputObservation {
	t.Helper()
	v := grok.InputObservation{Kind: grok.InputFileTool, InputID: c.reference.InputRequestID, NativePromptID: c.turn}
	var fact any
	if phase == "arguments" {
		fact = map[string]any{"Delta": map[string]any{"sessionId": c.thread, "update": map[string]any{"sessionUpdate": "tool_call_delta_chunk", "tool_call_id": id, "tool_index": index, "name": "write", "arguments_delta": "original arguments"}}}
	} else {
		fact = map[string]any{"Observation": map[string]any{"ID": id, "Phase": phase, "Input": map[string]any{"Name": "write", "Path": "/fixture/original", "Content": "Original private content"}, "Meta": map[string]any{"eventId": string(c.thread) + "-10", "totalTokens": 1, "agentTimestampMs": 1, "streamStartMs": 1, "turnStartMs": 1}}}
	}
	raw, _ := json.Marshal(map[string]any{"FileTool": fact})
	if json.Unmarshal(raw, &v) != nil {
		t.Fatal("invalid fixture native observation")
	}
	return v
}
func newGrokPublicResponseFixture(t *testing.T, mode string) (*grokPublicResponseFixture, responseControlIdentity) {
	c, client := acceptedGrokContentFixture(t)
	f := &grokPublicResponseFixture{openCodeBindingRPC: client, c: c, mode: mode}
	c.publisher.config.Client = f
	for _, phase := range []string{"arguments", "declared", "described"} {
		v := grokPublicFileObservation(t, c, "original-write", phase, 0)
		if phase == "described" {
			raw, _ := json.Marshal(v)
			v = grok.InputObservation{}
			json.Unmarshal(bytes.ReplaceAll(raw, []byte("-10"), []byte("-11")), &v)
		}
		if err := c.ObservePublic(context.Background(), v, f); err != nil {
			t.Fatal(err)
		}
	}
	offer := grok.FilePermissionOffer{ArrivalID: domain.NewID(), ToolID: "original-write", RequestDigest: strings.Repeat("ab", 32), ProposalDigest: strings.Repeat("cd", 32)}
	v := grok.InputObservation{Kind: grok.InputFileTool, InputID: c.reference.InputRequestID, NativePromptID: c.turn, Permission: &offer}
	raw, _ := json.Marshal(map[string]any{"FileTool": map[string]any{"Permission": map[string]any{"Tool": map[string]any{"ID": offer.ToolID, "Input": map[string]any{"Name": "write", "Path": "/fixture/original", "Content": "Original private content"}}}}})
	json.Unmarshal(raw, &v)
	if err := c.ObservePublic(context.Background(), v, f); err != nil {
		t.Fatal(err)
	}
	return f, responseControlIdentity{JobID: c.reference.JobID, InteractionID: offer.ArrivalID, ResponseID: domain.NewID(), Revision: 2}
}
func TestGrokPublicReplyLostAcknowledgementNeverResends(t *testing.T) {
	f, control := newGrokPublicResponseFixture(t, "ready")
	if err := f.c.deliverGrokResponse(context.Background(), context.Background(), control, false, f); err != nil {
		t.Fatal(err)
	}
	if f.sends != 1 || f.claims != 1 || !f.lost {
		t.Fatal("original delivery or outbox replay missing", f.sends, f.claims)
	}
	n := len(f.events)
	if n < 2 || !bytes.Equal(f.events[n-2], f.events[n-1]) || f.requests[n-2] != f.requests[n-1] {
		t.Fatal("lost delivery acknowledgement replaced exact receipt")
	}
	if err := f.c.deliverGrokResponse(context.Background(), context.Background(), control, false, f); err != nil || f.sends != 1 || f.claims != 1 {
		t.Fatal("same control repeated original reply", err)
	}
	changed := control
	changed.ResponseID = domain.NewID()
	if f.c.deliverGrokResponse(context.Background(), context.Background(), changed, false, f) == nil || f.sends != 1 {
		t.Fatal("competing native response accepted")
	}
	original, _ := security.ReadPrivate(f.c.journal.path, maxGrokClaimBytes)
	f.delivery.Resolved = true
	json.Unmarshal([]byte(`{"ToolPhase":"completed"}`), &f.delivery)
	v := grokPublicFileObservation(t, f.c, "original-write", "completed", 0)
	raw, _ := json.Marshal(v)
	json.Unmarshal(bytes.ReplaceAll(raw, []byte("-10"), []byte("-12")), &v)
	if err := f.c.ObservePublic(context.Background(), v, f); err != nil {
		t.Fatal(err)
	}
	after, _ := security.ReadPrivate(f.c.journal.path, maxGrokClaimBytes)
	if !bytes.Equal(original, after) || !f.c.interactions[control.InteractionID].accepted || f.sends != 1 {
		t.Fatal("semantic acceptance changed native journal or repeated reply")
	}
}
func TestGrokPublicReplyRevalidatesBeforeNativeSideEffects(t *testing.T) {
	for _, mode := range []string{"foreign-request", "changed-proposal", "foreign-claim", "wrong-kind", "lost-claim", "canceled-claim", "uncertain-send", "canceled-control", "wrong-control-kind"} {
		t.Run(mode, func(t *testing.T) {
			f, control := newGrokPublicResponseFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled-control" {
				cancel()
			}
			question := mode == "wrong-control-kind"
			err := f.c.deliverGrokResponse(ctx, context.Background(), control, question, f)
			if mode == "canceled-control" {
				if err != nil || f.sends != 0 || f.claims != 0 {
					t.Fatal("canceled control acquired side effect")
				}
				return
			}
			if err == nil {
				t.Fatal("unconfirmed response acquired authority")
			}
			expected := 0
			if mode == "uncertain-send" {
				expected = 1
			}
			if f.sends != expected {
				t.Fatal("invalid scope reached native", f.sends)
			}
			before := f.sends
			_ = f.c.deliverGrokResponse(context.Background(), context.Background(), control, question, f)
			if f.sends != before {
				t.Fatal("uncertain operation acquired automatic resend")
			}
		})
	}
}
func TestGrokPublicArgumentIndicesAreReusableAfterDeclaration(t *testing.T) {
	f, _ := newGrokPublicResponseFixture(t, "ready")
	v := grokPublicFileObservation(t, f.c, "second-original-write", "arguments", 0)
	if err := f.c.ObservePublic(context.Background(), v, f); err != nil {
		t.Fatal("native response index was mistaken for global tool identity", err)
	}
	if f.c.tools["second-original-write"].id == f.c.tools["original-write"].id {
		t.Fatal("new native tool reused product record")
	}
}
