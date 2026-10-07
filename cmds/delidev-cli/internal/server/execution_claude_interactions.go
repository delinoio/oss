package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func validateClaudeInteractionTool(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	r := event.Interaction.Claude
	if input.Configuration.Harness != domain.ClaudeCode || (input.Version != 4 && input.Installation.Version != r.Version) {
		return executionEventConflict()
	}
	row, err := tx.Get(domain.MessageKind, r.Tool.ID)
	if err != nil {
		return err
	}
	value, err := store.Decode[domain.ExecutionMessage](row)
	if err != nil {
		return err
	}
	tool := value.ClaudeTool
	if row.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != r.Tool.NativeID || value.NativeParentID != r.NativeMessageID || value.Role != domain.ToolMessage || value.State != domain.MessageStreaming || tool == nil || tool.Reference != r.Tool || tool.MessageID != r.MessageID || tool.NativeMessageID != r.NativeMessageID || tool.Index != r.Index || tool.Proposal == nil || tool.Result != nil || (tool.Caller == nil) != (r.Caller == nil) || tool.Caller != nil && *tool.Caller != *r.Caller || !domain.EqualClaudeToolInput(tool.Proposal.Applied, r.InputJSON) {
		return executionEventConflict()
	}
	exists, err := tx.HasClaudeInteractionConflict(session.ID, input.ExecutionID, r.ArrivalID, r.Tool.NativeID)
	if err != nil {
		return err
	}
	if exists {
		return executionEventConflict()
	}
	return nil
}
