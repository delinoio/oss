// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// These fixtures exercise the publication transaction. They do not establish
// native account support or replace the execution admission and lease checks.
func TestCodexWebSearchServerKeepsOriginalOrderedHistoryAndReceipt(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		t.Run(string(mode), func(t *testing.T) {
			f := newPublicationFixture(t)
			input := f.input
			input.Configuration.Subscription = true
			input.Configuration.SubscriptionService = domain.SubscriptionChatGPT
			input.Configuration.Options.WebSearch = mode
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.web-search-capability", nil, func(tx *store.Tx) (any, error) {
				r, machine, err := activeMachine(tx, input.MachineID)
				if err != nil {
					return nil, err
				}
				machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexWebSearchV1)
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			result := `[{"original":9007199254740993,"source":"원본"}]`
			for index, action := range []domain.CodexWebSearchActionKind{domain.CodexWebSearchActionSearch, domain.CodexWebSearchActionOpen, domain.CodexWebSearchActionFind} {
				id := domain.NewID()
				first := f.toolEvent(domain.ExecutionToolStarted, uint64(3+index*2), id, fmt.Sprintf("original-web-%d", index))
				first.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.WebSearchTool, Status: domain.ToolRunning, WebSearch: &domain.CodexWebSearchObservation{Query: "Original", Action: &domain.CodexWebSearchAction{Type: action}, Mode: mode, Source: "openai"}}
				publish := func(request domain.ID, event domain.ExecutionEvent) (store.Result, error) {
					return f.service.Store.Mutate(context.Background(), request, "fixture.web-search-publication", event, func(tx *store.Tx) (any, error) {
						r, err := tx.Get(domain.SessionKind, input.SessionID)
						if err != nil {
							return nil, err
						}
						if err := event.Validate(); err != nil {
							return nil, err
						}
						return nil, publishExecutionTool(tx, input, r, event)
					})
				}
				if _, err := publish(domain.NewID(), first); err != nil {
					t.Fatal(err)
				}
				last := f.toolEvent(domain.ExecutionToolCompleted, first.Sequence+1, id, first.Tool.NativeID)
				observed := *first.Tool.Snapshot.WebSearch
				observed.Results = &result
				last.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.WebSearchTool, Status: domain.ToolCompleted, WebSearch: &observed}
				foreign := last
				foreign.NativeTurnID = string(domain.NewID())
				if _, err := publish(domain.NewID(), foreign); err == nil {
					t.Fatal("foreign turn replaced original web history")
				}
				request := domain.NewID()
				if _, err := publish(request, last); err != nil {
					t.Fatal(err)
				}
				replay, err := publish(request, last)
				if err != nil || !replay.Replayed {
					t.Fatal("exact lost-receipt recovery failed", err)
				}
				if _, err := publish(domain.NewID(), last); err == nil {
					t.Fatal("another request duplicated a completed observation")
				}
				row, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
				if err != nil {
					t.Fatal(err)
				}
				retained, err := store.Decode[domain.ExecutionMessage](row)
				if err != nil || retained.Tool == nil || retained.Tool.Completed == nil || retained.FirstSequence != first.Sequence || retained.LastSequence != last.Sequence || retained.NativeTurnID != first.NativeTurnID || retained.Tool.Started.WebSearch.Action.Type != action || retained.Tool.Completed.WebSearch.Mode != mode || retained.Tool.Completed.WebSearch.Source != "openai" || *retained.Tool.Completed.WebSearch.Results != result {
					t.Fatal("original search action, provenance, sequence or result changed", err)
				}
			}
			rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: input.SessionID, Limit: 10})
			if err != nil || len(rows) != 3 {
				t.Fatal("receipt replay duplicated native history", err)
			}
		})
	}
}
