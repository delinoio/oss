package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const askQuestionTool fileToolName = "ask_user_question"

type questionMode string

const questionDefaultMode questionMode = "default"

type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// MultiSelect retains native null independently of explicit false. Native
// arguments use multi_select, while the detailed request uses multiSelect.
type questionItem struct {
	Question    string           `json:"question"`
	Options     []questionOption `json:"options"`
	MultiSelect json.RawMessage  `json:"multiSelect"`
}

func validateQuestions(items []questionItem) error {
	if len(items) == 0 || len(items) > 32 {
		return incompatible()
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !text(item.Question, 16<<10) || seen[item.Question] || len(item.Options) == 0 || len(item.Options) > 64 {
			return incompatible()
		}
		seen[item.Question] = true
		var multi bool
		if !isNull(item.MultiSelect) && decode(item.MultiSelect, &multi) != nil {
			return incompatible()
		}
		labels := map[string]bool{}
		for _, option := range item.Options {
			if !text(option.Label, 4096) || domain.Text(option.Description, "native question option", 16<<10, false) != nil || labels[option.Label] {
				return incompatible()
			}
			labels[option.Label] = true
		}
	}
	raw, err := json.Marshal(items)
	if err != nil || len(raw) > 256<<10 {
		return incompatible()
	}
	return nil
}

func parseQuestionArguments(raw []byte) ([]questionItem, error) {
	var args struct {
		Questions []struct {
			Question string           `json:"question"`
			Options  []questionOption `json:"options"`
			Multi    *bool            `json:"multi_select,omitempty"`
		} `json:"questions"`
	}
	if len(raw) > 256<<10 || decode(raw, &args) != nil {
		return nil, incompatible()
	}
	items := make([]questionItem, 0, len(args.Questions))
	for _, arg := range args.Questions {
		multi, _ := json.Marshal(arg.Multi)
		items = append(items, questionItem{arg.Question, arg.Options, multi})
	}
	if err := validateQuestions(items); err != nil {
		return nil, err
	}
	return items, nil
}

type questionRequest struct {
	Session   domain.ID      `json:"sessionId"`
	ID        string         `json:"toolCallId"`
	Questions []questionItem `json:"questions"`
	Mode      questionMode   `json:"mode"`
}

func parseQuestionRequest(raw []byte, session domain.ID) (questionRequest, error) {
	var request questionRequest
	if len(raw) > 256<<10 || session.Validate() != nil || decode(raw, &request) != nil || request.Session != session || !text(request.ID, 256) || request.Mode != questionDefaultMode || validateQuestions(request.Questions) != nil {
		return request, incompatible()
	}
	return request, nil
}

type questionObservation struct {
	ID         string
	Phase      fileToolPhase
	Questions  []questionItem
	Title      string
	Descriptor toolDescriptor
	Meta       toolObservationMeta
	Message    string
}

func questionDescriptor(d toolDescriptor) bool {
	return d.Version == 1 && d.Name == askQuestionTool && d.Kind == "ask_user" && d.Namespace == fileGrokNamespace && d.Label == "Ask User" && d.ReadOnly && d.Input == nil
}

func parseQuestionObservation(raw []byte, session domain.ID, prompt string) (questionObservation, error) {
	var value questionObservation
	var envelope struct {
		Session domain.ID           `json:"sessionId"`
		Update  json.RawMessage     `json:"update"`
		Meta    toolObservationMeta `json:"_meta"`
	}
	if session.Validate() != nil || decode(raw, &envelope) != nil || envelope.Session != session {
		return value, incompatible()
	}
	var variant struct {
		Kind   string  `json:"sessionUpdate"`
		Status *string `json:"status"`
	}
	if json.Unmarshal(envelope.Update, &variant) != nil {
		return value, incompatible()
	}
	value.Meta = envelope.Meta
	if variant.Kind == "tool_call" {
		var declared struct {
			Kind  string                 `json:"sessionUpdate"`
			ID    string                 `json:"toolCallId"`
			Title string                 `json:"title"`
			Input json.RawMessage        `json:"rawInput"`
			Meta  toolDescriptorEnvelope `json:"_meta"`
		}
		if decode(envelope.Update, &declared) != nil || declared.Title != string(askQuestionTool) || !questionDescriptor(declared.Meta.Tool) {
			return value, incompatible()
		}
		items, err := parseQuestionArguments(declared.Input)
		if err != nil {
			return value, err
		}
		value.ID, value.Phase, value.Title, value.Descriptor, value.Questions = declared.ID, fileToolDeclared, declared.Title, declared.Meta.Tool, items
	} else if variant.Kind == "tool_call_update" && variant.Status == nil {
		var details struct {
			Kind      string            `json:"sessionUpdate"`
			ID        string            `json:"toolCallId"`
			Category  string            `json:"kind"`
			Title     string            `json:"title"`
			Locations []json.RawMessage `json:"locations"`
			Input     struct {
				Variant   string         `json:"variant"`
				Questions []questionItem `json:"questions"`
			} `json:"rawInput"`
			Meta toolDescriptorEnvelope `json:"_meta"`
		}
		if decode(envelope.Update, &details) != nil || details.Category != "other" || !text(details.Title, 32<<10) || len(details.Locations) != 0 || details.Input.Variant != "AskUserQuestion" || validateQuestions(details.Input.Questions) != nil || !questionDescriptor(details.Meta.Tool) {
			return value, incompatible()
		}
		value.ID, value.Phase, value.Title, value.Descriptor, value.Questions = details.ID, fileToolDescribed, details.Title, details.Meta.Tool, details.Input.Questions
	} else if variant.Kind == "tool_call_update" && variant.Status != nil && *variant.Status == "completed" {
		var completed struct {
			Kind    string `json:"sessionUpdate"`
			ID      string `json:"toolCallId"`
			Status  string `json:"status"`
			Content []struct {
				Type    string     `json:"type"`
				Content promptText `json:"content"`
			} `json:"content"`
			Output struct {
				Type   string `json:"type"`
				Answer struct {
					Message string `json:"message"`
				} `json:"UserAnswered"`
			} `json:"rawOutput"`
		}
		if decode(envelope.Update, &completed) != nil || completed.Output.Type != "AskUserQuestion" || !text(completed.Output.Answer.Message, 256<<10) || len(completed.Content) != 1 || completed.Content[0].Type != "content" || completed.Content[0].Content.Type != "text" || completed.Content[0].Content.Text != completed.Output.Answer.Message {
			return value, incompatible()
		}
		// This rendered message is not parsed into an answer or cancellation.
		// Both accepted and declined questions have this same completed shape.
		value.ID, value.Phase, value.Message = completed.ID, fileToolCompleted, completed.Output.Answer.Message
	} else {
		return value, incompatible()
	}
	if !text(value.ID, 256) || !value.Meta.validate(session, prompt, value.ID, value.Phase, askQuestionTool) {
		return value, incompatible()
	}
	return value, nil
}
