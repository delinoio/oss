package server

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishGrokStop(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.GrokStop
	if v == nil || v.Validate(event.NativeThreadID) != nil || v.InputID != input.InputID || v.InputRequestID != input.TurnRequestID || v.Model != input.Configuration.NativeModel || event.Outcome != v.Outcome() || p.GrokStop != nil || p.GrokToolsTerminal != nil || p.GrokTerminal != nil || input.Input.Mode != domain.ExecuteMode || input.Continuation != nil || p.UnconfirmedResponses != 0 || v.RequestID == input.ThreadRequestID || v.RequestID == input.SessionID || v.RequestID == p.JobID {
		return executionEventConflict()
	}
	canceled, err := tx.JobCancellationRequested(p.JobID)
	if err != nil {
		return err
	}
	if !canceled {
		return executionEventConflict()
	}
	if v.Kind == domain.GrokInterruptedBeforeText {
		if p.GrokContent != nil || p.LatestUsageID != "" {
			return executionEventConflict()
		}
		complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
		if err != nil {
			return err
		}
		if !complete {
			return executionEventConflict()
		}
		copy := *v
		p.GrokStop = &copy
		return nil
	}
	if p.GrokContent == nil {
		return executionEventConflict()
	}
	last, err := domain.GrokEventIndex(p.GrokContent.LastEvent, event.NativeThreadID)
	terminal, terminalErr := domain.GrokEventIndex(v.NativeEventID, event.NativeThreadID)
	if err != nil || terminalErr != nil || terminal <= last {
		return executionEventConflict()
	}
	r, err := tx.Get(domain.MessageKind, v.MessageID)
	if err != nil {
		return err
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != sr.ID) ||
		m.ExecutionID != input.ExecutionID || m.NativeThreadID != event.NativeThreadID || m.NativeTurnID != event.NativeTurnID || m.Role != domain.AssistantMessage || m.LastSequence >= event.Sequence || m.GrokText == nil || m.GrokText.ResponseOrdinal != 1 || len(m.GrokText.Chunks) == 0 || uint32(len(m.GrokText.Chunks)) != v.TextChunks || m.GrokText.Interruption != nil || m.NativeID != m.GrokText.Chunks[0].EventID || m.GrokText.Chunks[len(m.GrokText.Chunks)-1].EventID != p.GrokContent.LastEvent || uint32(len(m.Text)) != p.GrokContent.TextBytes {
		return executionEventConflict()
	}
	first, _ := domain.GrokEventIndex(m.NativeID, event.NativeThreadID)
	chunks := map[string]bool{}
	for _, chunk := range m.GrokText.Chunks {
		chunks[chunk.EventID] = true
	}
	for _, retry := range v.Retries {
		index, err := domain.GrokEventIndex(retry.NativeEventID, event.NativeThreadID)
		if err != nil || index <= first || chunks[retry.NativeEventID] {
			return executionEventConflict()
		}
	}
	digest := sha256.Sum256([]byte(m.Text))
	if v.OutputDigest != hex.EncodeToString(digest[:]) {
		return executionEventConflict()
	}
	if v.Kind == domain.GrokInterruptedText {
		if p.GrokContent.Responses != 0 || p.GrokContent.MessageID != r.ID || p.GrokContent.MessageChunks != v.TextChunks || p.GrokContent.MessageBytes != uint32(len(m.Text)) || p.LatestUsageID != "" || m.State != domain.MessageStreaming {
			return executionEventConflict()
		}
		m.GrokText.Interruption = &domain.GrokTextInterruption{RequestID: v.RequestID, NativeEventID: v.NativeEventID}
		m.State, m.LastSequence = domain.MessageComplete, event.Sequence
		if _, err := tx.Put(domain.MessageKind, r.ID, r.Revision, r.SessionID, r.ProjectID, m); err != nil {
			return err
		}
		if err := tx.BindExecutionMessage(sr.ID, input.ExecutionID, r.ID, event.NativeThreadID, event.NativeTurnID, m.NativeID, domain.MessageComplete); err != nil {
			return err
		}
	} else {
		if p.GrokContent.Responses != 1 || p.GrokContent.MessageID != "" || m.State != domain.MessageComplete || v.Completed == nil {
			return executionEventConflict()
		}
		r, err := tx.Get(domain.UsageKind, p.LatestUsageID)
		if err != nil {
			return err
		}
		u, err := store.Decode[domain.GrokUsageRecord](r)
		if err != nil ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != sr.ID) ||
			u.ExecutionID != input.ExecutionID || u.Harness != domain.GrokBuild || u.Version != input.Installation.Version || u.ThreadID != event.NativeThreadID || u.TurnID != event.NativeTurnID || u.Usage.Ordinal != 1 || u.Usage.Counts != v.Completed.Counts {
			return executionEventConflict()
		}
	}
	complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	copy := *v
	p.GrokStop = &copy
	return nil
}
