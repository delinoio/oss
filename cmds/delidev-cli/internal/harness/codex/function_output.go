// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type functionOutputItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Namespace *string         `json:"namespace"`
	Output    json.RawMessage `json:"output"`
}

// Validate native bytes without reserializing protected history. Only the safe
// projection escapes this decoder; media locators/ciphertext grant no I/O.
func decodeFunctionOutput(raw json.RawMessage) (string, *domain.FunctionOutput, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 512<<10 || !functionOutputUnicode(raw) || domain.DecodeBounded(raw, &fields, 512<<10) != nil || (len(fields) != 4 && len(fields) != 5) || fields["type"] == nil || fields["id"] == nil || fields["name"] == nil || fields["output"] == nil {
		return "", nil, incompatible()
	}
	for key := range fields {
		if key != "type" && key != "id" && key != "name" && key != "namespace" && key != "output" {
			return "", nil, incompatible()
		}
	}
	var item functionOutputItem
	if domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "functionCallOutput" || domain.Text(item.ID, "native function output identity", 1024, true) != nil {
		return "", nil, incompatible()
	}
	out := &domain.FunctionOutput{Name: item.Name, Namespace: item.Namespace, NamespacePresent: fields["namespace"] != nil}
	var text string
	if len(item.Output) > 0 && item.Output[0] == '"' && json.Unmarshal(item.Output, &text) == nil {
		out.Text = &text
	} else {
		var content []json.RawMessage
		if domain.DecodeBounded(item.Output, &content, 512<<10) != nil || content == nil || len(content) > 1024 {
			return "", nil, incompatible()
		}
		out.Structured = true
		if len(content) > 0 {
			out.Content = make([]domain.FunctionOutputContent, 0, len(content))
		}
		for _, rawPart := range content {
			var part struct {
				Type      domain.FunctionOutputKind   `json:"type"`
				Text      *string                     `json:"text"`
				ImageURL  *string                     `json:"image_url"`
				FileID    *string                     `json:"file_id"`
				Detail    *domain.FunctionImageDetail `json:"detail"`
				AudioURL  *string                     `json:"audio_url"`
				Encrypted *string                     `json:"encrypted_content"`
			}
			var present map[string]json.RawMessage
			if domain.Decode(rawPart, &present) != nil || domain.Decode(rawPart, &part) != nil {
				return "", nil, incompatible()
			}
			allowed := map[string]bool{"type": true}
			safe := domain.FunctionOutputContent{Kind: part.Type}
			switch part.Type {
			case domain.FunctionText:
				allowed["text"] = true
				if part.Text == nil {
					return "", nil, incompatible()
				}
				safe.Text = part.Text
			case domain.FunctionImage:
				allowed["image_url"], allowed["file_id"], allowed["detail"] = true, true, true
				if (part.ImageURL == nil) == (part.FileID == nil) || present["image_url"] != nil && present["file_id"] != nil || present["detail"] != nil && part.Detail == nil {
					return "", nil, incompatible()
				}
				safe.Detail = part.Detail
				safe.ReferenceKind = domain.FunctionImageURL
				if part.FileID != nil {
					safe.ReferenceKind = domain.FunctionFileID
				}
			case domain.FunctionAudio:
				allowed["audio_url"] = true
				if part.AudioURL == nil {
					return "", nil, incompatible()
				}
				safe.ReferenceKind = domain.FunctionAudioURL
			case domain.FunctionEncrypted:
				allowed["encrypted_content"] = true
				if part.Encrypted == nil {
					return "", nil, incompatible()
				}
			default:
				return "", nil, incompatible()
			}
			for key := range present {
				if !allowed[key] {
					return "", nil, incompatible()
				}
			}
			for _, reference := range []*string{part.ImageURL, part.FileID, part.AudioURL, part.Encrypted} {
				if reference != nil && domain.Text(*reference, "private native function content", 512<<10, false) != nil {
					return "", nil, incompatible()
				}
			}
			out.Content = append(out.Content, safe)
		}
	}
	if out.Validate() != nil {
		return "", nil, incompatible()
	}
	return item.ID, out, nil
}

type functionOutputIdentity struct {
	Turn      domain.ID
	Digest    [32]byte
	Completed bool
}

func (c *Client) retainFunctionOutput(raw json.RawMessage, id string, turn domain.ID, stage domain.FunctionOutputStage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return incompatible()
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return incompatible()
	}
	digest := sha256.Sum256(normal)
	if c.functionOutputs == nil {
		c.functionOutputs = map[string]functionOutputIdentity{}
	}
	prior, known := c.functionOutputs[id]
	if known && (prior.Turn != turn || prior.Digest != digest || prior.Completed || stage != domain.FunctionOutputCompleted) || !known && len(c.functionOutputs) >= 10000 {
		return incompatible()
	}
	c.functionOutputs[id] = functionOutputIdentity{Turn: turn, Digest: digest, Completed: stage == domain.FunctionOutputCompleted}
	return nil
}

// Reject unpaired escapes before encoding/json can silently replace native bytes.
// Escaped literal backslashes are skipped as pairs, so literal text stays intact.
func functionOutputUnicode(raw json.RawMessage) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
