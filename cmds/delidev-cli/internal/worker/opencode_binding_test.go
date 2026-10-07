package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type openCodeBindingRPC struct {
	delidevv1connect.WorkerServiceClient
	t         *testing.T
	publisher *ExecutionPublisher
	lose      bool
	requests  []domain.ID
	events    [][]byte
}

func (f *openCodeBindingRPC) PublishExecution(_ context.Context, request *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	f.t.Helper()
	var event domain.ExecutionEvent
	if domain.Decode(request.Msg.EventJson, &event) != nil || event.Validate() != nil || request.Msg.Mutation.Id != string(f.publisher.job) || request.Msg.InstanceId != string(f.publisher.config.Instance) || request.Msg.MachineId != string(f.publisher.config.Credential.MachineID) {
		f.t.Fatal("binding publication changed original authority or event shape")
	}
	raw, err := security.ReadPrivate(f.publisher.path, 1<<20)
	var journal publicationJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Pending == nil || string(journal.Pending.RequestID) != request.Msg.Mutation.RequestId {
		f.t.Fatal("binding publication preceded its synchronized exact outbox request")
	}
	retained, _ := json.Marshal(journal.Pending.Event)
	if !bytes.Equal(retained, request.Msg.EventJson) {
		f.t.Fatal("binding outbox changed its original event bytes")
	}
	f.requests = append(f.requests, domain.ID(request.Msg.Mutation.RequestId))
	f.events = append(f.events, bytes.Clone(request.Msg.EventJson))
	if f.lose {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost publication acknowledgement"))
	}
	return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: event.Sequence}), nil
}

func newOpenCodeBindingFixture(t *testing.T) (*OpenCodeBindingPublisher, *openCodeClaimJournal, []opencode.SessionClaim, *openCodeBindingRPC) {
	t.Helper()
	p, journal, claims := newOpenCodeClaimsFixture(t)
	rpc := &openCodeBindingRPC{t: t, publisher: p}
	p.config.Client = rpc
	c, err := newOpenCodeBindingPublisher(p, journal)
	if err != nil {
		t.Fatal(err)
	}
	return c, journal, claims, rpc
}

func openCodeBindingObservation(c *OpenCodeBindingPublisher) domain.ObservedExecutionSettings {
	return domain.ObservedExecutionSettings{Model: c.publisher.input.Configuration.NativeModel, Permission: domain.PermissionDefault, OpenCodeAgent: domain.OpenCodeBuildAgent}
}

func openCodeBindingReceipt(claim opencode.SessionClaim) opencode.InputReceipt {
	return opencode.InputReceipt{RequestID: claim.RequestID, SessionID: claim.SessionID, MessageID: claim.MessageID, PartID: claim.PartID, Recorded: true}
}

func TestOpenCodeBindingCoordinatorBlocksInputUntilOriginalPublicationAcknowledged(t *testing.T) {
	c, journal, claims, rpc := newOpenCodeBindingFixture(t)
	ctx := context.Background()
	if err := c.Claim(ctx, claims[0]); err != nil {
		t.Fatal(err)
	}
	if c.Claim(ctx, claims[1]) == nil || len(journal.state.Claims) != 1 {
		t.Fatal("unbound original session gained native input authority")
	}
	rpc.lose = true
	if c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)) == nil || c.Claim(ctx, claims[1]) == nil || len(journal.state.Claims) != 1 {
		t.Fatal("uncertain session publication authorized a native input claim")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Claim(ctx, claims[1]); err != nil {
		t.Fatal(err)
	}
	if err := c.AcceptInput(ctx, openCodeBindingReceipt(claims[1])); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.Claim(ctx, claims[3]) == nil || c.ReplayPending(ctx) == nil || c.AcceptInput(ctx, openCodeBindingReceipt(claims[1])) == nil || c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)) == nil {
		t.Fatal("closed native publication coordinator regained authority")
	}
	if err := c.Close(); err != nil {
		t.Fatal("coordinator closure was not idempotent", err)
	}
	if another, err := OpenOpenCodeBindingPublisher(c.publisher); err == nil {
		_ = another.Close()
		t.Fatal("retained native claims were reopened as fresh publication authority")
	}
}

func TestOpenCodePublicationRejectsClosedOrFailedLiveJournal(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "failed"}[failed], func(t *testing.T) {
			c, journal, claims, rpc := newOpenCodeBindingFixture(t)
			ctx := context.Background()
			if err := journal.Claim(ctx, claims[0]); err != nil {
				t.Fatal(err)
			}
			if failed {
				wrong := claims[1]
				wrong.BodyDigest = claims[0].BodyDigest
				if journal.Claim(ctx, wrong) == nil || !journal.failed {
					t.Fatal("changed original input did not latch its journal failure")
				}
			} else if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			if c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)) == nil || len(rpc.requests) != 0 {
				t.Fatal("unchanged retained bytes erased original journal uncertainty")
			}
		})
	}
}

func TestOpenCodeBindingAndAcceptanceUseOriginalOutboxAndStorage(t *testing.T) {
	c, journal, claims, rpc := newOpenCodeBindingFixture(t)
	ctx := context.Background()
	if err := journal.Claim(ctx, claims[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)); err != nil {
		t.Fatal(err)
	}
	if err := journal.Claim(ctx, claims[1]); err != nil {
		t.Fatal(err)
	}
	receipt := openCodeBindingReceipt(claims[1])
	if err := c.AcceptInput(ctx, receipt); err != nil || receipt.HTTPAccepted || c.stage != openCodeAccepted || c.publisher.state.LastSequence != 2 || len(rpc.requests) != 2 {
		t.Fatal("original storage did not establish acceptance independently of lost HTTP")
	}
	if c.AcceptInput(ctx, receipt) == nil || c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)) == nil || len(rpc.requests) != 2 {
		t.Fatal("repeated binding/input observation published a duplicate")
	}
	for i, kind := range []domain.ExecutionEventKind{domain.ExecutionThreadBound, domain.ExecutionInputAccepted} {
		var event domain.ExecutionEvent
		if domain.Decode(rpc.events[i], &event) != nil || event.Kind != kind || event.NativeThreadID != receipt.SessionID || (i == 1 && event.NativeTurnID != receipt.MessageID) {
			t.Fatal("normalized binding replaced original native identity")
		}
	}
}

func TestOpenCodeBindingLostAcknowledgmentReplaysOnlyOriginalPublication(t *testing.T) {
	for _, acceptance := range []bool{false, true} {
		t.Run(map[bool]string{false: "binding", true: "acceptance"}[acceptance], func(t *testing.T) {
			c, journal, claims, rpc := newOpenCodeBindingFixture(t)
			ctx := context.Background()
			for _, claim := range claims[:2] {
				if err := journal.Claim(ctx, claim); err != nil {
					t.Fatal(err)
				}
			}
			rpc.lose = !acceptance
			err := c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c))
			if acceptance {
				if err != nil {
					t.Fatal(err)
				}
				rpc.lose = true
				err = c.AcceptInput(ctx, openCodeBindingReceipt(claims[1]))
			}
			if err == nil || c.publisher.state.Pending == nil || c.pendingRequest.Validate() != nil {
				t.Fatal("lost acknowledgement did not retain original publication")
			}
			prior, _ := security.ReadPrivate(journal.path, maxOpenCodeClaimBytes)
			last := len(rpc.requests) - 1
			rpc.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			after, _ := security.ReadPrivate(journal.path, maxOpenCodeClaimBytes)
			if !bytes.Equal(prior, after) || rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) || c.publisher.state.Pending != nil {
				t.Fatal("outbox replay changed native claims or original publication identity")
			}
			if c.ReplayPending(ctx) == nil || len(rpc.requests) != last+2 {
				t.Fatal("settled publication gained another replay")
			}
		})
	}
}

func TestOpenCodeBindingRequiresRecordedExactOriginalInput(t *testing.T) {
	for index, change := range []func(*opencode.InputReceipt){
		func(r *opencode.InputReceipt) { r.Recorded, r.HTTPAccepted = false, true },
		func(r *opencode.InputReceipt) { r.RequestID = domain.NewID() },
		func(r *opencode.InputReceipt) { r.SessionID = "ses_01960dcbe1fbabcdefghijklmn" },
		func(r *opencode.InputReceipt) { r.MessageID = "msg_01960dcbe1fbABCDEFGHIJKLMN" },
		func(r *opencode.InputReceipt) { r.PartID = "prt_01960dcbe1fb1234567890ABCD" },
	} {
		c, journal, claims, rpc := newOpenCodeBindingFixture(t)
		ctx := context.Background()
		for _, claim := range claims[:2] {
			if err := journal.Claim(ctx, claim); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)); err != nil {
			t.Fatal(err)
		}
		receipt := openCodeBindingReceipt(claims[1])
		change(&receipt)
		if index == 2 {
			if err := c.AcceptInput(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if c.AcceptInput(ctx, receipt) == nil || c.stage != openCodeBindingBlocked || len(rpc.requests) != 1 || c.publisher.state.Pending != nil {
			t.Fatal("foreign or unconfirmed native input gained acceptance")
		}
	}
}

func TestOpenCodeBindingReplayRejectsChangedOriginalEvidence(t *testing.T) {
	for _, change := range []string{"request", "observed-model", "native-claims"} {
		t.Run(change, func(t *testing.T) {
			c, journal, claims, rpc := newOpenCodeBindingFixture(t)
			ctx := context.Background()
			if err := journal.Claim(ctx, claims[0]); err != nil {
				t.Fatal(err)
			}
			rpc.lose = true
			if c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)) == nil {
				t.Fatal("expected lost acknowledgement")
			}
			switch change {
			case "request":
				c.publisher.state.Pending.RequestID = domain.NewID()
			case "observed-model":
				c.publisher.state.Pending.Event.Observed.Model = "foreign"
			case "native-claims":
				if err := os.WriteFile(journal.path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if c.ReplayPending(ctx) == nil || c.stage != openCodeBindingBlocked || len(rpc.requests) != 1 {
				t.Fatal("changed original evidence gained another publication")
			}
		})
	}
}

func TestOpenCodeBindingRequiresOriginalClaimsAndMode(t *testing.T) {
	for _, change := range []string{"missing-claim", "request", "session", "mode", "foreign-journal"} {
		t.Run(change, func(t *testing.T) {
			c, journal, claims, rpc := newOpenCodeBindingFixture(t)
			if change == "foreign-journal" {
				_, other, _ := newOpenCodeClaimsFixture(t)
				if _, err := newOpenCodeBindingPublisher(c.publisher, other); err == nil {
					t.Fatal("foreign journal was adopted")
				}
				return
			}
			if change != "missing-claim" {
				for _, claim := range claims[:2] {
					if err := journal.Claim(context.Background(), claim); err != nil {
						t.Fatal(err)
					}
				}
			}
			request, session, observation := claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)
			switch change {
			case "request":
				request = domain.NewID()
			case "session":
				session = "ses_01960dcbe1fbabcdefghijklmn"
			case "mode":
				observation.OpenCodeAgent = domain.OpenCodePlanAgent
			}
			if change == "session" {
				if err := c.BindSession(context.Background(), request, session, observation); err != nil {
					t.Fatal(err)
				}
				return
			}
			if c.BindSession(context.Background(), request, session, observation) == nil || c.stage != openCodeBindingBlocked || len(rpc.requests) != 0 || c.publisher.state.Pending != nil {
				t.Fatal("unowned native binding gained publication")
			}
		})
	}
}

func TestOpenCodeBindingCannotAdoptChangedCanonicalNativeClaims(t *testing.T) {
	for _, change := range []string{"creation-digest", "input-digest", "removed-input"} {
		t.Run(change, func(t *testing.T) {
			c, journal, claims, rpc := newOpenCodeBindingFixture(t)
			ctx := context.Background()
			for _, claim := range claims[:2] {
				if err := journal.Claim(ctx, claim); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.BindSession(ctx, claims[0].RequestID, claims[1].SessionID, openCodeBindingObservation(c)); err != nil {
				t.Fatal(err)
			}
			if change == "removed-input" {
				rpc.lose = true
				if c.AcceptInput(ctx, openCodeBindingReceipt(claims[1])) == nil {
					t.Fatal("expected lost acceptance acknowledgement")
				}
			}
			state := journal.state
			state.Claims = append([]opencode.SessionClaim(nil), state.Claims...)
			switch change {
			case "creation-digest":
				state.Claims[0].BodyDigest = claims[1].BodyDigest
			case "input-digest":
				state.Claims[1].BodyDigest = claims[0].BodyDigest
			case "removed-input":
				state.Claims = state.Claims[:1]
			}
			raw, _ := json.Marshal(state)
			if err := security.WriteAtomic(journal.path, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := readOpenCodeClaims(c.publisher.config.Root, c.reference); err != nil {
				t.Fatal("test must retain structurally valid canonical native metadata")
			}
			before := len(rpc.requests)
			var err error
			if change == "removed-input" {
				err = c.ReplayPending(ctx)
			} else {
				err = c.AcceptInput(ctx, openCodeBindingReceipt(claims[1]))
			}
			if err == nil || c.stage != openCodeBindingBlocked || len(rpc.requests) != before {
				t.Fatal("altered canonical native metadata gained acceptance/replay")
			}
		})
	}
}
