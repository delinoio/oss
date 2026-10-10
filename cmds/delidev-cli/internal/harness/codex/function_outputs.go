// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// One closed decoder admits live and full-history output without opening a
// reference or reconstructing protected history from its public projection.
func decodeFunctionOutput(raw json.RawMessage) (*domain.CodexFunctionOutput, error) {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Namespace *string         `json:"namespace"`
		Output    json.RawMessage `json:"output"`
	}
	if domain.DecodeBounded(raw, &item, 1<<20) != nil || item.Type != "functionCallOutput" {
		return nil, incompatible()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["namespace"] == nil {
		return nil, incompatible()
	}
	v := &domain.CodexFunctionOutput{Version: 1, ID: domain.NewID(), NativeID: item.ID, Name: item.Name, Namespace: item.Namespace, Stage: domain.CodexFunctionOutputCompleted}
	body := bytes.TrimSpace(item.Output)
	if len(body) == 0 {
		return nil, incompatible()
	}
	switch body[0] {
	case '"':
		var text string
		if domain.DecodeBounded(body, &text, 1<<20) != nil {
			return nil, incompatible()
		}
		v.Output = domain.CodexFunctionOutputBody{Variant: domain.CodexFunctionString, Text: &text}
	case '[':
		var contents []json.RawMessage
		if domain.DecodeBounded(body, &contents, 1<<20) != nil || contents == nil || len(contents) > domain.MaxCodexFunctionContents {
			return nil, incompatible()
		}
		v.Output = domain.CodexFunctionOutputBody{Variant: domain.CodexFunctionContents, Contents: []domain.CodexFunctionContent{}}
		for _, rawContent := range contents {
			var keys map[string]json.RawMessage
			if domain.DecodeBounded(rawContent, &keys, 1<<20) != nil {
				return nil, incompatible()
			}
			var kind domain.CodexFunctionContentKind
			if json.Unmarshal(keys["type"], &kind) != nil {
				return nil, incompatible()
			}
			c := domain.CodexFunctionContent{Type: kind}
			switch kind {
			case domain.CodexFunctionText:
				var content struct {
					Type domain.CodexFunctionContentKind `json:"type"`
					Text *string                         `json:"text"`
				}
				if domain.DecodeBounded(rawContent, &content, 1<<20) != nil || content.Text == nil {
					return nil, incompatible()
				}
				c.Text = content.Text
			case domain.CodexFunctionImage:
				var content struct {
					Type   domain.CodexFunctionContentKind `json:"type"`
					Detail *string                         `json:"detail,omitempty"`
					URL    *string                         `json:"image_url,omitempty"`
					File   *string                         `json:"file_id,omitempty"`
				}
				if domain.DecodeBounded(rawContent, &content, 1<<20) != nil || (keys["image_url"] != nil) == (keys["file_id"] != nil) || (content.URL == nil) == (content.File == nil) || keys["detail"] != nil && content.Detail == nil {
					return nil, incompatible()
				}
				ref := content.URL
				c.ReferenceKind = domain.CodexFunctionImageURL
				if content.File != nil {
					ref = content.File
					c.ReferenceKind = domain.CodexFunctionFileID
				}
				if domain.Text(*ref, "private native image reference", 256<<10, false) != nil || content.Detail != nil && !slices.Contains([]string{"auto", "low", "high", "original"}, *content.Detail) {
					return nil, incompatible()
				}
				c.ReferencePresent = true
				c.Detail = content.Detail
			case domain.CodexFunctionAudio:
				var content struct {
					Type domain.CodexFunctionContentKind `json:"type"`
					URL  *string                         `json:"audio_url"`
				}
				if domain.DecodeBounded(rawContent, &content, 1<<20) != nil || content.URL == nil || domain.Text(*content.URL, "private native audio reference", 256<<10, false) != nil {
					return nil, incompatible()
				}
				c.ReferenceKind = domain.CodexFunctionAudioURL
				c.ReferencePresent = true
			case domain.CodexFunctionEncrypted:
				var content struct {
					Type      domain.CodexFunctionContentKind `json:"type"`
					Encrypted *string                         `json:"encrypted_content"`
				}
				if domain.DecodeBounded(rawContent, &content, 1<<20) != nil || content.Encrypted == nil || domain.Text(*content.Encrypted, "private encrypted output", 256<<10, false) != nil {
					return nil, incompatible()
				}
				c.Present = true
			default:
				return nil, incompatible()
			}
			v.Output.Contents = append(v.Output.Contents, c)
		}
	default:
		return nil, incompatible()
	}
	if v.Validate() != nil {
		return nil, incompatible()
	}
	return v, nil
}
