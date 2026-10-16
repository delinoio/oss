// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type namedPublicationRPC struct {
	*openCodeBindingRPC
	names [][]*pb.SubagentDisplayName
}

func (f *namedPublicationRPC) PublishExecution(ctx context.Context, request *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	f.names = append(f.names, request.Msg.SubagentNames)
	return f.openCodeBindingRPC.PublishExecution(ctx, request)
}

func TestSubagentDisplayNamesSurviveOutboxReplayWithOriginalBytes(t *testing.T) {
	p, _, _ := newOpenCodeClaimsFixture(t)
	rpc := &namedPublicationRPC{openCodeBindingRPC: &openCodeBindingRPC{t: t, publisher: p, lose: true}}
	p.config.Client = rpc
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(domain.NewID()), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "native"}
	event := domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: "thread", NativeTurnID: "turn", Subagents: []domain.SubagentObservation{child}, SubagentNames: []domain.SubagentDisplayName{{ID: child.ID, Name: "Atlas"}}}
	if err := p.Publish(context.Background(), event); err == nil {
		t.Fatal("acknowledgement loss was not reported")
	}
	// The caller-owned slice cannot change the retained exact name bytes.
	event.SubagentNames[0].Name = "Changed"
	raw, err := security.ReadPrivate(p.path, 1<<20)
	var journal publicationJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Pending == nil || len(journal.Pending.Names) != 1 || journal.Pending.Names[0].Name != "Atlas" {
		t.Fatal("durable outbox did not retain the exact display names", err)
	}
	// The schema-1 event JSON never carries a name.
	eventJSON, _ := json.Marshal(journal.Pending.Event)
	if bytes.Contains(eventJSON, []byte("Atlas")) {
		t.Fatal("name entered the closed event JSON")
	}
	requestID := journal.Pending.RequestID
	// A restarted Worker reloads the same pending request and names.
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExecutionPublisher(p.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reloaded := &namedPublicationRPC{openCodeBindingRPC: &openCodeBindingRPC{t: t, publisher: reopened}}
	reopened.config.Client = reloaded
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal("replay failed", err)
	}
	if len(reloaded.names) != 1 || len(reloaded.names[0]) != 1 || reloaded.names[0][0].Name != "Atlas" || reloaded.names[0][0].ChildId != string(child.ID) || reloaded.requests[0] != requestID {
		t.Fatal("replay changed the original request identity or name bytes")
	}
	if len(rpc.names) != 1 || len(rpc.names[0]) != 1 || rpc.names[0][0].Name != "Atlas" {
		t.Fatal("original request omitted its names")
	}
}

func TestSubagentDisplayNamesRejectForeignChildBeforeRetention(t *testing.T) {
	p, _, _ := newOpenCodeClaimsFixture(t)
	rpc := &namedPublicationRPC{openCodeBindingRPC: &openCodeBindingRPC{t: t, publisher: p}}
	p.config.Client = rpc
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(domain.NewID()), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "native"}
	event := domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: "thread", NativeTurnID: "turn", Subagents: []domain.SubagentObservation{child}, SubagentNames: []domain.SubagentDisplayName{{ID: domain.NewID(), Name: "Atlas"}}}
	if err := p.Publish(context.Background(), event); err == nil || len(rpc.names) != 0 || p.state.Pending != nil {
		t.Fatal("foreign display metadata reached the outbox or server", err)
	}
}
