package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishGrokContent(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	state := domain.GrokContentState{}
	if progress.GrokContent != nil {
		state = *progress.GrokContent
	}
	if event.Kind == domain.ExecutionGrokTextObserved {
		v := *event.GrokText
		next, err := state.ObserveText(v, event.NativeThreadID)
		if err != nil {
			return err
		}
		var message domain.ExecutionMessage
		var revision uint64
		if state.MessageID == "" {
			message = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: v.Metadata.EventID, Role: domain.AssistantMessage, State: domain.MessageStreaming, FirstSequence: event.Sequence, GrokText: &domain.GrokTextContent{ResponseOrdinal: v.ResponseOrdinal}}
		} else {
			r, err := tx.Get(domain.MessageKind, state.MessageID)
			if err != nil {
				return err
			}
			message, err = store.Decode[domain.ExecutionMessage](r)
			if err != nil || r.SessionID != sr.ID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.GrokText == nil || message.GrokText.ResponseOrdinal != v.ResponseOrdinal || len(message.GrokText.Chunks) == 0 || uint32(len(message.GrokText.Chunks)) != state.MessageChunks || message.NativeID != message.GrokText.Chunks[0].EventID || message.State != domain.MessageStreaming || uint32(len(message.Text)) != state.MessageBytes {
				return executionEventConflict()
			}
			revision = r.Revision
		}
		message.Text += v.Text
		message.LastSequence = event.Sequence
		message.GrokText.Chunks = append(message.GrokText.Chunks, v.Metadata)
		if _, err := tx.Put(domain.MessageKind, v.ID, revision, sr.ID, sr.ProjectID, message); err != nil {
			return err
		}
		if err := tx.BindExecutionMessage(sr.ID, input.ExecutionID, v.ID, event.NativeThreadID, event.NativeTurnID, message.NativeID, message.State); err != nil {
			return err
		}
		progress.GrokContent = &next
		return nil
	}
	v := *event.GrokUsage
	next, err := state.ObserveResponse(v)
	if err != nil {
		return err
	}
	if state.MessageID != "" {
		r, err := tx.Get(domain.MessageKind, state.MessageID)
		if err != nil {
			return err
		}
		message, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil || r.SessionID != sr.ID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.GrokText == nil || message.GrokText.ResponseOrdinal != v.Ordinal || len(message.GrokText.Chunks) == 0 || uint32(len(message.GrokText.Chunks)) != state.MessageChunks || message.NativeID != message.GrokText.Chunks[0].EventID || message.State != domain.MessageStreaming || uint32(len(message.Text)) != state.MessageBytes {
			return executionEventConflict()
		}
		message.State, message.LastSequence = domain.MessageComplete, event.Sequence
		if _, err := tx.Put(domain.MessageKind, r.ID, r.Revision, sr.ID, sr.ProjectID, message); err != nil {
			return err
		}
		if err := tx.BindExecutionMessage(sr.ID, input.ExecutionID, r.ID, event.NativeThreadID, event.NativeTurnID, message.NativeID, message.State); err != nil {
			return err
		}
	}
	// Ordinals are bounded and monotonically committed in the same transaction.
	// Exact receipt replay skips this mutation, so equal counts on two different
	// original responses remain distinct without inventing native response IDs.
	record := domain.GrokUsageRecord{ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Harness: input.Configuration.Harness, Version: input.Installation.Version, ThreadID: event.NativeThreadID, TurnID: event.NativeTurnID, Sequence: event.Sequence, Usage: v}
	if _, err := tx.Put(domain.UsageKind, event.ObservationID, 0, sr.ID, sr.ProjectID, record); err != nil {
		return err
	}
	progress.GrokContent, progress.LatestUsageID = &next, event.ObservationID
	return nil
}
