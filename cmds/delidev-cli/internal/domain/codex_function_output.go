// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

type FunctionOutputVariant string
type FunctionOutputContentKind string

const (
	FunctionOutputString     FunctionOutputVariant     = "string"
	FunctionOutputStructured FunctionOutputVariant     = "structured"
	FunctionOutputText       FunctionOutputContentKind = "input_text"
	FunctionOutputImage      FunctionOutputContentKind = "input_image"
	FunctionOutputAudio      FunctionOutputContentKind = "input_audio"
	FunctionOutputEncrypted  FunctionOutputContentKind = "encrypted_content"
)

// Encrypted bytes have no field in this public projection. Media references are
// inert native metadata, never a Worker-owned file/image grant or retrieval URL.
type FunctionOutputContent struct {
	Kind     FunctionOutputContentKind `json:"type"`
	Text     *string                   `json:"text,omitempty"`
	ImageURL *string                   `json:"image_url,omitempty"`
	FileID   *string                   `json:"file_id,omitempty"`
	AudioURL *string                   `json:"audio_url,omitempty"`
	Detail   *string                   `json:"detail,omitempty"`
}
type CodexFunctionOutput struct {
	NativeItemID string                  `json:"native_item_id"`
	Name         string                  `json:"name"`
	Namespace    *string                 `json:"namespace"`
	Variant      FunctionOutputVariant   `json:"variant"`
	Text         *string                 `json:"text,omitempty"`
	Content      []FunctionOutputContent `json:"content"`
}

func (v CodexFunctionOutput) Validate() error {
	if Text(v.NativeItemID, "native function output identity", 1024, true) != nil || Text(v.Name, "native function name", 1024, true) != nil || v.Namespace != nil && Text(*v.Namespace, "native function namespace", 1024, false) != nil {
		return invalidArtifact()
	}
	switch v.Variant {
	case FunctionOutputString:
		if v.Text == nil || v.Content != nil || Text(*v.Text, "native function output", MaxMessageText, false) != nil {
			return invalidArtifact()
		}
	case FunctionOutputStructured:
		if v.Text != nil || v.Content == nil || len(v.Content) > MaxArtifactParts {
			return invalidArtifact()
		}
		for _, p := range v.Content {
			for _, value := range []*string{p.Text, p.ImageURL, p.FileID, p.AudioURL} {
				if value != nil && Text(*value, "native function output part", MaxMessageText, false) != nil {
					return invalidArtifact()
				}
			}
			switch p.Kind {
			case FunctionOutputText:
				if p.Text == nil || p.ImageURL != nil || p.FileID != nil || p.AudioURL != nil || p.Detail != nil {
					return invalidArtifact()
				}
			case FunctionOutputImage:
				if (p.ImageURL == nil) == (p.FileID == nil) || p.Text != nil || p.AudioURL != nil || p.Detail != nil && !slices.Contains([]string{"auto", "low", "high", "original"}, *p.Detail) {
					return invalidArtifact()
				}
			case FunctionOutputAudio:
				if p.AudioURL == nil || p.Text != nil || p.ImageURL != nil || p.FileID != nil || p.Detail != nil {
					return invalidArtifact()
				}
			case FunctionOutputEncrypted:
				if p.Text != nil || p.ImageURL != nil || p.FileID != nil || p.AudioURL != nil || p.Detail != nil {
					return invalidArtifact()
				}
			default:
				return invalidArtifact()
			}
		}
	default:
		return invalidArtifact()
	}
	return boundedArtifactPublication(v)
}
