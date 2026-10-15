// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type displayNameRPC struct {
	delidevv1connect.WorkerServiceClient
	lose     bool
	requests []*pb.PublishExecutionRequest
}

func (f *displayNameRPC) PublishExecution(_ context.Context, request *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	f.requests = append(f.requests, request.Msg)
	if f.lose {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost acknowledgement"))
	}
	var event domain.ExecutionEvent
	if err := domain.Decode(request.Msg.EventJson, &event); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: event.Sequence}), nil
}

func displayNameBatch() ([]domain.SubagentObservation, domain.ID) {
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(domain.NewID()), Status: domain.SubagentRunning, Source: domain.CodexCollaborationSource, SourceID: "spawn"}
	return []domain.SubagentObservation{child}, child.ID
}

func TestDisplayNamesTravelBesideEventJSONAndSurviveOutboxReplay(t *testing.T) {
	c, _ := newClaudeBindingFixture(t, domain.ExecuteMode)
	p := c.publisher
	rpc := &displayNameRPC{lose: true}
	p.config.Client = rpc
	batch, id := displayNameBatch()
	event := domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: batch[0].ParentID, NativeTurnID: "turn", Subagents: batch, ChildNames: []domain.SubagentDisplayName{{ChildID: id, Name: "Atlas"}}}
	if err := p.Publish(context.Background(), event); err == nil || p.state.Pending == nil {
		t.Fatal("lost acknowledgement did not retain the pending outbox")
	}
	first := rpc.requests[0]
	if len(first.ChildDisplayNames) != 1 || first.ChildDisplayNames[0].ChildId != string(id) || first.ChildDisplayNames[0].Name != "Atlas" {
		t.Fatal("publication did not carry typed display metadata")
	}
	if string(first.EventJson) == "" || contains(first.EventJson, "Atlas") {
		t.Fatal("display name leaked into the closed event JSON")
	}
	// A restarted Worker reopens the durable outbox and replays identical bytes.
	config := p.config
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExecutionPublisher(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	rpc.lose = false
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := rpc.requests[1]
	if second.Mutation.RequestId != first.Mutation.RequestId || string(second.EventJson) != string(first.EventJson) || len(second.ChildDisplayNames) != 1 || second.ChildDisplayNames[0].Name != "Atlas" || second.ChildDisplayNames[0].ChildId != string(id) {
		t.Fatal("outbox replay changed the original request or dropped display metadata")
	}
}

func TestPublicationRejectsInvalidDisplayMetadataAndNamelessEventsStayUnchanged(t *testing.T) {
	c, _ := newClaudeBindingFixture(t, domain.ExecuteMode)
	p := c.publisher
	rpc := &displayNameRPC{}
	p.config.Client = rpc
	batch, id := displayNameBatch()
	for name, names := range map[string][]domain.SubagentDisplayName{
		"foreign":   {{ChildID: domain.NewID(), Name: "Atlas"}},
		"duplicate": {{ChildID: id, Name: "Atlas"}, {ChildID: id, Name: "Nova"}},
		"blank":     {{ChildID: id, Name: " "}},
	} {
		event := domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: batch[0].ParentID, NativeTurnID: "turn", Subagents: batch, ChildNames: names}
		if err := p.Publish(context.Background(), event); err == nil || len(rpc.requests) != 0 || p.state.Pending != nil {
			t.Fatalf("%s display metadata reached the outbox", name)
		}
	}
	event := domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: batch[0].ParentID, NativeTurnID: "turn", Subagents: batch}
	if err := p.Publish(context.Background(), event); err != nil || len(rpc.requests) != 1 || len(rpc.requests[0].ChildDisplayNames) != 0 {
		t.Fatal("nameless publication changed", err)
	}
	other := domain.ExecutionEvent{Kind: domain.ExecutionInputAccepted, NativeThreadID: batch[0].ParentID, NativeTurnID: "turn", ChildNames: []domain.SubagentDisplayName{{ChildID: id, Name: "Atlas"}}}
	if err := p.Publish(context.Background(), other); err == nil {
		t.Fatal("display metadata attached to another event kind")
	}
}

func contains(raw []byte, text string) bool { return strings.Contains(string(raw), text) }

func TestCodexSubagentEventPublishesNativeNamesWithItsBatch(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	rpc := &displayNameRPC{}
	c.publisher.config.Client = rpc
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(c.thread), Source: domain.CodexCollaborationSource, SourceID: "spawn", Status: domain.SubagentRunning}
	event := codex.Event{Kind: codex.SubagentEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Subagents: []domain.SubagentObservation{child}, SubagentNames: []domain.SubagentDisplayName{{ChildID: child.ID, Name: "Atlas"}}}
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
		t.Fatal("named child rejected", err)
	}
	if len(rpc.requests) != 1 || len(rpc.requests[0].ChildDisplayNames) != 1 || rpc.requests[0].ChildDisplayNames[0].Name != "Atlas" || rpc.requests[0].ChildDisplayNames[0].ChildId != string(child.ID) {
		t.Fatal("Codex nickname did not reach the publication request")
	}
}
