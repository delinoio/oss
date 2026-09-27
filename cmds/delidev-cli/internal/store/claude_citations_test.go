package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaudeCitationContinuationRechecksOriginalStoredMessages(t *testing.T) {
	for _, scenario := range []string{"web", "legacy-absent", "foreign-execution", "foreign-role", "child", "streaming", "block-streaming", "thinking", "initial-null", "matched", "null-history"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			session, execution, id := domain.NewID(), domain.NewID(), domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.citation-history", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{}); err != nil {
					return nil, err
				}
				h := &domain.ClaudeCitationHistory{Initial: &domain.ClaudeCitationCollection{Entries: []domain.ClaudeCitation{}}, Deltas: []domain.ClaudeCitation{{Kind: domain.ClaudeWebCitation, Text: "Original quote", Web: &domain.ClaudeWebLocation{URL: "https://fixture.invalid/source"}}}, Completed: &domain.ClaudeCitationCollection{Entries: []domain.ClaudeCitation{}}, Completion: domain.ClaudeCitationsOmitted}
				b := domain.ClaudeRetainedBlock{Index: 0, Block: domain.ClaudeTextBlock{Kind: domain.ClaudeText}, State: domain.ClaudeBlockStopped, Citations: h}
				m := domain.ExecutionMessage{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeID: "msg_original", Role: domain.AssistantMessage, State: domain.MessageComplete}
				switch scenario {
				case "legacy-absent":
					b.Citations = nil
				case "foreign-execution":
					m.ExecutionID = domain.NewID()
				case "foreign-role":
					m.Role = domain.ToolMessage
				case "child":
					m.NativeParentID = "toolu_parent"
				case "streaming":
					m.State = domain.MessageStreaming
				case "block-streaming":
					b.State = domain.ClaudeBlockStreaming
				case "thinking":
					b.Block.Kind = domain.ClaudeThinking
				case "initial-null":
					h.Initial = &domain.ClaudeCitationCollection{Null: true}
				case "matched":
					h.Completed.Entries, h.Completion = h.Deltas, domain.ClaudeCitationsMatched
				}
				m.Claude = &domain.ClaudeMessageContent{Model: "model", Blocks: []domain.ClaudeRetainedBlock{b}}
				raw, err := json.Marshal(m)
				if err != nil {
					return nil, err
				}
				if scenario == "null-history" {
					var v map[string]any
					if err := json.Unmarshal(raw, &v); err != nil {
						return nil, err
					}
					v["claude"].(map[string]any)["blocks"].([]any)[0].(map[string]any)["citations"] = nil
					raw, err = json.Marshal(v)
					if err != nil {
						return nil, err
					}
				}
				if _, err := tx.Put(domain.MessageKind, id, 0, session, "", json.RawMessage(raw)); err != nil {
					return nil, err
				}
				return nil, tx.BindExecutionMessage(session, execution, id, "thread", "turn", "msg_original", m.State)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Read(ctx, func(tx *Tx) error {
				ready, err := tx.ClaudeRootContentContinuation(execution)
				if err == nil && ready != (scenario == "web" || scenario == "legacy-absent") {
					t.Fatal("stored original citation eligibility changed", ready)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
