package codex

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type InteractionKind string
type RequestIDKind string

const (
	UserInputInteraction InteractionKind = "user-input"
	TextRequestID        RequestIDKind   = "text"
	NumberRequestID      RequestIDKind   = "number"
)

// Native numeric and textual request identities are different namespaces.
// The arrival UUID additionally binds an interaction to this exact connection.
type NativeRequestID struct {
	Kind   RequestIDKind
	Text   string
	Number *int64
}

func decodeNativeRequestID(raw json.RawMessage) (NativeRequestID, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 256 {
		return NativeRequestID{}, incompatible()
	}
	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil || domain.Text(value, "native interaction identity", 128, true) != nil {
			return NativeRequestID{}, incompatible()
		}
		return NativeRequestID{Kind: TextRequestID, Text: value}, nil
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || !json.Valid(raw) || bytes.ContainsAny(raw, ".eE") {
		return NativeRequestID{}, incompatible()
	}
	return NativeRequestID{Kind: NumberRequestID, Number: &value}, nil
}

type QuestionOption struct {
	Label       string
	Description string
}
type Question struct {
	ID      string
	Header  string
	Text    string
	Other   bool
	Secret  bool
	Options []QuestionOption
}
type QuestionRequest struct {
	Blocking         bool
	AutoResolutionMS *uint64
	Questions        []Question
}
type Interaction struct {
	ID        domain.ID
	NativeID  NativeRequestID
	Kind      InteractionKind
	Questions *QuestionRequest
	// Raw request data and the pipe reply token remain private. A caller cannot
	// reconstruct reply authority from a serialized normalized observation.
	native nativewire.Event
}

func (c *Client) observeInteractionLocked(native nativewire.Event) (Event, error) {
	if native.Method != "item/tool/requestUserInput" {
		return privateNative(native), nil
	}
	var params struct {
		ThreadID         domain.ID         `json:"threadId"`
		TurnID           domain.ID         `json:"turnId"`
		ItemID           string            `json:"itemId"`
		Blocking         *bool             `json:"isBlocking"`
		AutoResolutionMS *uint64           `json:"autoResolutionMs"`
		Questions        []json.RawMessage `json:"questions"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || domain.Text(params.ItemID, "native question item", 1024, true) != nil || params.Blocking == nil || params.Questions == nil || len(params.Questions) == 0 || len(params.Questions) > 128 || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	nativeID, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	request := &QuestionRequest{Blocking: *params.Blocking, AutoResolutionMS: params.AutoResolutionMS, Questions: []Question{}}
	seen := map[string]bool{}
	for _, raw := range params.Questions {
		question, err := decodeQuestion(raw)
		if err != nil || seen[question.ID] {
			return Event{}, incompatible()
		}
		seen[question.ID] = true
		request.Questions = append(request.Questions, question)
	}
	interaction := &Interaction{ID: native.Token, NativeID: nativeID, Kind: UserInputInteraction, Questions: request, native: native}
	return Event{Kind: InteractionRequestedEvent, ThreadID: c.thread, TurnID: params.TurnID, ItemID: params.ItemID, Correlated: known, Late: turn.Turn.Status.terminal(), Interaction: interaction}, nil
}

func decodeQuestion(raw json.RawMessage) (Question, error) {
	var item struct {
		ID       *string           `json:"id"`
		Header   *string           `json:"header"`
		Question *string           `json:"question"`
		Other    json.RawMessage   `json:"isOther"`
		Secret   json.RawMessage   `json:"isSecret"`
		Options  []json.RawMessage `json:"options"`
	}
	if domain.Decode(raw, &item) != nil || item.ID == nil || item.Header == nil || item.Question == nil || domain.Text(*item.ID, "native question identity", 128, true) != nil || domain.Text(*item.Header, "native question header", 4096, false) != nil || domain.Text(*item.Question, "native question text", nativewire.MaxFrame, false) != nil || len(item.Options) > 128 {
		return Question{}, incompatible()
	}
	result := Question{ID: *item.ID, Header: *item.Header, Text: *item.Question}
	for _, field := range []struct {
		raw    json.RawMessage
		target *bool
	}{{item.Other, &result.Other}, {item.Secret, &result.Secret}} {
		// The pinned schema defaults omitted flags to false; null is not false.
		if len(field.raw) != 0 {
			var value *bool
			if json.Unmarshal(field.raw, &value) != nil || value == nil {
				return Question{}, incompatible()
			}
			*field.target = *value
		}
	}
	if item.Options != nil {
		result.Options = make([]QuestionOption, 0, len(item.Options))
	}
	seen := map[string]bool{}
	for _, raw := range item.Options {
		var option struct {
			Label       *string `json:"label"`
			Description *string `json:"description"`
		}
		if domain.Decode(raw, &option) != nil || option.Label == nil || option.Description == nil || domain.Text(*option.Label, "native question option", 4096, true) != nil || domain.Text(*option.Description, "native option description", nativewire.MaxFrame, false) != nil || seen[*option.Label] {
			return Question{}, incompatible()
		}
		seen[*option.Label] = true
		result.Options = append(result.Options, QuestionOption{Label: *option.Label, Description: *option.Description})
	}
	return result, nil
}
