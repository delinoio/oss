package opencode

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type InteractionKind string

const (
	PermissionInteraction InteractionKind = "permission"
	QuestionInteraction   InteractionKind = "question"
)

type PermissionDecision string

const (
	PermissionOnce   PermissionDecision = "once"
	PermissionAlways PermissionDecision = "always"
	PermissionReject PermissionDecision = "reject"
)

type InteractionTool struct {
	MessageID string
	CallID    string
}

// NativeInteraction is a private proposal, not permission to reply. Optional
// tool ownership remains optional; a session ID cannot invent a missing call.
type NativeInteraction struct {
	ID         string
	SessionID  string
	Kind       InteractionKind
	Tool       *InteractionTool
	Permission *NativePermission `json:"-"`
	Questions  []NativeQuestion  `json:"-"`
}

type NativePermission struct {
	Name     string
	Patterns []string        `json:"-"`
	Always   []string        `json:"-"`
	Metadata json.RawMessage `json:"-"`
}

type NativeQuestion struct {
	Text     string           `json:"-"`
	Header   string           `json:"-"`
	Options  []QuestionOption `json:"-"`
	Multiple *bool
	Custom   *bool
}

type QuestionOption struct {
	Label       string `json:"-"`
	Description string `json:"-"`
}

// NativeInteractionReply preserves the exact native closure namespace. A
// permission rejection/always decision can close other pending permissions;
// decoding this notification cannot attribute it to a direct response claim.
type NativeInteractionReply struct {
	RequestID string
	SessionID string
	Kind      InteractionKind
	Decision  *PermissionDecision
	Rejected  bool
	Answers   [][]string `json:"-"`
}

func interactionProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode interaction does not match its typed native profile.", "Retain the original request and verify tool ownership and response authority before answering.")
}

func nativeStrings(raw json.RawMessage, count, size int) ([]string, bool) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil || len(entries) > count {
		return nil, false
	}
	result := make([]string, len(entries))
	for i, entry := range entries {
		text, valid := boundedString(entry, size, false)
		if !valid {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

func decodeInteractionTool(raw []byte) (*InteractionTool, error) {
	fields, err := shape(raw, []string{"messageID", "callID"}, nil)
	if err != nil {
		return nil, interactionProblem()
	}
	message, ok := boundedString(fields["messageID"], 30, true)
	call, valid := boundedString(fields["callID"], 1024, true)
	if !ok || !valid || !nativeID(message, "msg") {
		return nil, interactionProblem()
	}
	return &InteractionTool{MessageID: message, CallID: call}, nil
}

func decodeNativeInteraction(kind EventKind, raw []byte) (NativeInteraction, error) {
	bad := func() (NativeInteraction, error) { return NativeInteraction{}, interactionProblem() }
	var result NativeInteraction
	var required []string
	prefix := ""
	switch kind {
	case PermissionAskedEvent:
		result.Kind, prefix = PermissionInteraction, "per"
		required = []string{"id", "sessionID", "permission", "patterns", "metadata", "always"}
	case QuestionAskedEvent:
		result.Kind, prefix = QuestionInteraction, "que"
		required = []string{"id", "sessionID", "questions"}
	default:
		return bad()
	}
	fields, err := shape(raw, required, []string{"tool"})
	if err != nil {
		return bad()
	}
	var ok, valid bool
	result.ID, ok = boundedString(fields["id"], 30, true)
	result.SessionID, valid = boundedString(fields["sessionID"], 30, true)
	if !ok || !valid || !nativeID(result.ID, prefix) || !nativeID(result.SessionID, "ses") {
		return bad()
	}
	if raw, exists := fields["tool"]; exists {
		result.Tool, err = decodeInteractionTool(raw)
		if err != nil {
			return bad()
		}
	}
	if result.Kind == PermissionInteraction {
		p := &NativePermission{}
		result.Permission = p
		p.Name, ok = boundedString(fields["permission"], 256, true)
		p.Patterns, valid = nativeStrings(fields["patterns"], 1024, 32768)
		if !ok || !valid {
			return bad()
		}
		p.Always, ok = nativeStrings(fields["always"], 1024, 32768)
		p.Metadata, valid = privateObject(fields["metadata"])
		if !ok || !valid {
			return bad()
		}
		return result, nil
	}
	var questions []json.RawMessage
	if json.Unmarshal(fields["questions"], &questions) != nil || questions == nil || len(questions) > 128 {
		return bad()
	}
	result.Questions = make([]NativeQuestion, len(questions))
	for i, raw := range questions {
		fields, err := shape(raw, []string{"question", "header", "options"}, []string{"multiple", "custom"})
		if err != nil {
			return bad()
		}
		q := &result.Questions[i]
		q.Text, ok = boundedString(fields["question"], 64<<10, false)
		q.Header, valid = boundedString(fields["header"], 4096, false)
		if !ok || !valid {
			return bad()
		}
		for key, destination := range map[string]**bool{"multiple": &q.Multiple, "custom": &q.Custom} {
			if raw, exists := fields[key]; exists {
				*destination, ok = boolPointer(raw)
				if !ok {
					return bad()
				}
			}
		}
		var options []json.RawMessage
		if json.Unmarshal(fields["options"], &options) != nil || options == nil || len(options) > 256 {
			return bad()
		}
		q.Options = make([]QuestionOption, len(options))
		for j, raw := range options {
			fields, err := shape(raw, []string{"label", "description"}, nil)
			if err != nil {
				return bad()
			}
			q.Options[j].Label, ok = boundedString(fields["label"], 4096, false)
			q.Options[j].Description, valid = boundedString(fields["description"], 64<<10, false)
			if !ok || !valid {
				return bad()
			}
		}
	}
	return result, nil
}

func decodeNativeInteractionReply(kind EventKind, raw []byte) (NativeInteractionReply, error) {
	bad := func() (NativeInteractionReply, error) { return NativeInteractionReply{}, interactionProblem() }
	result := NativeInteractionReply{Kind: QuestionInteraction}
	required := []string{"sessionID", "requestID"}
	prefix := "que"
	switch kind {
	case PermissionRepliedEvent:
		result.Kind, prefix = PermissionInteraction, "per"
		required = append(required, "reply")
	case QuestionRepliedEvent:
		required = append(required, "answers")
	case QuestionRejectedEvent:
		result.Rejected = true
	default:
		return bad()
	}
	fields, err := shape(raw, required, nil)
	if err != nil {
		return bad()
	}
	var ok, valid bool
	result.RequestID, ok = boundedString(fields["requestID"], 30, true)
	result.SessionID, valid = boundedString(fields["sessionID"], 30, true)
	if !ok || !valid || !nativeID(result.RequestID, prefix) || !nativeID(result.SessionID, "ses") {
		return bad()
	}
	if kind == PermissionRepliedEvent {
		value, ok := boundedString(fields["reply"], 16, true)
		decision := PermissionDecision(value)
		if !ok || decision != PermissionOnce && decision != PermissionAlways && decision != PermissionReject {
			return bad()
		}
		result.Decision = &decision
		result.Rejected = decision == PermissionReject
	} else if kind == QuestionRepliedEvent {
		var answers []json.RawMessage
		if json.Unmarshal(fields["answers"], &answers) != nil || answers == nil || len(answers) > 128 {
			return bad()
		}
		result.Answers = make([][]string, len(answers))
		for i, raw := range answers {
			result.Answers[i], ok = nativeStrings(raw, 256, 64<<10)
			if !ok {
				return bad()
			}
		}
	}
	return result, nil
}
