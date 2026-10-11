// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Live and full-history consumers share this validator. The projection never
// replaces original history, which retains ciphertext and its original digest.
func decodeFunctionCallOutput(raw json.RawMessage) (string, *domain.FunctionCallOutputObservation, error) {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Namespace json.RawMessage `json:"namespace"`
		Output    json.RawMessage `json:"output"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "functionCallOutput" || domain.Text(item.ID, "identity", 1024, true) != nil || item.Namespace == nil || item.Output == nil {
		return "", nil, incompatible()
	}
	o := &domain.FunctionCallOutputObservation{Name: item.Name}
	if domain.Decode(item.Namespace, &o.Namespace) != nil {
		return "", nil, incompatible()
	}
	var text string
	if bytes.HasPrefix(bytes.TrimSpace(item.Output), []byte(`"`)) && json.Unmarshal(item.Output, &text) == nil {
		o.Text = &text
	} else {
		var parts []json.RawMessage
		if domain.Decode(item.Output, &parts) != nil || parts == nil || len(parts) > 1024 {
			return "", nil, incompatible()
		}
		o.Content = make([]domain.FunctionOutputContent, 0, len(parts))
		for _, part := range parts {
			var header map[string]json.RawMessage
			if domain.Decode(part, &header) != nil {
				return "", nil, incompatible()
			}
			var kind domain.FunctionOutputContentKind
			if domain.Decode(header["type"], &kind) != nil {
				return "", nil, incompatible()
			}
			var c domain.FunctionOutputContent
			if kind == domain.FunctionOutputEncrypted {
				var encrypted struct {
					Type  domain.FunctionOutputContentKind `json:"type"`
					Value *string                          `json:"encrypted_content"`
				}
				if domain.Decode(part, &encrypted) != nil || encrypted.Value == nil || domain.Text(*encrypted.Value, "protected output", domain.MaxMessageText, true) != nil {
					return "", nil, incompatible()
				}
				c.Type = kind
			} else {
				if domain.Decode(part, &c) != nil {
					return "", nil, incompatible()
				}
				for key, value := range header {
					if key != "type" && string(value) == "null" {
						return "", nil, incompatible()
					}
				}
			}
			o.Content = append(o.Content, c)
		}
	}
	if o.Validate() != nil {
		return "", nil, incompatible()
	}
	return item.ID, o, nil
}
