package claude

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// sessionHistory retains hashes before observations reach display consumers.
// Only pending compaction messages need private bytes, released at completion.
// Child and auxiliary completeness remain separate from this root ledger.
type sessionHistory struct {
	messages    []HistoryMessageProof
	compactions []HistoryCompactionProof
	actions     []HistoryCompactionActionProof
	resumes     []historyResumeProof
	boundary    *StreamEvent
	echo        *StreamEvent
	output      *StreamEvent
	action      domain.ID
	status      CompactResult
}

func retainHistoryEvent(event *StreamEvent) *StreamEvent {
	copy := *event
	copy.Body = bytes.Clone(event.Body)
	return &copy
}

func (h *sessionHistory) observe(observed LifecycleObservation) error {
	if observed.Native == nil || observed.Native.Kind != NativeMessage {
		return nil
	}
	event := observed.Native
	if observed.Kind == CompactionObserved {
		if h.boundary != nil || len(h.compactions) >= maxStreamIdentities {
			return historyUncertain()
		}
		h.boundary = retainHistoryEvent(event)
		return nil
	}
	if observed.Kind == CompactionSummaryObserved {
		if h.boundary == nil {
			return historyUncertain()
		}
		proof, err := ObserveMainCompaction(*h.boundary, *event, observed.SessionID)
		if err != nil {
			return err
		}
		h.compactions = append(h.compactions, proof)
		h.boundary = nil
		return nil
	}
	if observed.ActionID != "" {
		if h.action != "" && h.action != observed.ActionID {
			return historyUncertain()
		}
		h.action = observed.ActionID
		switch observed.Kind {
		case CompactionCommandObserved:
			if observed.CompactCommand.Kind == CompactionCommandEcho {
				if h.echo != nil {
					return historyUncertain()
				}
				h.echo = retainHistoryEvent(event)
			} else {
				if h.output != nil {
					return historyUncertain()
				}
				h.output = retainHistoryEvent(event)
			}
		case CompactionResultObserved:
			h.status = observed.CompactResult.Status
		case RunStateObserved:
			if observed.Run.State != RunIdle {
				break
			}
			if h.echo == nil || h.output == nil || len(h.messages) == 0 || len(h.actions) >= maxStreamIdentities {
				return historyUncertain()
			}
			boundary := ""
			if h.status == CompactSucceeded {
				if len(h.compactions) == 0 {
					return historyUncertain()
				}
				boundary = h.compactions[len(h.compactions)-1].NativeID
			}
			proof, err := ObserveCompactionActionHistory(*h.echo, *h.output, observed.SessionID, h.action, h.messages[len(h.messages)-1].NativeID, h.status, boundary)
			if err != nil {
				return err
			}
			h.actions = append(h.actions, proof)
			h.echo, h.output, h.action, h.status = nil, nil, "", ""
		}
		return nil
	}
	if event.Type == "user" || event.Type == "assistant" {
		var fields map[string]json.RawMessage
		if domain.Decode(event.Body, &fields) != nil {
			return historyUncertain()
		}
		if !bytes.Equal(bytes.TrimSpace(fields["parent_tool_use_id"]), []byte("null")) {
			return nil
		}
		if len(h.messages) >= maxStreamIdentities {
			return historyUncertain()
		}
		proof, err := ObserveMainHistoryMessage(*event, observed.SessionID)
		if err != nil {
			return err
		}
		h.messages = append(h.messages, proof)
	}
	return nil
}
