package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"strconv"
)

func publishGrokToolsTerminal(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.GrokToolsTerminal
	if v == nil || input.Input.Mode == domain.ExecuteMode && p.GrokToolObservations == 0 || v.Validate(event.NativeThreadID) != nil || p.GrokToolsTerminal != nil || p.GrokTerminal != nil || p.GrokStop != nil || input.Continuation != nil || v.Model != input.Configuration.NativeModel || event.Outcome != v.Outcome() || p.UnconfirmedResponses != 0 || p.GrokContent == nil || p.GrokContent.MessageID != "" || p.GrokResponseTotals == nil || *p.GrokResponseTotals != v.Counts || v.ModelCalls != strconv.FormatUint(uint64(p.GrokContent.Responses), 10) {
		return executionEventConflict()
	}
	journal, records, _, err := retainedGrokJournal(tx, input, event.NativeThreadID, event.NativeTurnID)
	if err != nil {
		return err
	}
	if !journal.Settled() {
		return executionEventConflict()
	}
	for _, r := range records {
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil || value.Closure == domain.InteractionOpen {
			return executionEventConflict()
		}
	}
	messages, err := tx.GrokToolJournal(input.SessionID, input.ExecutionID)
	if err != nil {
		return err
	}
	terminal, err := domain.GrokEventIndex(v.NativeEventID, event.NativeThreadID)
	if err != nil {
		return err
	}
	for _, m := range messages {
		meta := m.GrokTool.Payload.Meta
		if meta != nil && meta.Event != "" {
			prior, e := domain.GrokEventIndex(meta.Event, event.NativeThreadID)
			if e != nil || prior >= terminal {
				return executionEventConflict()
			}
		}
	}
	if p.GrokContent.LastEvent != "" {
		last, err := domain.GrokEventIndex(p.GrokContent.LastEvent, event.NativeThreadID)
		if err != nil || last >= terminal {
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
	if err := advanceGrokNativeEvent(p, v.NativeEventID, event.NativeThreadID); err != nil {
		return err
	}
	copy := *v
	p.GrokToolsTerminal = &copy
	return nil
}
