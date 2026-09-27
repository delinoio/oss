package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishGrokTerminal(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.GrokTerminal
	if v == nil || v.Validate(event.NativeThreadID) != nil || p.GrokTerminal != nil || input.Input.Mode != domain.ExecuteMode || input.Continuation != nil || v.Model != input.Configuration.NativeModel || p.GrokContent == nil || p.GrokContent.Responses != 1 || p.GrokContent.MessageID != "" || p.GrokContent.LastEvent == "" || event.Outcome != domain.ExecutionSucceeded || v.ClosureID == input.ThreadRequestID || v.ClosureID == input.TurnRequestID || v.ClosureID == input.SessionID {
		return executionEventConflict()
	}
	last, err := domain.GrokEventIndex(p.GrokContent.LastEvent, event.NativeThreadID)
	terminal, terminalErr := domain.GrokEventIndex(v.NativeEventID, event.NativeThreadID)
	if err != nil || terminalErr != nil || terminal <= last {
		return executionEventConflict()
	}
	r, err := tx.Get(domain.UsageKind, p.LatestUsageID)
	if err != nil {
		return err
	}
	usage, err := store.Decode[domain.GrokUsageRecord](r)
	if err != nil || r.SessionID != sr.ID || usage.ExecutionID != input.ExecutionID || usage.Harness != domain.GrokBuild || usage.Version != input.Installation.Version || usage.ThreadID != event.NativeThreadID || usage.TurnID != event.NativeTurnID || usage.Usage.Ordinal != 1 || usage.Usage.Counts != v.Counts {
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
	p.GrokTerminal = &copy
	return nil
}
