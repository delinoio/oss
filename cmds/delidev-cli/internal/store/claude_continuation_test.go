package store

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaudeContinuationRetainsOriginalReadAndRejectsOtherProfiles(t *testing.T) {
	for _, scenario := range []string{"read", "read-error", "missing-result", "missing-metadata", "non-executed", "foreign-provider", "foreign-tool", "foreign-role", "unsupported-tool", "pending-tool", "missing-proposal", "invalid-proposal", "callback"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			session, execution, messageID, toolID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.original-read", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{}); err != nil {
					return nil, err
				}
				message := domain.ExecutionMessage{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeID: "msg_original", Role: domain.AssistantMessage, State: domain.MessageComplete, Claude: &domain.ClaudeMessageContent{}}
				if _, err := tx.Put(domain.MessageKind, messageID, 0, session, "", message); err != nil {
					return nil, err
				}
				if err := tx.BindExecutionMessage(session, execution, messageID, "thread", "turn", "msg_original", domain.MessageComplete); err != nil {
					return nil, err
				}
				metadata, failed := `{"type":"text"}`, false
				tool := &domain.ClaudeToolContent{Reference: domain.ClaudeToolReference{ID: toolID, NativeID: "toolu_original", Name: "Read"}, MessageID: messageID, NativeMessageID: "msg_original", Proposal: &domain.ClaudeToolProposal{Proposed: `{}`, Applied: `{}`}, Result: &domain.ClaudeToolResult{NativeEventID: string(domain.NewID()), Error: &failed, Structured: &metadata}}
				value := domain.ExecutionMessage{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeID: "toolu_original", NativeParentID: "msg_original", Role: domain.ToolMessage, State: domain.MessageComplete, ClaudeTool: tool}
				switch scenario {
				case "read-error":
					failed, metadata = true, `"Original read error"`
				case "missing-result":
					tool.Result = nil
				case "missing-metadata":
					tool.Result.Structured = nil
				case "non-executed":
					failed = true
					tool.Result.NonExecution = &domain.ClaudeToolNonExecution{NativeID: "toolu_original", Kind: domain.ClaudeUserRejectedNonExecution}
				case "foreign-provider":
					value.NativeParentID = "another_provider"
				case "foreign-tool":
					value.NativeID = "another_tool"
				case "foreign-role":
					value.Role = domain.AssistantMessage
				case "unsupported-tool":
					tool.Reference.Name = "Agent"
				case "pending-tool":
					value.State = domain.MessageStreaming
				case "missing-proposal":
					tool.Proposal = nil
				case "invalid-proposal":
					tool.Proposal.Applied = `[]`
				case "callback":
					id := domain.NewID()
					if _, err := tx.Put(domain.InteractionKind, id, 0, session, "", domain.ExecutionInteraction{}); err != nil {
						return nil, err
					}
					if err := tx.BindExecutionInteraction(session, execution, id, "thread", domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-callback"}, 1); err != nil {
						return nil, err
					}
				}
				if _, err := tx.Put(domain.MessageKind, toolID, 0, session, "", value); err != nil {
					return nil, err
				}
				return nil, tx.BindExecutionMessage(session, execution, toolID, "thread", "turn", "toolu_original", value.State)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Read(ctx, func(tx *Tx) error {
				eligible, err := tx.ClaudeRootContentContinuation(execution)
				if err == nil && eligible != (scenario == "read" || scenario == "read-error") {
					t.Fatal("continuation changed original Read classification", eligible)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
