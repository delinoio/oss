// SPDX-License-Identifier: Apache-2.0
package domain

import "strings"

type FunctionOutputContentKind string
type FunctionOutputImageDetail string

const (
	FunctionOutputText      FunctionOutputContentKind = "input_text"
	FunctionOutputImage     FunctionOutputContentKind = "input_image"
	FunctionOutputAudio     FunctionOutputContentKind = "input_audio"
	FunctionOutputEncrypted FunctionOutputContentKind = "encrypted_content"
	FunctionImageAuto       FunctionOutputImageDetail = "auto"
	FunctionImageLow        FunctionOutputImageDetail = "low"
	FunctionImageHigh       FunctionOutputImageDetail = "high"
	FunctionImageOriginal   FunctionOutputImageDetail = "original"
)

// Native references are inert metadata; encrypted bytes remain private history.
type FunctionOutputContent struct {
	Type     FunctionOutputContentKind  `json:"type"`
	Text     *string                    `json:"text,omitempty"`
	ImageURL *string                    `json:"image_url,omitempty"`
	FileID   *string                    `json:"file_id,omitempty"`
	AudioURL *string                    `json:"audio_url,omitempty"`
	Detail   *FunctionOutputImageDetail `json:"detail,omitempty"`
}
type FunctionCallOutputObservation struct {
	Name      string                  `json:"name"`
	Namespace *string                 `json:"namespace"`
	Text      *string                 `json:"text,omitempty"`
	Content   []FunctionOutputContent `json:"content"`
}

func (o FunctionCallOutputObservation) Validate() error {
	if Text(o.Name, "function name", 1024, true) != nil || o.Namespace != nil && Text(*o.Namespace, "namespace", 1024, false) != nil || (o.Text == nil) == (o.Content == nil) || len(o.Content) > 1024 {
		return invalidTool()
	}
	if o.Text != nil && Text(*o.Text, "output", MaxMessageText, false) != nil {
		return invalidTool()
	}
	total := 0
	for _, c := range o.Content {
		fields := 0
		for _, v := range []*string{c.Text, c.ImageURL, c.FileID, c.AudioURL} {
			if v != nil {
				fields++
				total += len(*v)
				if Text(*v, "content", MaxMessageText, false) != nil {
					return invalidTool()
				}
			}
		}
		switch c.Type {
		case FunctionOutputText:
			if fields != 1 || c.Text == nil || c.Detail != nil {
				return invalidTool()
			}
		case FunctionOutputImage:
			if fields != 1 || c.ImageURL == nil && c.FileID == nil {
				return invalidTool()
			}
			ref := c.ImageURL
			if ref == nil {
				ref = c.FileID
			}
			if Text(*ref, "image reference", 8192, true) != nil {
				return invalidTool()
			}
			if c.Detail != nil && *c.Detail != FunctionImageAuto && *c.Detail != FunctionImageLow && *c.Detail != FunctionImageHigh && *c.Detail != FunctionImageOriginal {
				return invalidTool()
			}
		case FunctionOutputAudio:
			if fields != 1 || c.AudioURL == nil || c.Detail != nil || Text(*c.AudioURL, "audio reference", 8192, true) != nil {
				return invalidTool()
			}
		case FunctionOutputEncrypted:
			if fields != 0 || c.Detail != nil {
				return invalidTool()
			}
		default:
			return invalidTool()
		}
	}
	if total > MaxMessageText {
		return invalidTool()
	}
	return nil
}
func (o FunctionCallOutputObservation) InertText() string {
	if o.Text != nil {
		return *o.Text
	}
	var text strings.Builder
	for _, c := range o.Content {
		if c.Type == FunctionOutputText && c.Text != nil {
			text.WriteString(*c.Text)
		}
	}
	return text.String()
}
