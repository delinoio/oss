package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"strconv"
)

// ObserveContent consumes only original accepted-input text and individual
// response notifications. It cannot substitute for tool, terminal, Stop,
// closure or continuation composition. Pending receipt replay never calls Grok.
func (c *GrokBindingPublisher) ObserveContent(ctx context.Context, v grok.InputObservation) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokInputAccepted {
		return publicationUncertain()
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) != len(c.proof) || v.InputID != c.reference.InputRequestID || v.NativePromptID != c.turn {
		return c.block()
	}
	event := domain.ExecutionEvent{Sequence: c.sequence + 1, NativeThreadID: string(c.thread), NativeTurnID: c.turn}
	next := c.content
	switch v.Kind {
	case grok.InputText:
		if v.Chunk == nil || v != (grok.InputObservation{Kind: grok.InputText, InputID: v.InputID, NativePromptID: v.NativePromptID, Chunk: v.Chunk}) {
			return c.block()
		}
		chunk := *v.Chunk
		if chunk.Session != c.thread || chunk.Meta.Prompt != c.turn || chunk.Update.Kind != "agent_message_chunk" || chunk.Update.Content.Type != "text" || chunk.Meta.Type != "AgentMessageChunk" {
			return c.block()
		}
		id := c.content.MessageID
		if id == "" {
			id = domain.NewID()
		}
		m := chunk.Meta
		text := domain.GrokTextUpdate{ID: id, ResponseOrdinal: c.content.Responses + 1, Text: chunk.Update.Content.Text, Metadata: domain.GrokTextMetadata{EventID: m.Event, ChunkID: strconv.FormatUint(m.Chunk, 10), ContextTokens: strconv.FormatUint(m.ContextTokens, 10), TimestampMS: strconv.FormatUint(m.TimestampMS, 10), StreamStartMS: strconv.FormatUint(m.StreamStartMS, 10), TurnStartMS: strconv.FormatUint(m.TurnStartMS, 10)}}
		next, err = c.content.ObserveText(text, string(c.thread))
		event.Kind, event.GrokText = domain.ExecutionGrokTextObserved, &text
	case grok.InputResponse:
		if v.Response == nil || v != (grok.InputObservation{Kind: grok.InputResponse, InputID: v.InputID, NativePromptID: v.NativePromptID, Response: v.Response}) {
			return c.block()
		}
		u := *v.Response
		usage := domain.GrokResponseUsage{Ordinal: c.content.Responses + 1, Counts: domain.GrokResponseCounts{Input: strconv.FormatUint(u.Input, 10), Output: strconv.FormatUint(u.Output, 10), CachedRead: strconv.FormatUint(u.CachedRead, 10), CacheCreation: strconv.FormatUint(u.CacheCreation, 10), Reasoning: strconv.FormatUint(u.Reasoning, 10)}}
		next, err = c.content.ObserveResponse(usage)
		event.Kind, event.GrokUsage, event.ObservationID = domain.ExecutionGrokUsageObserved, &usage, domain.NewID()
	default:
		return c.block()
	}
	if err != nil {
		return c.block()
	}
	c.sequence, c.stage, c.pendingContent, c.pendingKind = event.Sequence, grokContentPending, next, event.Kind
	if err := c.publish(ctx, event); err != nil {
		return err
	}
	c.content, c.pendingContent, c.pendingKind, c.stage = next, domain.GrokContentState{}, "", grokInputAccepted
	return nil
}
