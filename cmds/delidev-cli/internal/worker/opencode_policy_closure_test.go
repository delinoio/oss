package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func openCodePolicyClosureFixture(t *testing.T, rejected bool) (*openCodeTextFixture, *OpenCodeEventPublisher, opencode.Observation, string) {
	t.Helper()
	f, c, proposal := openCodeInteractionFixture(t, false)
	if err := c.PublishObservation(context.Background(), proposal); err != nil {
		t.Fatal(err)
	}
	original := c.interactions[proposal.Interaction.ID]
	source := original
	source.ID = domain.NewID()
	source.NativeRequestID.Text = "per_01960dcbe1fbABCDEFGHIJKLMN"
	native := *source.OpenCode
	permission := *native.Permission
	permission.Always = []string{"original/*"}
	native.Permission = &permission
	source.OpenCode = &native
	decision := opencode.PermissionAlways
	o := opencode.Observation{Kind: opencode.PermissionRepliedEvent, InteractionReply: &opencode.NativeInteractionReply{SessionID: f.input.SessionID, RequestID: original.NativeRequestID.Text, Kind: opencode.PermissionInteraction, Decision: &decision}, AlwaysObservations: []string{source.NativeRequestID.Text}}
	if rejected {
		decision, o.InteractionReply.Rejected = opencode.PermissionReject, true
		o.AlwaysObservations, o.RejectionSources = nil, []string{source.NativeRequestID.Text}
	}
	c.responses = map[string]*openCodeResponseAttempt{source.NativeRequestID.Text: {original: source, decision: domain.OpenCodePermissionDecision(decision), accepted: true}}
	c.closedInteractions = map[string]openCodeClosedInteraction{source.NativeRequestID.Text: {original: source, rejected: rejected}}
	return f, c, f.observation(o), source.NativeRequestID.Text
}

func TestOpenCodePolicyClosureDoesNotInventNativeReplyClaims(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		f, c, o, source := openCodePolicyClosureFixture(t, rejected)
		before := len(f.rpc.events)
		if err := c.PublishObservation(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		var event domain.ExecutionEvent
		if len(f.rpc.events) != before+1 || domain.Decode(f.rpc.events[before], &event) != nil || event.Kind != domain.ExecutionInteractionClosed || event.Interaction.OpenCodeClosure == nil || len(event.Interaction.OpenCodeClosure.Sources) != 1 || event.Interaction.OpenCodeClosure.Sources[0].NativeRequestID != source || len(c.interactions) != 0 || len(c.responses) != 1 || c.closedInteractions[o.InteractionReply.RequestID].correction {
			t.Fatal("automatic closure fabricated a direct response, feedback or acceptance")
		}
		claims, err := f.c.binding.readClaims()
		if err != nil || len(claims) != 2 {
			t.Fatal("native policy closure claimed another HTTP reply")
		}
		if err := c.PublishObservation(context.Background(), f.observation(o)); err == nil {
			t.Fatal("already closed native request was closed again")
		}
	}
}

func TestOpenCodePolicyClosureBlocksUnprovenContextAndUncertainPublication(t *testing.T) {
	for _, name := range []string{"no-context", "unknown-source", "unaccepted", "missing-closure", "wrong-decision", "missing-scope", "duplicate-source", "mixed-context", "wrong-rejection", "session", "lost-ack", "capacity"} {
		t.Run(name, func(t *testing.T) {
			f, c, o, source := openCodePolicyClosureFixture(t, false)
			switch name {
			case "no-context":
				o.AlwaysObservations = nil
			case "unknown-source":
				o.AlwaysObservations[0] = "per_01960dcbe1fcABCDEFGHIJKLMN"
			case "unaccepted":
				c.responses[source].accepted = false
			case "missing-closure":
				delete(c.closedInteractions, source)
			case "wrong-decision":
				c.responses[source].decision = domain.OpenCodePermissionOnce
			case "missing-scope":
				c.responses[source].original.OpenCode.Permission.Always = []string{}
			case "duplicate-source":
				o.AlwaysObservations = append(o.AlwaysObservations, source)
			case "mixed-context":
				o.RejectionSources = []string{source}
			case "wrong-rejection":
				o.InteractionReply.Rejected = true
			case "session":
				o.InteractionReply.SessionID = "ses_01960dcbe1fcABCDEFGHIJKLMN"
			case "lost-ack":
				f.rpc.lose = true
			case "capacity":
				c.text.bytes = maxOpenCodeTextBytes
			}
			if name == "session" {
				err := c.PublishObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if c.PublishObservation(context.Background(), o) == nil || !c.blocked || !c.text.blocked || len(c.interactions) != 1 {
				t.Fatal("unproven or uncertain policy closure released pending ownership")
			}
		})
	}
}

func TestOpenCodeRejectionRequiresOriginalFailedToolAndFinalAssistant(t *testing.T) {
	f, c, o, _ := openCodePolicyClosureFixture(t, true)
	if err := c.PublishObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	c.final, f.c.binding.requested.Rejection = textAssistantID, opencode.StopOnInteractionRejection
	if rejected, stopped := c.rejectionState(); rejected || stopped {
		t.Fatal("native rejection closure fabricated a failed tool")
	}
	f.c.tools[textPartOneID].latest.Status = domain.ToolFailed
	if rejected, stopped := c.rejectionState(); !rejected || !stopped {
		t.Fatal("original native rejection lost its stopped outcome")
	}
	c.final = "msg_01960dcbe1fcABCDEFGHIJKLMN"
	if rejected, stopped := c.rejectionState(); !rejected || stopped {
		t.Fatal("a preceding rejection stopped the later assistant")
	}
	c.final, f.c.binding.requested.Rejection = textAssistantID, opencode.ContinueOnInteractionRejection
	if rejected, stopped := c.rejectionState(); !rejected || stopped {
		t.Fatal("rejection bypassed the verified effective policy")
	}
}

func TestOpenCodeLateResponseControlPreservesOriginalPolicyClosure(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		for _, changed := range []string{"original", "foreign-job", "unknown-request", "wrong-kind", "uncertain-closure"} {
			t.Run(fmt.Sprintf("%t/%s", rejected, changed), func(t *testing.T) {
				f, c, o, _ := openCodePolicyClosureFixture(t, rejected)
				original := c.interactions[o.InteractionReply.RequestID]
				if err := c.PublishObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
				before := len(f.rpc.events)
				claimCount, err := f.c.binding.readClaims()
				if err != nil {
					t.Fatal(err)
				}
				identity := responseControlIdentity{JobID: f.c.binding.publisher.job, InteractionID: original.ID, ResponseID: domain.NewID(), Revision: 1}
				kind := domain.NativeApprovalInteraction
				switch changed {
				case "foreign-job":
					identity.JobID = domain.NewID()
				case "unknown-request":
					identity.InteractionID = domain.NewID()
				case "wrong-kind":
					kind = domain.UserQuestionInteraction
				case "uncertain-closure":
					c.blocked = true
				}
				err = c.deliverOpenCodeResponse(context.Background(), context.Background(), identity, kind)
				if changed == "original" {
					if err != nil || c.blocked || c.text.blocked {
						t.Fatal("late original control invalidated already published closure", err)
					}
					// Repeated metadata cannot manufacture a response or reopen the request.
					if err := c.deliverOpenCodeResponse(context.Background(), context.Background(), identity, kind); err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("foreign or uncertain control was adopted as original closure")
				}
				after, err := f.c.binding.readClaims()
				if err != nil || len(after) != len(claimCount) || len(f.rpc.events) != before || len(c.interactions) != 0 || len(c.responses) != 1 || c.closedInteractions[o.InteractionReply.RequestID].original.ID != original.ID {
					t.Fatal("late control changed original native or publication ownership")
				}
				directory := filepath.Join(f.c.binding.publisher.config.Root, "jobs", string(identity.JobID), "opencode-responses")
				if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("closed request created a new response journal")
				}
			})
		}
	}
}
