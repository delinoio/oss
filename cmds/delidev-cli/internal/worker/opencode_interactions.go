package worker

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (c *OpenCodeEventPublisher) publishInteractionRequest(ctx context.Context, o opencode.Observation) error {
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	claims, err := b.readClaims()
	n := o.Interaction
	if err != nil || !b.validPublicationClaims(claims) || b.stage != openCodeAccepted || c.text.blocked || n == nil || n.SessionID != b.thread || n.Tool == nil || c.interactions[n.ID].ID != "" || c.responses[n.ID] != nil || c.closedInteractions[n.ID].original.ID != "" || len(c.interactions) >= domain.MaxExecutionInteractions {
		return publicationUncertain()
	}
	part := c.text.tools[c.text.calls[n.Tool.CallID]]
	message := c.text.messages[n.Tool.MessageID]
	if part == nil || part.update.NativeParentID != n.Tool.MessageID || message == nil || message.role != domain.AssistantMessage || message.finalized || part.latest.Status != domain.ToolPending && part.latest.Status != domain.ToolRunning {
		return publicationUncertain()
	}
	r := &domain.OpenCodeInteractionRequest{Version: opencode.SupportedVersion, NativeEventID: o.EventID, NativeMessageID: n.Tool.MessageID, CallID: n.Tool.CallID}
	u := domain.ExecutionInteractionUpdate{ID: domain.NewID(), NativeItemID: part.update.NativeID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: n.ID}, OpenCode: r}
	switch n.Kind {
	case opencode.PermissionInteraction:
		if o.Kind != opencode.PermissionAskedEvent || n.Permission == nil || n.Questions != nil {
			return publicationUncertain()
		}
		u.Type = domain.NativeApprovalInteraction
		r.Permission = &domain.OpenCodePermission{Name: n.Permission.Name, Patterns: slices.Clone(n.Permission.Patterns), Always: slices.Clone(n.Permission.Always), MetadataJSON: string(n.Permission.Metadata)}
	case opencode.QuestionInteraction:
		if o.Kind != opencode.QuestionAskedEvent || n.Permission != nil || n.Questions == nil || part.latest.Kind != domain.OpenCodeBuiltinTool || part.latest.Builtin.Name != domain.OpenCodeQuestionTool {
			return publicationUncertain()
		}
		u.Type, r.Questions = domain.UserQuestionInteraction, []domain.OpenCodeQuestion{}
		for _, q := range n.Questions {
			value := domain.OpenCodeQuestion{Text: q.Text, Header: q.Header, Multiple: q.Multiple, Custom: q.Custom}
			if q.Options != nil {
				value.Options = []domain.QuestionOption{}
			}
			for _, option := range q.Options {
				value.Options = append(value.Options, domain.QuestionOption{Label: option.Label, Description: option.Description})
			}
			r.Questions = append(r.Questions, value)
		}
	default:
		return publicationUncertain()
	}
	if err := u.Validate(domain.ExecutionInteractionRequested); err != nil {
		return err
	}
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > maxOpenCodeTextBytes-c.text.bytes {
		return publicationUncertain()
	}
	var retained domain.ExecutionInteractionUpdate
	if domain.Decode(raw, &retained) != nil || retained.Validate(domain.ExecutionInteractionRequested) != nil {
		return publicationUncertain()
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionRequested, NativeThreadID: b.thread, NativeTurnID: b.turn, Interaction: &retained}); err != nil {
		return err
	}
	if c.interactions == nil {
		c.interactions = map[string]domain.ExecutionInteractionUpdate{}
	}
	c.interactions[n.ID] = retained
	c.text.bytes += len(raw)
	return nil
}
