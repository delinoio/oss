// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestExecutionDynamicToolDurableOriginalLifecycleAndSeparateRootOutcome(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	original := &domain.DynamicToolObservation{Tool: "fixture_tool", Arguments: json.RawMessage(`{"original":true}`)}
	e := f.toolEvent(domain.ExecutionToolStarted, 3, id, "dynamic-call")
	e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.DynamicTool, Status: domain.ToolRunning, Dynamic: original}
	f.publish(t, e)
	complete := domain.CloneDynamicTool(original)
	success := false
	complete.Success = &success
	content := []domain.DynamicToolContent{}
	complete.ContentItems = &content
	e = f.toolEvent(domain.ExecutionToolCompleted, 4, id, "dynamic-call")
	e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.DynamicTool, Status: domain.ToolCompleted, Dynamic: complete}
	complete.Tool = "foreign"
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("dynamic original tool substituted")
	}
	complete.Tool = original.Tool
	request := f.publish(t, e)
	if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
		t.Fatal("dynamic receipt replay failed", err)
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool == nil || m.Tool.Completed == nil || *m.Tool.Completed.Dynamic.Success {
		t.Fatal("durable negative outcome lost", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.Execution.Outcome == domain.ExecutionSucceeded || session.Execution.CleanupVerified {
		t.Fatal("dynamic completion fabricated root outcome/cleanup", err)
	}
}
