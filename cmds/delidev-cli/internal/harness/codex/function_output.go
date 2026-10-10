// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The same closed decoder admits live and full-history items. The source bytes
// remain in protected native history; only this safe projection leaves it.
func decodeFunctionOutput(raw json.RawMessage) (*domain.CodexFunctionOutput, error) {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Namespace json.RawMessage `json:"namespace"`
		Output    json.RawMessage `json:"output"`
	}
	if domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "functionCallOutput" || len(item.Namespace) == 0 || len(item.Output) == 0 {
		return nil, incompatible()
	}
	v := &domain.CodexFunctionOutput{NativeItemID: item.ID, Name: item.Name}
	if string(item.Namespace) != "null" {
		var namespace string
		if json.Unmarshal(item.Namespace, &namespace) != nil {
			return nil, incompatible()
		}
		v.Namespace = &namespace
	}
	var text string
	if json.Unmarshal(item.Output, &text) == nil && string(item.Output) != "null" {
		v.Variant = domain.FunctionOutputString
		v.Text = &text
	} else {
		var parts []json.RawMessage
		if domain.DecodeBounded(item.Output, &parts, 512<<10) != nil || parts == nil || len(parts) > domain.MaxArtifactParts {
			return nil, incompatible()
		}
		v.Variant = domain.FunctionOutputStructured
		v.Content = make([]domain.FunctionOutputContent, 0, len(parts))
		for _, rawPart := range parts {
			var part struct {
				Kind      domain.FunctionOutputContentKind `json:"type"`
				Text      *string                          `json:"text"`
				ImageURL  *string                          `json:"image_url"`
				FileID    *string                          `json:"file_id"`
				AudioURL  *string                          `json:"audio_url"`
				Detail    *string                          `json:"detail"`
				Encrypted *string                          `json:"encrypted_content"`
			}
			if domain.DecodeBounded(rawPart, &part, 512<<10) != nil {
				return nil, incompatible()
			}
			var fields map[string]json.RawMessage
			if domain.Decode(rawPart, &fields) != nil {
				return nil, incompatible()
			}
			allowed := map[string]bool{"type": true}
			switch part.Kind {
			case domain.FunctionOutputText:
				allowed["text"] = true
			case domain.FunctionOutputImage:
				allowed["image_url"] = true
				allowed["file_id"] = true
				allowed["detail"] = true
			case domain.FunctionOutputAudio:
				allowed["audio_url"] = true
			case domain.FunctionOutputEncrypted:
				allowed["encrypted_content"] = true
				if part.Encrypted == nil || domain.Text(*part.Encrypted, "private encrypted function output", 512<<10, false) != nil {
					return nil, incompatible()
				}
			default:
				return nil, incompatible()
			}
			for key := range fields {
				if !allowed[key] {
					return nil, incompatible()
				}
			}
			for _, source := range fields {
				if string(source) == "null" {
					return nil, incompatible()
				}
			}
			if part.Kind == domain.FunctionOutputImage {
				if _, hasURL := fields["image_url"]; hasURL {
					if _, hasFile := fields["file_id"]; hasFile {
						return nil, incompatible()
					}
				}
			}
			v.Content = append(v.Content, domain.FunctionOutputContent{Kind: part.Kind, Text: part.Text, ImageURL: part.ImageURL, FileID: part.FileID, AudioURL: part.AudioURL, Detail: part.Detail})
		}
	}
	if v.Validate() != nil {
		return nil, incompatible()
	}
	return v, nil
}
