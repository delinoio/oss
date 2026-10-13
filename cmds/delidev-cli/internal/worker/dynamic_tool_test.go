// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureDynamicResponder struct {
	t        *testing.T
	root     string
	calls    int
	delivery domain.DynamicReplyDelivery
	lost     bool
}

func (f *fixtureDynamicResponder) DynamicRequestID(context.Context, domain.ID) (json.RawMessage, error) {
	return json.RawMessage("7"), nil
}
func (f *fixtureDynamicResponder) ReplyDynamicUnavailable(ctx context.Context, token, turn, claim domain.ID, commit func() error) (domain.DynamicToolReply, error) {
	f.calls++
	prepared, err := os.ReadFile(filepath.Join(f.root, "reply.json"))
	if err != nil || !strings.Contains(string(prepared), `"state":"prepared"`) {
		f.t.Fatal("reply preceded durable ownership", err)
	}
	if err := commit(); err != nil {
		f.t.Fatal(err)
	}
	intent, _ := os.ReadFile(filepath.Join(f.root, "reply.json"))
	if !strings.Contains(string(intent), `"state":"send-intent"`) {
		f.t.Fatal("wire preceded durable send intent")
	}
	reply := domain.DynamicToolReply{Token: token, ClaimID: claim, Delivery: f.delivery, Unavailable: true}
	if f.lost {
		return reply, publicationUncertain()
	}
	return reply, nil
}
func dynamicWorkerFixture(t *testing.T) (*CodexEventPublisher, *openCodeBindingRPC, codex.Event) {
	t.Helper()
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.NativeDynamicTools = true
	identity := domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	event := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicStarted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}}
	if _, err := c.PublishCore(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicRequested, Request: &domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "7", Identity: identity}}
	if _, err := c.PublishCore(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	return c, rpc, event
}
func TestDynamicDurableOwnerBeforeWireAndRestartNeverResends(t *testing.T) {
	for _, delivery := range []domain.DynamicReplyDelivery{domain.DynamicTransmitted, domain.DynamicUncertain, domain.DynamicNotSent} {
		t.Run(string(delivery), func(t *testing.T) {
			c, _, event := dynamicWorkerFixture(t)
			root := filepath.Join(c.publisher.config.Root, "jobs", string(c.publisher.job), "dynamic-replies", "by-call", dynamicJournalKey(event.ItemID))
			client := &fixtureDynamicResponder{t: t, root: root, delivery: delivery, lost: delivery == domain.DynamicUncertain}
			err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client)
			if delivery == domain.DynamicUncertain && err == nil || delivery != domain.DynamicUncertain && err != nil || client.calls != 1 {
				t.Fatal("delivery boundary lost", err)
			}
			raw, err := os.ReadFile(filepath.Join(root, "reply.json"))
			var original dynamicReplyJournal
			if err != nil || domain.Decode(raw, &original) != nil || original.Delivery != delivery || original.Request.Token != event.DynamicTool.Request.Token || original.SessionID != c.publisher.input.SessionID || original.ExecutionID != c.publisher.execution || original.CallID != event.ItemID {
				t.Fatal("original journal lost", err)
			}
			if _, err := inspectDynamicReplyJournal(filepath.Join(root, "reply.json"), original); err != nil {
				t.Fatal(err)
			}
			// Simulate a recreated arrival token with the same original call/request.
			retained := c.dynamicTools[event.ItemID]
			retained.Tool.Reply = nil
			retained.Tool.Request.Token = domain.NewID()
			c.dynamicTools[event.ItemID] = retained
			event.DynamicTool.Request.Token = retained.Tool.Request.Token
			if err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client); err == nil || client.calls != 1 {
				t.Fatal("restored intent repeated reply")
			}
			original.ExecutionID = domain.NewID()
			if _, err := inspectDynamicReplyJournal(filepath.Join(root, "reply.json"), original); err == nil {
				t.Fatal("foreign journal acquired authority")
			}
		})
	}
}
func TestDynamicObservedJournalRecoversPublicationWithoutResending(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.NativeDynamicTools = true
	event := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true}
	identity := domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	request := domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "7", Identity: identity}
	if _, err := c.PublishCore(context.Background(), codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: event.ItemID, Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicRequested, Request: &request}}); err != nil {
		t.Fatal(err)
	}
	event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicRequested, Request: &request}
	config := c.publisher.config
	directory := filepath.Join(config.Root, "jobs", string(c.publisher.job), "dynamic-replies")
	byCall, byRequest := filepath.Join(directory, "by-call"), filepath.Join(directory, "by-request")
	if security.PrivateDir(directory) != nil || security.PrivateDir(byCall) != nil || security.PrivateDir(byRequest) != nil {
		t.Fatal("could not prepare private journal fixture")
	}
	requestAttempt := filepath.Join(byRequest, dynamicJournalKey(string(request.IDKind)+":"+request.IDValue)+".json")
	attempt := filepath.Join(byCall, dynamicJournalKey(event.ItemID))
	if security.CreatePrivateDirExclusive(attempt) != nil {
		t.Fatal("could not prepare exclusive journal ownership")
	}
	journal := dynamicReplyJournal{WireID: json.RawMessage("7"), Version: 1, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, MachineID: config.Credential.MachineID, InstanceID: config.Instance, JobID: c.publisher.job, Revision: c.publisher.state.Revision, AssignmentDigest: c.publisher.state.AssignmentDigest, SessionID: c.publisher.input.SessionID, ExecutionID: c.publisher.execution, ThreadID: c.thread, TurnID: c.turn, CallID: event.ItemID, Request: request, ClaimID: domain.NewID(), State: responseObserved, Delivery: domain.DynamicTransmitted}
	owner := journal
	owner.State, owner.Delivery = responsePrepared, ""
	if err := claimDynamicReplyOwner(requestAttempt, owner); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(attempt, "reply.json")
	if err := writeJSON(path, journal); err != nil {
		t.Fatal(err)
	}
	before := len(rpc.events)
	client := &fixtureDynamicResponder{t: t, root: attempt}
	if err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client); err != nil || client.calls != 0 || len(rpc.events) != before+1 {
		t.Fatal("observed reply was not reconciled without another send", err)
	}
	if got := c.dynamicTools[event.ItemID].Tool.Reply; got == nil || got.ClaimID != journal.ClaimID || got.Delivery != domain.DynamicTransmitted {
		t.Fatal("recovered publication did not retain the observed reply")
	}
}
func TestDynamicNativeCompletionAndFailedTurnDoNotSettleRequest(t *testing.T) {
	c, _, event := dynamicWorkerFixture(t)
	identity := event.DynamicTool.Request.Identity
	event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicCompleted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "completed"}}
	if _, err := c.PublishCore(context.Background(), event); err != nil || c.dynamicToolsSettled() {
		t.Fatal("completion settled request", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnFailed}}
	if _, err := c.PublishCore(context.Background(), terminal); err != nil || !c.finished || c.dynamicToolsSettled() {
		t.Fatal("root failure erased original request", err)
	}
}
func TestDynamicLateLifecycleSettlesBeforeTerminalPublication(t *testing.T) {
	c, rpc, _ := dynamicWorkerFixture(t)
	request := c.dynamicTools["dynamic-call"].Tool.Request
	attempt := filepath.Join(c.publisher.config.Root, "jobs", string(c.publisher.job), "dynamic-replies", "by-call", dynamicJournalKey("dynamic-call"))
	client := &fixtureDynamicResponder{t: t, root: attempt, delivery: domain.DynamicTransmitted}
	requested := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicRequested, Request: request}}
	if err := c.deliverDynamicUnavailable(context.Background(), context.Background(), requested, client); err != nil || client.calls != 1 {
		t.Fatal("dynamic request reply was not retained", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	before := len(rpc.events)
	// The execution reader retains this terminal without publishing it until
	// bounded lifecycle draining observes both completion and resolution.
	if c.dynamicToolsSettled() {
		t.Fatal("open request unexpectedly settled before the terminal event")
	}
	identity := c.dynamicTools["dynamic-call"].Tool.Request.Identity
	completed := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true, Late: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicCompleted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "completed"}}}
	if handled, err := c.PublishCore(context.Background(), completed); !handled || err != nil {
		t.Fatal("late native completion was not drained", err)
	}
	resolved := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "dynamic-call", Correlated: true, Late: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicResolved, Token: c.dynamicTools["dynamic-call"].Tool.Request.Token}}
	if handled, err := c.PublishCore(context.Background(), resolved); !handled || err != nil || !c.dynamicToolsSettled() {
		t.Fatal("late resolution did not settle the dynamic request", err)
	}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+3 {
		t.Fatal("terminal did not publish after lifecycle reconciliation", err)
	}
}
func TestDynamicCanceledControllerDoesNotSend(t *testing.T) {
	c, _, event := dynamicWorkerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fixtureDynamicResponder{t: t}
	if err := c.deliverDynamicUnavailable(ctx, context.Background(), event, client); err == nil || client.calls != 0 {
		t.Fatal("canceled owner replied")
	}
}

func TestDynamicCrashAtClaimAndSendBoundariesRetainsInspectableOwner(t *testing.T) {
	for _, state := range []string{"owner-only", "empty-call-directory", "prepared", "send-intent", "observed"} {
		t.Run(state, func(t *testing.T) {
			c, rpc, event := dynamicWorkerFixture(t)
			config := c.publisher.config
			directory := filepath.Join(config.Root, "jobs", string(c.publisher.job), "dynamic-replies")
			byCall, byRequest := filepath.Join(directory, "by-call"), filepath.Join(directory, "by-request")
			for _, path := range []string{directory, byCall, byRequest} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			request := *event.DynamicTool.Request
			index := filepath.Join(byRequest, dynamicJournalKey(string(request.IDKind)+":"+request.IDValue)+".json")
			attempt := filepath.Join(byCall, dynamicJournalKey(event.ItemID))
			journal := dynamicReplyJournal{WireID: json.RawMessage("7"), Version: 1, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, MachineID: config.Credential.MachineID, InstanceID: config.Instance, JobID: c.publisher.job, Revision: c.publisher.state.Revision, AssignmentDigest: c.publisher.state.AssignmentDigest, SessionID: c.publisher.input.SessionID, ExecutionID: c.publisher.execution, ThreadID: c.thread, TurnID: c.turn, CallID: event.ItemID, Request: request, ClaimID: domain.NewID(), State: responsePrepared}
			if err := claimDynamicReplyOwner(index, journal); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectDynamicReplyJournal(index, journal); err != nil {
				t.Fatal("first claim is not inspectable", err)
			}
			foreign := journal
			foreign.ClaimID = domain.NewID()
			if err := claimDynamicReplyOwner(index, foreign); err == nil {
				t.Fatal("first owner replaced")
			}
			if state != "owner-only" {
				if err := security.CreatePrivateDirExclusive(attempt); err != nil {
					t.Fatal(err)
				}
			}
			if state != "owner-only" && state != "empty-call-directory" {
				if state == "send-intent" {
					journal.State = responseSendIntent
				}
				if state == "observed" {
					journal.State, journal.Delivery = responseObserved, domain.DynamicTransmitted
				}
				if err := writeJSON(filepath.Join(attempt, "reply.json"), journal); err != nil {
					t.Fatal(err)
				}
			}
			before := len(rpc.events)
			client := &fixtureDynamicResponder{t: t, root: attempt}
			err := c.deliverDynamicUnavailable(context.Background(), context.Background(), event, client)
			if client.calls != 0 {
				t.Fatal("restored claim resent a native reply")
			}
			if state == "observed" {
				if err != nil || len(rpc.events) != before+1 {
					t.Fatal("observed fact before outbox did not replay original publication", err)
				}
			} else if err == nil || len(rpc.events) != before {
				t.Fatal("unobserved claim gained publication or send authority", err)
			}
		})
	}
}
