package domain

import (
	"encoding/json"
	"strconv"
)

type InteractionType string
type InteractionRequestIDKind string
type InteractionClosure string

const (
	UserQuestionInteraction  InteractionType          = "user-question"
	InteractionTextID        InteractionRequestIDKind = "text"
	InteractionNumberID      InteractionRequestIDKind = "number"
	InteractionOpen          InteractionClosure       = "open"
	InteractionNativeClosed  InteractionClosure       = "native-closed"
	InteractionTurnEnded     InteractionClosure       = "turn-ended"
	MaxExecutionInteractions                          = 4096
	MaxOpenInteractions                               = 128
	MaxOpenQuestionBytes                              = 8 << 20
)

type InteractionRequestID struct {
	Kind   InteractionRequestIDKind `json:"kind"`
	Text   string                   `json:"text,omitempty"`
	Number *int64                   `json:"number,omitempty"`
}

func (id InteractionRequestID) Key() (string, error) {
	switch id.Kind {
	case InteractionTextID:
		if id.Number == nil && Text(id.Text, "native request identity", 128, true) == nil {
			return "s:" + id.Text, nil
		}
	case InteractionNumberID:
		if id.Text == "" && id.Number != nil {
			return "n:" + strconv.FormatInt(*id.Number, 10), nil
		}
	}
	return "", invalidInteraction()
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type Question struct {
	ID      string           `json:"id"`
	Header  string           `json:"header"`
	Text    string           `json:"text"`
	Other   bool             `json:"other"`
	Secret  bool             `json:"secret"`
	Options []QuestionOption `json:"options"`
}
type QuestionRequest struct {
	Blocking         bool       `json:"blocking"`
	AutoResolutionMS *uint64    `json:"auto_resolution_ms"`
	Questions        []Question `json:"questions"`
}

func invalidInteraction() error {
	return Fail(InvalidArgument, "Invalid native interaction observation.", "Preserve its exact request kind, original questions and native ownership without response or approval fields.")
}

// Required native fields must not silently acquire Go zero values from missing
// or null JSON. Decode rejects unknown fields throughout the question graph.
func (q *Question) UnmarshalJSON(raw []byte) error {
	var wire struct {
		ID      *string           `json:"id"`
		Header  *string           `json:"header"`
		Text    *string           `json:"text"`
		Other   *bool             `json:"other"`
		Secret  *bool             `json:"secret"`
		Options []json.RawMessage `json:"options"`
	}
	if Decode(raw, &wire) != nil || wire.ID == nil || wire.Header == nil || wire.Text == nil || wire.Other == nil || wire.Secret == nil {
		return invalidInteraction()
	}
	*q = Question{ID: *wire.ID, Header: *wire.Header, Text: *wire.Text, Other: *wire.Other, Secret: *wire.Secret}
	if wire.Options != nil {
		q.Options = make([]QuestionOption, 0, len(wire.Options))
	}
	for _, raw := range wire.Options {
		var option struct {
			Label       *string `json:"label"`
			Description *string `json:"description"`
		}
		if Decode(raw, &option) != nil || option.Label == nil || option.Description == nil {
			return invalidInteraction()
		}
		q.Options = append(q.Options, QuestionOption{Label: *option.Label, Description: *option.Description})
	}
	return nil
}

func (q *QuestionRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Blocking         *bool      `json:"blocking"`
		AutoResolutionMS *uint64    `json:"auto_resolution_ms"`
		Questions        []Question `json:"questions"`
	}
	if Decode(raw, &wire) != nil || wire.Blocking == nil {
		return invalidInteraction()
	}
	*q = QuestionRequest{Blocking: *wire.Blocking, AutoResolutionMS: wire.AutoResolutionMS, Questions: wire.Questions}
	return q.Validate()
}

func (r QuestionRequest) Validate() error {
	if len(r.Questions) == 0 || len(r.Questions) > 128 {
		return invalidInteraction()
	}
	seen := map[string]bool{}
	for _, q := range r.Questions {
		if Text(q.ID, "question identity", 128, true) != nil || Text(q.Header, "question header", 4096, false) != nil || Text(q.Text, "question text", MaxMessageText, false) != nil || len(q.Options) > 128 || seen[q.ID] {
			return invalidInteraction()
		}
		seen[q.ID] = true
		options := map[string]bool{}
		for _, option := range q.Options {
			if Text(option.Label, "question option", 4096, true) != nil || Text(option.Description, "option description", MaxMessageText, false) != nil || options[option.Label] {
				return invalidInteraction()
			}
			options[option.Label] = true
		}
	}
	return nil
}

// The first durable interaction boundary observes requests and unanswered
// closure only. Response claims/delivery require a separate coordinator path;
// a Worker cannot fabricate owner authorization by adding answer fields here.
type ExecutionInteractionUpdate struct {
	ID              ID                   `json:"id"`
	NativeItemID    string               `json:"native_item_id"`
	NativeRequestID InteractionRequestID `json:"native_request_id"`
	Type            InteractionType      `json:"type"`
	Questions       *QuestionRequest     `json:"questions,omitempty"`
	Closure         InteractionClosure   `json:"closure,omitempty"`
}

func (k ExecutionEventKind) IsInteraction() bool {
	return k == ExecutionInteractionRequested || k == ExecutionInteractionClosed
}

func (u ExecutionInteractionUpdate) Validate(kind ExecutionEventKind) error {
	if u.ID.Validate() != nil || Text(u.NativeItemID, "native question item", 1024, true) != nil || u.Type != UserQuestionInteraction {
		return invalidInteraction()
	}
	if _, err := u.NativeRequestID.Key(); err != nil {
		return err
	}
	switch kind {
	case ExecutionInteractionRequested:
		if u.Questions == nil || u.Closure != "" || u.Questions.Validate() != nil {
			return invalidInteraction()
		}
	case ExecutionInteractionClosed:
		if u.Questions != nil || u.Closure != InteractionNativeClosed {
			return invalidInteraction()
		}
	default:
		return invalidInteraction()
	}
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > 512<<10 {
		return Fail(ResourceExhausted, "The question observation exceeds its publication bound.", "Retain native state for reconciliation without truncating the questions.")
	}
	return nil
}

type ExecutionInteraction struct {
	ExecutionID     ID                   `json:"execution_id"`
	NativeThreadID  string               `json:"native_thread_id"`
	NativeTurnID    string               `json:"native_turn_id"`
	NativeItemID    string               `json:"native_item_id"`
	NativeRequestID InteractionRequestID `json:"native_request_id"`
	Type            InteractionType      `json:"type"`
	Questions       *QuestionRequest     `json:"questions"`
	Closure         InteractionClosure   `json:"closure"`
	FirstSequence   uint64               `json:"first_sequence"`
	LastSequence    uint64               `json:"last_sequence"`
	Response        *QuestionResponse    `json:"response,omitempty"`
}

// Waiting flags are observed native state, not answer/approval authority and
// not closure evidence. Idle status cannot resolve a retained interaction.
type NativeWaiting struct {
	UserInput bool `json:"user_input"`
	Approval  bool `json:"approval"`
}

func (w *NativeWaiting) UnmarshalJSON(raw []byte) error {
	var wire struct {
		UserInput *bool `json:"user_input"`
		Approval  *bool `json:"approval"`
	}
	if Decode(raw, &wire) != nil || wire.UserInput == nil || wire.Approval == nil {
		return invalidInteraction()
	}
	*w = NativeWaiting{UserInput: *wire.UserInput, Approval: *wire.Approval}
	return nil
}
