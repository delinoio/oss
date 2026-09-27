package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type toolFamily string

const (
	fileToolFamily     toolFamily = "file"
	questionToolFamily toolFamily = "question"
)

// A mixed input shares original tool/request namespaces and cumulative bounds.
// Per-family observers still own their exact native schemas and transitions.
type mixedTools struct {
	files     *fileToolObserver
	questions *questionObserver
	owners    map[string]toolFamily
	streaming map[uint64]string
	arrivals  map[domain.ID]bool
	requests  map[string]bool
	bytes     int
	lastEvent uint64
	seenEvent bool
}

type mixedToolFact struct {
	File     *fileToolFact
	Question *questionFact
}

func newMixedTools(session domain.ID, prompt string) (*mixedTools, error) {
	files, err := newFileToolObserver(session, prompt)
	if err != nil {
		return nil, err
	}
	questions, err := newQuestionObserver(session, prompt)
	if err != nil {
		return nil, err
	}
	return &mixedTools{files: files, questions: questions, owners: map[string]toolFamily{}, streaming: map[uint64]string{}, arrivals: map[domain.ID]bool{}, requests: map[string]bool{}}, nil
}

func (o *mixedTools) observe(event nativewire.Event) (mixedToolFact, error) {
	var fact mixedToolFact
	if o == nil || len(event.Params) > nativewire.MaxFrame || o.bytes+len(event.Params) > 4<<20 {
		return fact, domain.Fail(domain.ResourceExhausted, "Native mixed tool observations reached their bound.", "Retain the original input and reconcile without replay.")
	}
	var id, request string
	var family toolFamily
	var index uint64
	stream, declared := false, false
	if event.Kind == nativewire.ServerRequest {
		var err error
		request, err = fileToolRequestKey(event.ID)
		if err != nil || event.Token.Validate() != nil || o.arrivals[event.Token] || o.requests[request] {
			return fact, incompatible()
		}
		switch event.Method {
		case "session/request_permission":
			var request struct {
				Tool struct {
					ID string `json:"toolCallId"`
				} `json:"toolCall"`
			}
			if json.Unmarshal(event.Params, &request) != nil {
				return fact, incompatible()
			}
			id, family = request.Tool.ID, fileToolFamily
		case "_x.ai/ask_user_question":
			var request struct {
				ID string `json:"toolCallId"`
			}
			if json.Unmarshal(event.Params, &request) != nil {
				return fact, incompatible()
			}
			id, family = request.ID, questionToolFamily
		default:
			return fact, incompatible()
		}
		if o.owners[id] != family {
			return fact, incompatible()
		}
	} else if event.Kind == nativewire.Notification {
		var variant struct {
			Update struct {
				Kind        string `json:"sessionUpdate"`
				ID          string `json:"toolCallId"`
				Interaction string `json:"tool_call_id"`
			} `json:"update"`
		}
		if json.Unmarshal(event.Params, &variant) != nil {
			return fact, incompatible()
		}
		switch variant.Update.Kind {
		case "tool_call_delta_chunk":
			var delta fileToolDelta
			if decode(event.Params, &delta) != nil || (delta.Update.ID == nil) != (delta.Update.Name == nil) {
				return fact, incompatible()
			}
			stream, index = true, delta.Update.Index
			if delta.Update.ID != nil {
				id = *delta.Update.ID
				switch *delta.Update.Name {
				case readFileTool, writeFileTool:
					family = fileToolFamily
				case askQuestionTool:
					family = questionToolFamily
				default:
					return fact, incompatible()
				}
				if prior, exists := o.owners[id]; exists && prior != family {
					return fact, incompatible()
				}
				if prior, exists := o.streaming[index]; exists && prior != id {
					return fact, incompatible()
				}
			} else {
				id = o.streaming[index]
				family = o.owners[id]
			}
		case "tool_call", "tool_call_update":
			id = variant.Update.ID
			family, declared = o.owners[id], variant.Update.Kind == "tool_call"
		case string(fileInteractionPending), string(fileInteractionResolved):
			id = variant.Update.Interaction
			family = o.owners[id]
		default:
			return fact, incompatible()
		}
	} else {
		return fact, incompatible()
	}
	if id == "" || family == "" {
		return fact, incompatible()
	}
	if _, exists := o.owners[id]; !exists && len(o.owners) >= 128 {
		return fact, domain.Fail(domain.ResourceExhausted, "Native mixed tool count reached its bound.", "Retain the original input and reconcile without replay.")
	}
	var eventSequence uint64
	hasEvent := event.Kind == nativewire.Notification && event.Method == "session/update"
	if hasEvent {
		var envelope struct {
			Meta struct {
				Event string `json:"eventId"`
			} `json:"_meta"`
		}
		if json.Unmarshal(event.Params, &envelope) != nil {
			return fact, incompatible()
		}
		var err error
		eventSequence, err = eventIndex(envelope.Meta.Event, o.files.session)
		if err != nil || o.seenEvent && eventSequence <= o.lastEvent {
			return fact, incompatible()
		}
	}
	switch family {
	case fileToolFamily:
		value, err := o.files.observe(event)
		if err != nil {
			return fact, err
		}
		fact.File = &value
	case questionToolFamily:
		value, err := o.questions.observe(event)
		if err != nil {
			return fact, err
		}
		fact.Question = &value
	default:
		return fact, incompatible()
	}
	// Publish routing ownership only after the original family accepts the
	// fact. An invalid cross-family candidate cannot poison either observer.
	o.owners[id] = family
	if stream {
		o.streaming[index] = id
	} else if declared {
		for index, owner := range o.streaming {
			if owner == id {
				delete(o.streaming, index)
			}
		}
	}
	if request != "" {
		o.requests[request], o.arrivals[event.Token] = true, true
	}
	o.bytes += len(event.Params)
	if hasEvent {
		o.lastEvent, o.seenEvent = eventSequence, true
	}
	return fact, nil
}
