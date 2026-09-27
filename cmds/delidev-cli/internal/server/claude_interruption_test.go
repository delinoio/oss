package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeInterruptionFixture(t *testing.T, kind domain.ClaudeInteractionKind, interrupt, uncertain, settle bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	t.Helper()
	f, tool, settlement := claudeSettlementResponseFixture(t, kind, true, uncertain, true, interrupt)
	f.publish(t, tool)
	sequence := tool.Sequence
	if settle {
		f.publish(t, settlement)
		sequence++
	}
	s := settlement.ClaudeSettlement
	text := domain.ClaudeDenialContextText
	v := domain.ClaudeInterruption{Kind: domain.ClaudeDenialContext, NativeEventID: string(domain.NewID()), InteractionID: s.InteractionID, ArrivalID: s.ArrivalID, ToolMessageID: s.ToolMessageID, ToolResultNativeID: s.ResultNativeID, Context: &text}
	c := f.event(domain.ExecutionClaudeInterruptionObserved, sequence+1)
	c.ClaudeInterruption = &domain.ExecutionClaudeInterruption{ID: domain.NewID(), Observation: v}
	r := f.event(domain.ExecutionClaudeInterruptionObserved, sequence+2)
	v.Kind, v.NativeEventID, v.Context = domain.ClaudeDenialResult, string(domain.NewID()), nil
	v.Result = &domain.ClaudeInterruptionResult{Kind: domain.ClaudeInterruptionError, Reason: domain.ClaudeToolsAborted, Error: true, Usage: &domain.ClaudeResultUsage{}}
	r.ClaudeInterruption = &domain.ExecutionClaudeInterruption{ID: domain.NewID(), Observation: v}
	return f, c, r
}

func TestClaudeInterruptionPreservesOriginalRecordsAndPauseWithoutInputOutcome(t *testing.T) {
	for _, kind := range []domain.ClaudeInteractionKind{domain.ClaudeToolPermission, domain.ClaudeUserQuestion, domain.ClaudePlanApproval} {
		for _, uncertain := range []bool{false, true} {
			f, c, result := claudeInterruptionFixture(t, kind, true, uncertain, true)
			for _, e := range []domain.ExecutionEvent{c, result} {
				receipt := f.publish(t, e)
				if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
					t.Fatal("original interruption receipt lost", err)
				}
				row, err := f.service.Store.Get(context.Background(), domain.MessageKind, e.ClaudeInterruption.ID)
				if err != nil {
					t.Fatal(err)
				}
				message, err := store.Decode[domain.ExecutionMessage](row)
				if err != nil || message.ClaudeInterruption == nil || message.ClaudeInterruption.Validate() != nil || message.NativeID != e.ClaudeInterruption.Observation.NativeEventID || message.InputID != "" || message.Role != domain.ProgressMessage || message.FirstSequence != e.Sequence || message.LastSequence != e.Sequence {
					t.Fatal("original context acquired input authority", err)
				}
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution.ClaudeInterruption.ResultID != result.ClaudeInterruption.ID || session.Execution.ClaudeInterruption.ContextID != c.ClaudeInterruption.ID || session.Dispatch != domain.DispatchPaused || session.Recovery != domain.NeedsRecovery || session.Outcome != domain.ExecutionRunning || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified || session.Execution.UnconfirmedResponses != 0 {
				t.Fatal("interruption changed independent outcome/cleanup", err)
			}
			queue, err := f.service.Store.Get(context.Background(), domain.QueueKind, f.input.InputID)
			if err != nil {
				t.Fatal(err)
			}
			input, err := store.Decode[domain.QueuedInput](queue)
			if err != nil || input.Delivery != domain.InputAccepted {
				t.Fatal("prior input acceptance changed", err)
			}
			inbox, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 10})
			if err != nil || len(inbox) != 1 {
				t.Fatal("interruption invented terminal inbox", err)
			}
			entry, err := store.Decode[domain.InboxEntry](inbox[0])
			if err != nil || entry.ReadState != domain.InboxUnread {
				t.Fatal("interruption marked inbox read", err)
			}
			result.Sequence++
			result.ClaudeInterruption.ID = domain.NewID()
			if _, err := f.call(f.requestEvent(t, result)); err == nil {
				t.Fatal("duplicate session result accepted")
			}
		}
	}
}

func TestClaudeInterruptionRejectsMissingChangedAndUnownedEvidenceAtomically(t *testing.T) {
	for _, scenario := range []string{"no-interrupt", "no-settlement", "no-context", "arrival", "interaction", "tool", "tool-result", "input", "context-text", "native-reuse", "duplicate-context", "write-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			f, c, result := claudeInterruptionFixture(t, domain.ClaudeToolPermission, scenario != "no-interrupt", false, scenario != "no-settlement")
			e := c
			switch scenario {
			case "no-context":
				e = result
				e.Sequence--
			case "arrival":
				e.ClaudeInterruption.Observation.ArrivalID = domain.NewID()
			case "interaction":
				e.ClaudeInterruption.Observation.InteractionID = domain.NewID()
			case "tool":
				e.ClaudeInterruption.Observation.ToolMessageID = domain.NewID()
			case "tool-result":
				e.ClaudeInterruption.Observation.ToolResultNativeID = string(domain.NewID())
			case "input":
				f.publish(t, c)
				e = result
				id := f.input.InputID
				e.ClaudeInterruption.Observation.Result.NativeInputID = &id
			case "context-text":
				text := "changed"
				e.ClaudeInterruption.Observation.Context = &text
			case "native-reuse":
				f.publish(t, c)
				e = result
				e.ClaudeInterruption.Observation.NativeEventID = c.ClaudeInterruption.Observation.NativeEventID
			case "duplicate-context":
				f.publish(t, c)
				e.Sequence++
				e.ClaudeInterruption.ID = domain.NewID()
			case "write-conflict":
				e.ClaudeInterruption.ID = e.ClaudeInterruption.Observation.ToolMessageID
			}
			before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unverified interruption accepted")
			}
			after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil || before.Revision != after.Revision {
				t.Fatal("invalid interruption partially changed session", err)
			}
		})
	}
}
