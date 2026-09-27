package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type questionInteractionStage string

const (
	questionPermissionPending  questionInteractionStage = "permission-pending"
	questionPermissionResolved questionInteractionStage = "permission-resolved"
	questionAnswerPending      questionInteractionStage = "question-pending"
	questionAnswerResolved     questionInteractionStage = "question-resolved"
)

type questionInteraction struct {
	ID    string
	Stage questionInteractionStage
}

type questionFact struct {
	Delta       *fileToolDelta
	Observation *questionObservation
	Interaction *questionInteraction
	Request     *questionRequest
}

type questionToolState struct {
	index     uint64
	arguments string
	questions [32]byte
	phase     fileToolPhase
	stage     questionInteractionStage
	arrival   domain.ID
}

type questionObserver struct {
	session   domain.ID
	mode      NativeMode
	prompt    string
	tools     map[string]questionToolState
	arrivals  map[domain.ID]bool
	requests  map[string]bool
	bytes     int
	lastEvent uint64
	seenEvent bool
}

func newQuestionObserver(session domain.ID, prompt string) (*questionObserver, error) {
	return newQuestionObserverForMode(session, prompt, NativeDefaultMode)
}

func newQuestionObserverForMode(session domain.ID, prompt string, mode NativeMode) (*questionObserver, error) {
	if session.Validate() != nil || !nativeUUID(prompt, 4) || mode != NativeDefaultMode && mode != NativePlanMode {
		return nil, incompatible()
	}
	return &questionObserver{session: session, prompt: prompt, mode: mode, tools: map[string]questionToolState{}, arrivals: map[domain.ID]bool{}, requests: map[string]bool{}}, nil
}

func (o *questionObserver) observe(event nativewire.Event) (questionFact, error) {
	var fact questionFact
	if o == nil || len(event.Params) > nativewire.MaxFrame || o.bytes+len(event.Params) > 4<<20 {
		return fact, domain.Fail(domain.ResourceExhausted, "Native question observations reached their bound.", "Retain the original input and reconcile without replay.")
	}
	if event.Kind == nativewire.ServerRequest {
		key, err := fileToolRequestKey(event.ID)
		if event.Method != "_x.ai/ask_user_question" || event.Token.Validate() != nil || err != nil || o.arrivals[event.Token] || o.requests[key] {
			return fact, incompatible()
		}
		request, err := parseQuestionRequestForMode(event.Params, o.session, o.mode)
		if err != nil {
			return fact, err
		}
		prior, exists := o.tools[request.ID]
		if !exists || prior.phase != fileToolDescribed || prior.stage != questionAnswerPending || prior.arrival != "" || prior.questions != historyValueDigest(request.Questions) {
			return fact, incompatible()
		}
		prior.arrival = event.Token
		o.tools[request.ID] = prior
		o.arrivals[event.Token], o.requests[key] = true, true
		o.bytes += len(event.Params)
		fact.Request = &request
		return fact, nil
	}
	if event.Kind != nativewire.Notification {
		return fact, incompatible()
	}
	var variant struct {
		Update struct {
			Kind string `json:"sessionUpdate"`
			ID   string `json:"toolCallId"`
		} `json:"update"`
	}
	if json.Unmarshal(event.Params, &variant) != nil {
		return fact, incompatible()
	}
	var id string
	var next questionToolState
	var index uint64
	hasEvent := false
	switch variant.Update.Kind {
	case "tool_call_delta_chunk":
		var delta fileToolDelta
		if event.Method != "_x.ai/session_notification" || decode(event.Params, &delta) != nil || delta.Session != o.session || delta.Update.Index >= 128 || !toolText(delta.Update.Arguments) || (delta.Update.ID == nil) != (delta.Update.Name == nil) {
			return fact, incompatible()
		}
		if delta.Update.ID != nil {
			if !text(*delta.Update.ID, 256) || *delta.Update.Name != askQuestionTool {
				return fact, incompatible()
			}
			id = *delta.Update.ID
		} else {
			for candidate, tool := range o.tools {
				if tool.phase == "" && tool.index == delta.Update.Index {
					if id != "" {
						return fact, incompatible()
					}
					id = candidate
				}
			}
			if id == "" {
				return fact, incompatible()
			}
		}
		prior, exists := o.tools[id]
		if exists && (prior.phase != "" || prior.index != delta.Update.Index) || len(prior.arguments)+len(delta.Update.Arguments) > 256<<10 {
			return fact, incompatible()
		}
		next = prior
		if !exists {
			for _, tool := range o.tools {
				if tool.phase == "" && tool.index == delta.Update.Index {
					return fact, incompatible()
				}
			}
			next.index = delta.Update.Index
		}
		next.arguments += delta.Update.Arguments
		fact.Delta = &delta
	case "tool_call", "tool_call_update":
		if event.Method != "session/update" {
			return fact, incompatible()
		}
		observed, err := parseQuestionObservation(event.Params, o.session, o.prompt)
		if err != nil {
			return fact, err
		}
		id = observed.ID
		prior, exists := o.tools[id]
		if !exists {
			return fact, incompatible()
		}
		next = prior
		switch observed.Phase {
		case fileToolDeclared:
			items, err := parseQuestionArguments([]byte(prior.arguments))
			if prior.phase != "" || err != nil || historyValueDigest(items) != historyValueDigest(observed.Questions) {
				return fact, incompatible()
			}
			next.arguments, next.questions = "", historyValueDigest(items)
		case fileToolDescribed:
			if prior.phase != fileToolDeclared || prior.stage != questionPermissionPending || prior.questions != historyValueDigest(observed.Questions) {
				return fact, incompatible()
			}
		case fileToolCompleted:
			if prior.phase != fileToolDescribed || prior.stage != questionAnswerResolved || prior.arrival == "" {
				return fact, incompatible()
			}
		default:
			return fact, incompatible()
		}
		index, err = eventIndex(observed.Meta.Event, o.session)
		if err != nil || o.seenEvent && index <= o.lastEvent {
			return fact, incompatible()
		}
		hasEvent, next.phase, fact.Observation = true, observed.Phase, &observed
	case string(fileInteractionPending), string(fileInteractionResolved):
		var interaction struct {
			Session domain.ID `json:"sessionId"`
			Update  struct {
				Kind fileInteractionKind `json:"sessionUpdate"`
				ID   string              `json:"tool_call_id"`
				Type *string             `json:"kind,omitempty"`
			} `json:"update"`
		}
		if event.Method != "_x.ai/session_notification" || decode(event.Params, &interaction) != nil || interaction.Session != o.session {
			return fact, incompatible()
		}
		id = interaction.Update.ID
		prior, exists := o.tools[id]
		if !exists {
			return fact, incompatible()
		}
		next = prior
		if interaction.Update.Kind == fileInteractionPending {
			if interaction.Update.Type == nil {
				return fact, incompatible()
			}
			switch *interaction.Update.Type {
			case "permission":
				if prior.phase != fileToolDeclared || prior.stage != "" {
					return fact, incompatible()
				}
				next.stage = questionPermissionPending
			case "question":
				if prior.phase != fileToolDescribed || prior.stage != questionPermissionResolved {
					return fact, incompatible()
				}
				next.stage = questionAnswerPending
			default:
				return fact, incompatible()
			}
		} else {
			if interaction.Update.Type != nil || prior.phase != fileToolDescribed {
				return fact, incompatible()
			}
			switch prior.stage {
			case questionPermissionPending:
				next.stage = questionPermissionResolved
			case questionAnswerPending:
				if prior.arrival == "" {
					return fact, incompatible()
				}
				next.stage = questionAnswerResolved
			default:
				return fact, incompatible()
			}
		}
		fact.Interaction = &questionInteraction{ID: id, Stage: next.stage}
	default:
		return fact, incompatible()
	}
	if _, exists := o.tools[id]; !exists && len(o.tools) >= 128 {
		return questionFact{}, domain.Fail(domain.ResourceExhausted, "Native question count reached its bound.", "Retain the original input and reconcile without replay.")
	}
	o.tools[id] = next
	o.bytes += len(event.Params)
	if hasEvent {
		o.lastEvent, o.seenEvent = index, true
	}
	return fact, nil
}
