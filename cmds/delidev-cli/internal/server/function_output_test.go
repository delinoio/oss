// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func functionOutputPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	f := newPublicationFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.function-output-capability", nil, func(tx *store.Tx) (any, error) {
		row, value, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		value.WorkerCapabilities = append(value.WorkerCapabilities, domain.CodexFunctionOutputsV1)
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestFunctionOutputLostReceiptRetainsOneOriginalMessageWithoutTurnAuthority(t *testing.T) {
	f := functionOutputPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	output := &domain.FunctionOutput{Name: "tool", Structured: true, Content: []domain.FunctionOutputContent{{Kind: domain.FunctionText, Text: functionText("inert")}, {Kind: domain.FunctionEncrypted}}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.FunctionOutputEvent, ItemID: "original-output", FunctionOutput: output, FunctionOutputStage: domain.FunctionOutputStarted})
	e := codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "original-output", Correlated: true, FunctionOutput: output, FunctionOutputStage: domain.FunctionOutputCompleted}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost receipt not retained")
	}
	if _, err := mapper.PublishCore(context.Background(), e); err == nil {
		t.Fatal("uncertainty allowed retransmission")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 5 || client.calls[3] != client.calls[4] {
		t.Fatal("replay changed exact request")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("result replay duplicated transcript", err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.FunctionOutput == nil || m.Role != domain.ToolMessage || m.Tool != nil || m.Text != "" || m.State != domain.MessageComplete || m.FirstSequence != 3 || m.LastSequence != 4 || m.NativeID != "original-output" || m.NativeTurnID != string(f.turn) {
		t.Fatal("original result identity/lifecycle lost", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.Execution.Outcome == domain.ExecutionSucceeded || session.Execution.CleanupVerified {
		t.Fatal("result fabricated success or cleanup")
	}
}
func functionText(value string) *string { return &value }
func TestFunctionOutputServerRequiresCapabilityAndExactOriginalPayload(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	e := f.event(domain.ExecutionFunctionOutputObserved, 3)
	e.FunctionOutput = &domain.FunctionOutputUpdate{ID: domain.NewID(), NativeID: "output", Stage: domain.FunctionOutputCompleted, Output: domain.FunctionOutput{Name: "tool", Text: functionText("inert")}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("unnegotiated capability published typed result")
	}
	f = functionOutputPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	e = f.event(domain.ExecutionFunctionOutputObserved, 3)
	id := domain.NewID()
	e.FunctionOutput = &domain.FunctionOutputUpdate{ID: id, NativeID: "output", Stage: domain.FunctionOutputStarted, Output: domain.FunctionOutput{Name: "tool", Structured: true, Content: []domain.FunctionOutputContent{}}}
	f.publish(t, e)
	e = f.event(domain.ExecutionFunctionOutputObserved, 4)
	e.FunctionOutput = &domain.FunctionOutputUpdate{ID: id, NativeID: "output", Stage: domain.FunctionOutputCompleted, Output: domain.FunctionOutput{Name: "substitution", Structured: true}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("completion substituted name")
	}
	e.FunctionOutput.Output.Name = "tool"
	request := f.publish(t, e)
	if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
		t.Fatal("exact result receipt lost", err)
	}
	e = f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](row)
	if err != nil || s.Execution.Outcome != domain.ExecutionSucceeded || s.Execution.CleanupVerified {
		t.Fatal("independent turn result changed")
	}
}
