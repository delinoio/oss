package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishGrokTerminal(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.GrokTerminal
	if v == nil || v.Validate(event.NativeThreadID) != nil || p.GrokTerminal != nil || p.GrokStop != nil || input.Input.Mode != domain.ExecuteMode || input.Continuation != nil || v.Model != input.Configuration.NativeModel || p.GrokContent == nil || p.GrokContent.Responses != 1 || p.GrokContent.MessageID != "" || p.GrokContent.LastEvent == "" || event.Outcome != domain.ExecutionSucceeded || v.ClosureID == input.ThreadRequestID || v.ClosureID == input.TurnRequestID || v.ClosureID == input.SessionID {
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
	// Legacy accepted receipts omit the reservation and user comparison together.
	// A new reservation may only become a message through this original closure.
	if (p.GrokUserMessageID != "") != (v.User != nil) {
		return executionEventConflict()
	}
	if v.User != nil {
		if err := publishGrokUser(tx, input, sr, p, event); err != nil {
			return err
		}
	}
	copy := *v
	p.GrokTerminal = &copy
	return nil
}

func publishGrokUser(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	user := *event.GrokTerminal.User
	if p.GrokUserMessageID.Validate() != nil || user.InputDigest != domain.GrokUserInputDigest(input.Input.Prompt) {
		return executionEventConflict()
	}
	r, err := tx.GrokFirstTextMessage(input.ExecutionID)
	if err != nil {
		return err
	}
	assistant, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || r.SessionID != sr.ID || r.ID <= p.GrokUserMessageID || assistant.ExecutionID != input.ExecutionID || assistant.NativeThreadID != event.NativeThreadID || assistant.NativeTurnID != event.NativeTurnID || assistant.Role != domain.AssistantMessage || assistant.State != domain.MessageComplete || assistant.GrokText == nil || len(assistant.GrokText.Chunks) == 0 || assistant.NativeID != assistant.GrokText.Chunks[0].EventID {
		return executionEventConflict()
	}
	first, e1 := domain.GrokEventIndex(assistant.NativeID, event.NativeThreadID)
	original, e2 := domain.GrokEventIndex(user.NativeEventID, event.NativeThreadID)
	if e1 != nil || e2 != nil || original >= first {
		return executionEventConflict()
	}
	message := domain.ExecutionMessage{GrokUser: &user, ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: user.NativeEventID, Role: domain.UserMessage, InputID: input.InputID, Text: input.Input.Prompt, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence}
	if _, err := tx.Put(domain.MessageKind, p.GrokUserMessageID, 0, sr.ID, sr.ProjectID, message); err != nil {
		return err
	}
	return tx.BindExecutionMessage(sr.ID, input.ExecutionID, p.GrokUserMessageID, event.NativeThreadID, event.NativeTurnID, user.NativeEventID, domain.MessageComplete)
}
