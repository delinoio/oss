package domain

import "encoding/json"

// OpenCode questions are an ordered matrix, without native per-question IDs.
// Preserve that representation and its optional flags instead of inventing
// Codex identities, blocking flags, secret declarations or auto-resolution.
type OpenCodeQuestion struct {
	Text     string           `json:"text"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple *bool            `json:"multiple,omitempty"`
	Custom   *bool            `json:"custom,omitempty"`
}

func (q *OpenCodeQuestion) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Text     *string           `json:"text"`
		Header   *string           `json:"header"`
		Options  []json.RawMessage `json:"options"`
		Multiple *bool             `json:"multiple,omitempty"`
		Custom   *bool             `json:"custom,omitempty"`
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &wire) != nil || Decode(raw, &fields) != nil || wire.Text == nil || wire.Header == nil || wire.Options == nil || fields["text"] == nil || fields["header"] == nil || fields["options"] == nil {
		return invalidInteraction()
	}
	for name, value := range fields {
		if name != "text" && name != "header" && name != "options" && name != "multiple" && name != "custom" || (name == "multiple" || name == "custom") && string(value) == "null" {
			return invalidInteraction()
		}
	}
	*q = OpenCodeQuestion{Text: *wire.Text, Header: *wire.Header, Options: []QuestionOption{}, Multiple: wire.Multiple, Custom: wire.Custom}
	for _, raw := range wire.Options {
		var option struct {
			Label       *string `json:"label"`
			Description *string `json:"description"`
		}
		var fields map[string]json.RawMessage
		if Decode(raw, &option) != nil || Decode(raw, &fields) != nil || len(fields) != 2 || fields["label"] == nil || fields["description"] == nil || option.Label == nil || option.Description == nil {
			return invalidInteraction()
		}
		q.Options = append(q.Options, QuestionOption{Label: *option.Label, Description: *option.Description})
	}
	return q.Validate()
}

func (q OpenCodeQuestion) Validate() error {
	if Text(q.Text, "native question", 64<<10, false) != nil || Text(q.Header, "native question header", 4096, false) != nil || q.Options == nil || len(q.Options) > 256 {
		return invalidInteraction()
	}
	for _, option := range q.Options {
		if Text(option.Label, "native question option", 4096, false) != nil || Text(option.Description, "native question description", 64<<10, false) != nil {
			return invalidInteraction()
		}
	}
	return nil
}

type OpenCodePermission struct {
	Name         string   `json:"name"`
	Patterns     []string `json:"patterns"`
	Always       []string `json:"always"`
	MetadataJSON string   `json:"metadata_json"`
}

func (p *OpenCodePermission) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Name         string    `json:"name"`
		Patterns     []*string `json:"patterns"`
		Always       []*string `json:"always"`
		MetadataJSON string    `json:"metadata_json"`
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &wire) != nil || Decode(raw, &fields) != nil || len(fields) != 4 || fields["name"] == nil || fields["patterns"] == nil || fields["always"] == nil || fields["metadata_json"] == nil || wire.Patterns == nil || wire.Always == nil {
		return invalidInteraction()
	}
	*p = OpenCodePermission{Name: wire.Name, Patterns: []string{}, Always: []string{}, MetadataJSON: wire.MetadataJSON}
	for _, pair := range []struct {
		source []*string
		target *[]string
	}{{wire.Patterns, &p.Patterns}, {wire.Always, &p.Always}} {
		for _, value := range pair.source {
			if value == nil {
				return invalidInteraction()
			}
			*pair.target = append(*pair.target, *value)
		}
	}
	return p.Validate()
}

func (p OpenCodePermission) Validate() error {
	if Text(p.Name, "native permission", 256, true) != nil || p.Patterns == nil || p.Always == nil || len(p.Patterns) > 1024 || len(p.Always) > 1024 || ValidateOpenCodeObjectJSON(p.MetadataJSON) != nil {
		return invalidInteraction()
	}
	for _, values := range [][]string{p.Patterns, p.Always} {
		for _, value := range values {
			if Text(value, "native permission pattern", 32768, false) != nil {
				return invalidInteraction()
			}
		}
	}
	return nil
}

// This original-tool profile requires observed tool ownership. The underlying
// native protocol can omit it; such requests need a separate publication profile.
type OpenCodeInteractionRequest struct {
	Version         string              `json:"version"`
	NativeEventID   string              `json:"native_event_id"`
	NativeMessageID string              `json:"native_message_id"`
	CallID          string              `json:"call_id"`
	Permission      *OpenCodePermission `json:"permission,omitempty"`
	Questions       []OpenCodeQuestion  `json:"questions,omitempty"`
}

func (r OpenCodeInteractionRequest) Validate(kind InteractionType, native InteractionRequestID) error {
	if r.Version != OpenCodeProtocolVersion || NativeIdentity(r.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || NativeIdentity(r.NativeMessageID).Validate(OpenCode, NativeMessageIdentity) != nil || Text(r.CallID, "native interaction tool call", 1024, true) != nil || native.Kind != InteractionTextID || native.Number != nil {
		return invalidInteraction()
	}
	switch kind {
	case NativeApprovalInteraction:
		if NativeIdentity(native.Text).Validate(OpenCode, NativePermissionIdentity) != nil || r.Permission == nil || r.Permission.Validate() != nil || r.Questions != nil {
			return invalidInteraction()
		}
	case UserQuestionInteraction:
		if NativeIdentity(native.Text).Validate(OpenCode, NativeQuestionIdentity) != nil || r.Permission != nil || r.Questions == nil || len(r.Questions) > 128 {
			return invalidInteraction()
		}
		for _, q := range r.Questions {
			if q.Validate() != nil {
				return invalidInteraction()
			}
		}
	default:
		return invalidInteraction()
	}
	return nil
}

// Keep the native explicitly empty question matrix across storage round trips.
func (r OpenCodeInteractionRequest) MarshalJSON() ([]byte, error) {
	type plain OpenCodeInteractionRequest
	if r.Questions != nil {
		return json.Marshal(struct {
			plain
			Questions []OpenCodeQuestion `json:"questions"`
		}{plain(r), r.Questions})
	}
	return json.Marshal(plain(r))
}

func OpenCodeResponseUnavailable() error {
	return Fail(Unsupported, "This OpenCode request is retained, but its product response delivery is not implemented.", "Keep the original request pending; an ordinary message or another harness response cannot answer or approve it.")
}
