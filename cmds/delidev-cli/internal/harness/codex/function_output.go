// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Official 0.162.0 c1382380de69521303b416720a52f42d51af6248:
// protocol/src/models.rs FunctionCallOutputBody/FunctionCallOutputContentItem.
// Validation never opens a reference, decodes media, decrypts or rebuilds history.
type FunctionOutput struct {
	ID          string
	Observation domain.FunctionOutputObservation
}

func decodeFunctionOutput(raw json.RawMessage) (*FunctionOutput, error) {
	var item struct {
		Type      string          `json:"type"`
		ID        *string         `json:"id"`
		Name      *string         `json:"name"`
		Namespace json.RawMessage `json:"namespace"`
		Output    json.RawMessage `json:"output"`
	}
	if domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "functionCallOutput" || item.ID == nil || item.Name == nil || len(item.Namespace) == 0 || domain.Text(*item.ID, "native output identity", 1024, true) != nil {
		return nil, incompatible()
	}
	v := domain.FunctionOutputObservation{Name: *item.Name}
	if !bytes.Equal(bytes.TrimSpace(item.Namespace), []byte("null")) {
		var namespace string
		if domain.Decode(item.Namespace, &namespace) != nil {
			return nil, incompatible()
		}
		v.Namespace = &namespace
	}
	output := bytes.TrimSpace(item.Output)
	if len(output) == 0 {
		return nil, incompatible()
	}
	if output[0] == '"' {
		var text string
		if domain.Decode(output, &text) != nil {
			return nil, incompatible()
		}
		v.Variant, v.Text = domain.FunctionOutputString, &text
	} else {
		var parts []json.RawMessage
		if output[0] != '[' || domain.DecodeBounded(output, &parts, 512<<10) != nil || parts == nil || len(parts) > domain.MaxArtifactParts {
			return nil, incompatible()
		}
		v.Variant, v.Parts = domain.FunctionOutputStructured, make([]domain.FunctionOutputPart, 0, len(parts))
		for _, rawPart := range parts {
			p, err := decodeFunctionOutputPart(rawPart)
			if err != nil {
				return nil, err
			}
			v.Parts = append(v.Parts, p)
		}
	}
	if v.Validate() != nil {
		return nil, incompatible()
	}
	return &FunctionOutput{ID: *item.ID, Observation: v}, nil
}
func decodeFunctionOutputPart(raw json.RawMessage) (domain.FunctionOutputPart, error) {
	var kind struct {
		Type domain.FunctionOutputPartKind `json:"type"`
	}
	if json.Unmarshal(raw, &kind) != nil {
		return domain.FunctionOutputPart{}, incompatible()
	}
	p := domain.FunctionOutputPart{Kind: kind.Type}
	switch p.Kind {
	case domain.FunctionOutputText:
		var wire struct {
			Type domain.FunctionOutputPartKind `json:"type"`
			Text *string                       `json:"text"`
		}
		if domain.Decode(raw, &wire) != nil || wire.Text == nil {
			return p, incompatible()
		}
		p.Text = wire.Text
	case domain.FunctionOutputImage:
		var wire struct {
			Type   domain.FunctionOutputPartKind     `json:"type"`
			URL    *string                           `json:"image_url"`
			File   *string                           `json:"file_id"`
			Detail *domain.FunctionOutputImageDetail `json:"detail"`
		}
		var fields map[string]json.RawMessage
		if domain.Decode(raw, &wire) != nil || domain.Decode(raw, &fields) != nil || (wire.URL == nil) == (wire.File == nil) {
			return p, incompatible()
		}
		// Explicit null/extra alternative is not an omitted optional field.
		for _, key := range []string{"image_url", "file_id", "detail"} {
			if value, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return p, incompatible()
			}
		}
		reference := wire.URL
		p.Reference = domain.FunctionOutputImageURL
		if wire.File != nil {
			reference = wire.File
			p.Reference = domain.FunctionOutputFileID
		}
		if domain.Text(*reference, "native image reference", domain.MaxMessageText, true) != nil {
			return p, incompatible()
		}
		p.Detail = wire.Detail
	case domain.FunctionOutputAudio:
		var wire struct {
			Type domain.FunctionOutputPartKind `json:"type"`
			URL  *string                       `json:"audio_url"`
		}
		if domain.Decode(raw, &wire) != nil || wire.URL == nil || domain.Text(*wire.URL, "native audio reference", domain.MaxMessageText, true) != nil {
			return p, incompatible()
		}
		p.Reference = domain.FunctionOutputAudioURL
	case domain.FunctionOutputEncrypted:
		var wire struct {
			Type      domain.FunctionOutputPartKind `json:"type"`
			Encrypted *string                       `json:"encrypted_content"`
		}
		if domain.Decode(raw, &wire) != nil || wire.Encrypted == nil || domain.Text(*wire.Encrypted, "private native output", domain.MaxMessageText, false) != nil {
			return p, incompatible()
		}
	default:
		return p, incompatible()
	}
	return p, nil
}
func (c *Client) observeFunctionOutput(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	output, err := decodeFunctionOutput(raw)
	if err != nil {
		return Event{}, err
	}
	if c.execution == nil {
		return Event{}, incompatible()
	}
	turn, known := c.execution.turns[turnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != turnID {
		return Event{}, incompatible()
	}
	kind := FunctionOutputStartedEvent
	if native.Method == "item/completed" {
		kind = FunctionOutputCompletedEvent
	}
	return Event{Kind: kind, ThreadID: c.thread, TurnID: turnID, ItemID: output.ID, FunctionOutput: output, Correlated: true}, nil
}
