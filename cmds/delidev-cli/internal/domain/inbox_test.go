package domain

import (
	"encoding/json"
	"testing"
)

func TestInboxMetadataRejectsCrossKindAndIncompleteEvidence(t *testing.T) {
	valid := InboxEntry{Source: ExecutionTerminalInbox, SourceID: NewID(), ReadState: InboxUnread, Terminal: &InboxTerminal{JobID: NewID(), InputID: NewID(), NativeThreadID: "thread", NativeTurnID: "turn", Sequence: 3, Outcome: ExecutionSucceeded}}
	for _, change := range []string{"source", "source-id", "state", "missing-terminal", "cross-kind", "job", "input", "thread", "turn", "zero-sequence", "overflow-sequence", "running", "unknown-outcome"} {
		t.Run(change, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var value InboxEntry
			if Decode(raw, &value) != nil || value.Validate() != nil {
				t.Fatal("valid fixture failed")
			}
			switch change {
			case "source":
				value.Source = "permission"
			case "source-id":
				value.SourceID = "invalid"
			case "state":
				value.ReadState = "approved"
			case "missing-terminal":
				value.Terminal = nil
			case "cross-kind":
				value.Source = InteractionInbox
			case "job":
				value.Terminal.JobID = ""
			case "input":
				value.Terminal.InputID = ""
			case "thread":
				value.Terminal.NativeThreadID = ""
			case "turn":
				value.Terminal.NativeTurnID = ""
			case "zero-sequence":
				value.Terminal.Sequence = 0
			case "overflow-sequence":
				value.Terminal.Sequence = MaxExecutionEvents + 1
			case "running":
				value.Terminal.Outcome = ExecutionRunning
			case "unknown-outcome":
				value.Terminal.Outcome = "cleanup-verified"
			}
			if value.Validate() == nil {
				t.Fatal("invalid inbox metadata was accepted")
			}
		})
	}
	for _, raw := range []string{`{"source":"interaction","source_id":"` + string(NewID()) + `","read_state":"read","approved":true}`, `{"source":"interaction","source_id":"` + string(NewID()) + `","read_state":"read","answers":{"choice":["secret"]}}`} {
		var value InboxEntry
		if Decode([]byte(raw), &value) == nil {
			t.Fatal("inbox metadata accepted owner answer/approval fields")
		}
	}
}
