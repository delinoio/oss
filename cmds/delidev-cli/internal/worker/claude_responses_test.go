package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type claudeReplyFixtureRPC struct {
	*openCodeBindingRPC
	content  *ClaudeContentPublisher
	original domain.ExecutionInteractionUpdate
	change   string
	claims   int
	reply    *domain.ClaudePermissionResponse
}

func (f *claudeReplyFixtureRPC) ClaimApprovalResponse(_ context.Context, req *connect.Request[pb.ClaimApprovalResponseRequest]) (*connect.Response[pb.ClaimApprovalResponseResponse], error) {
	f.claims++
	c, r := f.content, req.Msg
	j := c.binding.journal
	path := filepath.Join(c.binding.publisher.config.Root, "jobs", string(j.JobID), "claude-responses", string(f.original.ID)+".json")
	var prepared claudeResponseJournal
	raw, err := security.ReadPrivate(path, 16<<10)
	if err != nil || domain.Decode(raw, &prepared) != nil || prepared.State != responsePrepared || string(prepared.ClaimID) != r.Mutation.RequestId {
		f.t.Fatal("claim RPC preceded durable ownership", err)
	}
	if f.change == "claim-loss" {
		return nil, connect.NewError(connect.CodeUnavailable, publicationUncertain())
	}
	value := domain.ExecutionInteraction{ExecutionID: j.ExecutionID, NativeThreadID: string(j.SessionID), NativeTurnID: c.binding.turn, NativeItemID: f.original.NativeItemID, NativeRequestID: f.original.NativeRequestID, Type: f.original.Type, Closure: domain.InteractionOpen, Claude: f.original.Claude, ApprovalResponse: &domain.ApprovalResponse{ID: domain.ID(r.ResponseId), State: domain.ApprovalResponseClaimed, Input: domain.ApprovalResponseInput{Claude: &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}}, AcceptedAt: time.Now().UTC(), Claim: &domain.ApprovalResponseClaim{ID: domain.ID(r.Mutation.RequestId), JobID: j.JobID, MachineID: j.MachineID, InstanceID: j.InstanceID, DeviceID: j.DeviceID, ClaimedAt: time.Now().UTC()}}}
	if f.reply != nil {
		value.ApprovalResponse.Input.Claude = f.reply
	}
	if f.change == "premature-settlement" {
		value.ClaudeSettlement = &domain.ClaudeCallbackSettlement{}
	}
	if f.change == "foreign-thread" {
		value.NativeThreadID = string(domain.NewID())
	}
	if f.change == "foreign-claim" {
		value.ApprovalResponse.Claim.InstanceID = domain.NewID()
	}
	if f.change == "mixed" {
		value.ApprovalResponse.Input.OpenCode = &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}
	}
	raw, _ = json.Marshal(value)
	return connect.NewResponse(&pb.ClaimApprovalResponseResponse{Interaction: &pb.Resource{Id: string(f.original.ID), SessionId: string(j.SessionID), Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, Revision: r.Mutation.ExpectedRevision + 1, DocumentJson: raw}}), nil
}

type claudeReplyFixtureNative struct {
	t            *testing.T
	c            *ClaudeContentPublisher
	change       string
	calls, sends int
}

func (f *claudeReplyFixtureNative) ReplyClaimed(ctx context.Context, arrival domain.ID, reply claude.PermissionReply, claim func(context.Context, claude.PermissionReplyClaim) error) error {
	f.calls++
	a := f.c.responses[arrival]
	if a == nil || string(reply.Behavior) != string(a.input.Behavior) || reply.Answers != nil || reply.Interrupt != (a.input.Interrupt != nil && *a.input.Interrupt) || a.input.Message != nil && reply.Message != *a.input.Message {
		f.t.Fatal("native reply changed original response")
	}
	original := a.journal.Native
	if f.change == "foreign-native" {
		original.InputID = domain.NewID()
	}
	if err := claim(ctx, original); err != nil {
		return err
	}
	path := filepath.Join(f.c.binding.publisher.config.Root, "jobs", string(a.journal.Control.JobID), "claude-responses", string(a.journal.Control.InteractionID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var retained claudeResponseJournal
	if err != nil || domain.Decode(raw, &retained) != nil || retained.State != responseSendIntent || retained.Native != original || bytes.Contains(raw, []byte("file_path")) {
		f.t.Fatal("native send preceded metadata-only intent", err)
	}
	f.sends++
	if f.change == "send-loss" {
		return publicationUncertain()
	}
	return nil
}
func claudeReplyFixture(t *testing.T, change string) (*ClaudeContentPublisher, *claudeReplyFixtureRPC, *claudeReplyFixtureNative, *pb.ApprovalResponseControl, claude.LifecycleObservation) {
	t.Helper()
	c, rpc, _ := claudeToolFixture(t, "")
	o := claudeInteractionObservation(c)
	if _, err := c.PublishInteractionObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	original := c.interactions[o.Interaction.ArrivalID].update
	fake := &claudeReplyFixtureRPC{openCodeBindingRPC: rpc, content: c, original: original, change: change}
	c.binding.publisher.config.Client = fake
	control := &pb.ApprovalResponseControl{JobId: string(c.binding.publisher.job), InteractionId: string(original.ID), ResponseId: string(domain.NewID()), Revision: 2}
	return c, fake, &claudeReplyFixtureNative{t: t, c: c, change: change}, control, o
}
func TestClaudeResponseDeliveryRetriesOnlyOriginalReceipts(t *testing.T) {
	c, rpc, native, control, o := claudeReplyFixture(t, "")
	ctx := context.Background()
	rpc.lose = true
	if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err == nil {
		t.Fatal("delivery acknowledgment not lost")
	}
	last := len(rpc.events) - 1
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
		t.Fatal("delivery receipt changed")
	}
	if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err != nil || native.calls != 1 || native.sends != 1 || rpc.claims != 1 {
		t.Fatal("duplicate control repeated native response", err)
	}
	o.Interaction.Kind, o.Interaction.Request = claude.InteractionReplyEchoed, nil
	o.Interaction.ReplyDigest = c.responses[o.Interaction.ArrivalID].journal.Native.BodyDigest
	rpc.lose = true
	if handled, err := c.PublishInteractionObservation(ctx, o); !handled || err == nil {
		t.Fatal("echo acknowledgment not lost", err)
	}
	last = len(rpc.events) - 1
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) || c.interactionsSettled() {
		t.Fatal("echo changed receipt or granted settlement")
	}
	if _, err := c.PublishInteractionObservation(ctx, o); err == nil {
		t.Fatal("echo repeated")
	}
}
func TestClaudeResponseFailsClosedWithoutAnotherNativeSend(t *testing.T) {
	for _, change := range []string{"claim-loss", "foreign-thread", "foreign-claim", "mixed", "foreign-native", "send-loss", "retained-journal", "premature-settlement"} {
		t.Run(change, func(t *testing.T) {
			c, rpc, native, control, o := claudeReplyFixture(t, change)
			ctx := context.Background()
			if change == "retained-journal" {
				if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err != nil {
					t.Fatal(err)
				}
				delete(c.responses, o.Interaction.ArrivalID)
			}
			if err := c.DeliverApprovalResponse(ctx, ctx, control, native); (err == nil) != (change == "foreign-claim") {
				t.Fatal("response result does not match input validation", err)
			}
			if err := c.DeliverApprovalResponse(ctx, ctx, control, native); (err == nil) != (change == "foreign-claim") {
				t.Fatal("duplicate response changed its retained outcome", err)
			}
			want := 0
			if change == "send-loss" || change == "foreign-claim" || change == "retained-journal" {
				want = 1
			}
			if native.sends != want || rpc.claims != 1 {
				t.Fatal("response replayed or foreign claim sent", native.sends, rpc.claims)
			}
		})
	}
}
