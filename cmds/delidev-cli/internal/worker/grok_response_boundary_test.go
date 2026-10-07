package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type grokReplyFixture struct {
	*openCodeBindingRPC
	mapper        *GrokEventPublisher
	original      domain.ExecutionInteractionUpdate
	control       responseControlIdentity
	mode          string
	claims, sends int
	loseOnce      bool
}

func newGrokReplyFixture(t *testing.T, mode string) *grokReplyFixture {
	t.Helper()
	b, rpc := acceptedGrokContentFixture(t)
	mapper, err := OpenGrokEventPublisher(b)
	if err != nil {
		t.Fatal(err)
	}
	f := &grokReplyFixture{openCodeBindingRPC: rpc, mapper: mapper, mode: mode}
	b.publisher.config.Client = f
	raw, err := os.ReadFile("../harness/grok/testdata/file-tool-write.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "019f6de0-a760-7000-8000-000000000071", string(b.thread)), "e5833c4a-d764-4428-8bd8-6c2968a34b1b", b.turn))
	var rows []struct {
		Kind   string                `json:"kind"`
		Method domain.GrokToolMethod `json:"method"`
		Params json.RawMessage       `json:"params"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("fixture")
	}
	for _, row := range rows {
		v := grok.InputObservation{Kind: grok.InputFileTool, InputID: b.reference.InputRequestID, NativePromptID: b.turn, ToolEvent: &domain.GrokToolEvent{Method: row.Method}}
		if domain.Decode(row.Params, &v.ToolEvent.Payload) != nil {
			t.Fatal("original payload")
		}
		if row.Kind == "server-request" {
			arrival := domain.NewID()
			id := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-native-file"}
			v.ToolEvent.ArrivalID, v.ToolEvent.RequestID = arrival, &id
			v.ToolEvent.ProposalJSON = string(row.Params)
			digest, _ := grok.PublicRequestDigest(id)
			sum := sha256.Sum256(row.Params)
			v.Permission = &grok.FilePermissionOffer{ArrivalID: arrival, ToolID: *v.ToolEvent.Payload.Tool.ID, RequestDigest: digest, ProposalDigest: hex.EncodeToString(sum[:])}
		}
		if err := mapper.Observe(context.Background(), v); err != nil {
			t.Fatal("original publication", err)
		}
		if v.Permission != nil {
			f.original = mapper.interactions[v.Permission.ArrivalID]
			break
		}
	}
	if f.original.ID == "" {
		t.Fatal("missing permission")
	}
	f.control = responseControlIdentity{b.publisher.job, f.original.ID, domain.NewID(), 2}
	return f
}

func (f *grokReplyFixture) readJournal(want responseJournalState) grokPublicResponseJournal {
	f.t.Helper()
	b := f.mapper.binding
	path := filepath.Join(b.publisher.config.Root, "jobs", string(b.publisher.job), "grok-responses", string(f.original.ID)+".json")
	raw, err := security.ReadPrivate(path, 64<<10)
	var j grokPublicResponseJournal
	if err != nil || domain.Decode(raw, &j) != nil || j.State != want || j.Control != f.control || strings.Contains(string(raw), "decision") {
		f.t.Fatal("side effect preceded original metadata journal", err)
	}
	return j
}
func (f *grokReplyFixture) ClaimApprovalResponse(_ context.Context, request *connect.Request[pb.ClaimApprovalResponseRequest]) (*connect.Response[pb.ClaimApprovalResponseResponse], error) {
	f.claims++
	j := f.readJournal(responsePrepared)
	b := f.mapper.binding
	c := b.publisher.config
	if request.Msg.Mutation.RequestId != string(j.ClaimID) || request.Msg.InstanceId != string(c.Instance) || request.Msg.ResponseId != string(f.control.ResponseID) {
		f.t.Fatal("claim identity changed")
	}
	if f.mode == "claim-ack-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost claim receipt"))
	}
	v := domain.ExecutionInteraction{ExecutionID: b.publisher.execution, NativeThreadID: string(b.thread), NativeTurnID: b.turn, NativeItemID: f.original.NativeItemID, NativeRequestID: f.original.NativeRequestID, Type: f.original.Type, Closure: domain.InteractionOpen, Grok: f.original.Grok, ApprovalResponse: &domain.ApprovalResponse{ID: f.control.ResponseID, State: domain.ApprovalResponseClaimed, Input: domain.ApprovalResponseInput{Grok: &domain.GrokApprovalResponse{Decision: domain.GrokAllowOnce}}, Claim: &domain.ApprovalResponseClaim{ID: j.ClaimID, JobID: b.publisher.job, MachineID: c.Credential.MachineID, InstanceID: c.Instance, DeviceID: c.Credential.DeviceID}}}
	if f.mode == "foreign-claim" {
		v.ApprovalResponse.Claim.InstanceID = domain.NewID()
	}
	raw, _ := json.Marshal(v)
	return connect.NewResponse(&pb.ClaimApprovalResponseResponse{Interaction: &pb.Resource{Id: string(f.original.ID), SessionId: string(b.reference.SessionID), Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, Revision: 3, DocumentJson: raw}}), nil
}
func (f *grokReplyFixture) PublishExecution(ctx context.Context, r *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	response, err := f.openCodeBindingRPC.PublishExecution(ctx, r)
	if f.loseOnce {
		f.loseOnce = false
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost delivery receipt"))
	}
	return response, err
}
func (f *grokReplyFixture) ReplyFilePermission(ctx context.Context, response, arrival domain.ID, decision grok.FilePermissionDecision) (grok.FilePermissionDelivery, error) {
	f.readJournal(responseSendIntent)
	b := f.mapper.binding
	r := f.original.Grok
	if response != f.control.ResponseID || arrival != r.Event.ArrivalID || decision != grok.AllowFileOnce {
		f.t.Fatal("native reply replaced original decision/arrival")
	}
	body := []byte(`{"outcome":{"outcome":"selected","optionId":"allow-once"}}`)
	sum := sha256.Sum256(body)
	claim := grok.FilePermissionClaim{Version: 1, OwnerID: b.reference.JobID, ProductSessionID: b.reference.SessionID, InputRequestID: b.reference.InputRequestID, RequestID: response, NativeSessionID: b.thread, NativePromptID: b.turn, ArrivalID: arrival, ToolID: f.original.NativeItemID, RequestDigest: r.RequestDigest, ProposalDigest: r.ProposalDigest, Decision: decision, BodyDigest: hex.EncodeToString(sum[:])}
	if err := f.mapper.FileReply(ctx, claim); err != nil {
		return grok.FilePermissionDelivery{}, err
	}
	f.sends++
	if f.mode == "uncertain" {
		return grok.FilePermissionDelivery{Claimed: true, Attempted: true}, errors.New("fixture uncertain pipe")
	}
	return grok.FilePermissionDelivery{Claimed: true, Attempted: true, Delivered: true}, nil
}
func (f *grokReplyFixture) ReplyQuestion(context.Context, domain.ID, domain.ID, grok.QuestionAnswer) (grok.QuestionDelivery, error) {
	f.t.Fatal("wrong native family")
	return grok.QuestionDelivery{}, nil
}
func (f *grokReplyFixture) ReplyPlan(context.Context, domain.ID, domain.ID, grok.PlanOutcome) (grok.PlanDelivery, error) {
	f.t.Fatal("wrong native family")
	return grok.PlanDelivery{}, nil
}

func TestGrokOriginalReplyIsNotRepeatedAfterReceiptLossOrConflict(t *testing.T) {
	for _, mode := range []string{"delivered", "lost-receipt", "uncertain", "claim-ack-lost", "foreign-claim", "stop"} {
		t.Run(mode, func(t *testing.T) {
			f := newGrokReplyFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "stop" {
				cancel()
			}
			f.loseOnce = mode == "lost-receipt"
			before := len(f.events)
			err := f.mapper.deliver(ctx, context.Background(), f.control, domain.NativeApprovalInteraction, f)
			succeeded := mode == "delivered" || mode == "foreign-claim" || mode == "lost-receipt"
			if (err == nil) != succeeded {
				t.Fatal("delivery result", err)
			}
			want := 0
			if succeeded || mode == "uncertain" {
				want = 1
			}
			if f.sends != want {
				t.Fatal("native sends", f.sends)
			}
			if mode == "lost-receipt" && (len(f.events) != before+2 || f.requests[before] != f.requests[before+1] || string(f.events[before]) != string(f.events[before+1])) {
				t.Fatal("receipt retry replaced bytes/identity")
			}
			if succeeded {
				if f.mapper.deliver(ctx, ctx, f.control, domain.NativeApprovalInteraction, f) != nil || f.sends != 1 || f.claims != 1 {
					t.Fatal("identical control resent reply")
				}
				competitor := f.control
				competitor.ResponseID = domain.NewID()
				if f.mapper.deliver(ctx, ctx, competitor, domain.NativeApprovalInteraction, f) == nil || f.sends != 1 || f.claims != 1 {
					t.Fatal("competing control acquired another reply")
				}
			} else if f.mapper.deliver(ctx, context.Background(), f.control, domain.NativeApprovalInteraction, f) == nil || f.sends != want {
				t.Fatal("uncertainty/cancellation acquired another native send")
			}
		})
	}
}
