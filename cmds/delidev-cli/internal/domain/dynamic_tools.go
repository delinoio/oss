// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"slices"
)

type DynamicContentKind string

const (
	DynamicText  DynamicContentKind = "inputText"
	DynamicImage DynamicContentKind = "inputImage"
	DynamicAudio DynamicContentKind = "inputAudio"
)

// Media locations are inert native metadata. They grant no fetch or byte claim.
type DynamicToolContent struct {
	Type     DynamicContentKind `json:"type"`
	Text     *string            `json:"text,omitempty"`
	ImageURL *string            `json:"imageUrl,omitempty"`
	AudioURL *string            `json:"audioUrl,omitempty"`
}
type DynamicToolObservation struct {
	Namespace    *string               `json:"namespace"`
	Tool         string                `json:"tool"`
	Arguments    json.RawMessage       `json:"arguments"`
	ContentItems *[]DynamicToolContent `json:"contentItems"`
	Success      *bool                 `json:"success"`
	DurationMS   *int64                `json:"durationMs"`
}

func (v DynamicToolObservation) Validate(status ToolStatus) error {
	var arguments any
	if Text(v.Tool, "native dynamic tool", 1024, true) != nil || v.Namespace != nil && Text(*v.Namespace, "native dynamic namespace", 1024, true) != nil || len(v.Arguments) == 0 || len(v.Arguments) > 256<<10 || DecodeBounded(v.Arguments, &arguments, 256<<10) != nil || v.DurationMS != nil && *v.DurationMS < 0 || status != ToolRunning && status != ToolCompleted && status != ToolFailed {
		return invalidTool()
	}
	if status == ToolRunning && (v.ContentItems != nil || v.Success != nil || v.DurationMS != nil) {
		return invalidTool()
	}
	if v.ContentItems != nil {
		if len(*v.ContentItems) > 1024 {
			return invalidTool()
		}
		for _, item := range *v.ContentItems {
			fields := 0
			for _, value := range []*string{item.Text, item.ImageURL, item.AudioURL} {
				if value != nil {
					fields++
					if Text(*value, "native dynamic content", MaxMessageText, false) != nil {
						return invalidTool()
					}
				}
			}
			if fields != 1 || item.Type == DynamicText && item.Text == nil || item.Type == DynamicImage && item.ImageURL == nil || item.Type == DynamicAudio && item.AudioURL == nil || item.Type != DynamicText && item.Type != DynamicImage && item.Type != DynamicAudio {
				return invalidTool()
			}
		}
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 384<<10 {
		return invalidTool()
	}
	return nil
}
func SameDynamicToolCall(a, b *DynamicToolObservation) bool {
	return a != nil && b != nil && a.Tool == b.Tool && (a.Namespace == nil) == (b.Namespace == nil) && (a.Namespace == nil || *a.Namespace == *b.Namespace) && bytes.Equal(a.Arguments, b.Arguments)
}

func CloneDynamicTool(v *DynamicToolObservation) *DynamicToolObservation {
	if v == nil {
		return nil
	}
	copy := *v
	copy.Namespace = copyDynamicValue(v.Namespace)
	copy.Arguments = slices.Clone(v.Arguments)
	copy.Success = copyDynamicValue(v.Success)
	copy.DurationMS = copyDynamicValue(v.DurationMS)
	if v.ContentItems != nil {
		items := slices.Clone(*v.ContentItems)
		for i := range items {
			items[i].Text = copyDynamicValue(items[i].Text)
			items[i].ImageURL = copyDynamicValue(items[i].ImageURL)
			items[i].AudioURL = copyDynamicValue(items[i].AudioURL)
		}
		copy.ContentItems = &items
	}
	return &copy
}
func copyDynamicValue[T any](v *T) *T {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
