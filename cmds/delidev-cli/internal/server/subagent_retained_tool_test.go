// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubagentRetainedParentToolRejectsFreshNativeChildAtomically(t *testing.T) {
	f, children, sequence := claudeSubagentPublicationFixture(t)
	ctx := context.Background()
	original := children[0]
	original.ID, original.NativeID, original.Status = domain.NewID(), "earlier_child", domain.SubagentCompleted
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "subagent.retained-tool-fixture", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.SubagentKind, original.ID, 0, f.input.SessionID, "", domain.SubagentRecord{ExecutionID: domain.NewID(), Harness: domain.ClaudeCode, Version: f.input.Installation.Version, RootID: string(f.thread), Observation: original})
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	event := f.event(domain.ExecutionSubagentObserved, sequence+1)
	// The independent sibling precedes the conflicting claim in the batch.
	event.Subagents = []domain.SubagentObservation{children[1], children[0]}
	if _, err := f.call(f.requestEvent(t, event)); err != nil {
		t.Fatal("retained tool attribution blocked fresh child records", err)
	}
	after, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil || before.Revision+1 != after.Revision {
		t.Fatal("rejected historical tool claim advanced the session", err)
	}
	for _, child := range children {
		if _, err := f.service.Store.Get(ctx, domain.SubagentKind, child.ID); err != nil {
			t.Fatal("rejected batch partially published a child", err)
		}
	}
	// A distinct original tool remains independently usable after rejection.
	children[1].Status, children[1].SourceID = domain.SubagentCompleted, string(domain.NewID())
	event.Sequence++
	event.Subagents = []domain.SubagentObservation{children[1]}
	f.publish(t, event)
	originalRow, err := f.service.Store.Get(ctx, domain.SubagentKind, original.ID)
	if err != nil || originalRow.Revision != 1 {
		t.Fatal("new sibling altered retained ownership", err)
	}
}
