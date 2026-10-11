// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

const CodexFunctionOutputsV1 WorkerCapability = "codex-function-outputs-v1"

type FunctionOutputKind string
type FunctionReferenceKind string
type FunctionOutputStage string
type FunctionImageDetail string

const (
	FunctionText            FunctionOutputKind    = "input_text"
	FunctionImage           FunctionOutputKind    = "input_image"
	FunctionAudio           FunctionOutputKind    = "input_audio"
	FunctionEncrypted       FunctionOutputKind    = "encrypted_content"
	FunctionImageURL        FunctionReferenceKind = "image_url"
	FunctionFileID          FunctionReferenceKind = "file_id"
	FunctionAudioURL        FunctionReferenceKind = "audio_url"
	FunctionDetailAuto      FunctionImageDetail   = "auto"
	FunctionDetailLow       FunctionImageDetail   = "low"
	FunctionDetailHigh      FunctionImageDetail   = "high"
	FunctionDetailOriginal  FunctionImageDetail   = "original"
	FunctionOutputStarted   FunctionOutputStage   = "started"
	FunctionOutputCompleted FunctionOutputStage   = "completed"
)

// This public projection never contains media locators or encrypted bytes.
// Presence and reference kind are observations, not permission to retrieve data.
type FunctionOutputContent struct {
	Kind          FunctionOutputKind    `json:"kind"`
	Text          *string               `json:"text,omitempty"`
	ReferenceKind FunctionReferenceKind `json:"reference_kind,omitempty"`
	Detail        *FunctionImageDetail  `json:"detail,omitempty"`
}
type FunctionOutput struct {
	Name       string                  `json:"name"`
	Namespace  *string                 `json:"namespace"`
	Text       *string                 `json:"text,omitempty"`
	Content    []FunctionOutputContent `json:"content,omitempty"`
	Structured bool                    `json:"structured"`
}
type FunctionOutputUpdate struct {
	ID       ID                  `json:"id"`
	NativeID string              `json:"native_id"`
	Stage    FunctionOutputStage `json:"stage"`
	Output   FunctionOutput      `json:"output"`
}

func (o FunctionOutput) Validate() error {
	if Text(o.Name, "native function name", 256, true) != nil || o.Namespace != nil && Text(*o.Namespace, "native function namespace", 256, false) != nil || o.Structured && o.Text != nil || !o.Structured && (o.Text == nil || o.Content != nil) || len(o.Content) > 1024 {
		return invalidTool()
	}
	if o.Text != nil && Text(*o.Text, "native function text", MaxMessageText, false) != nil {
		return invalidTool()
	}
	for _, p := range o.Content {
		switch p.Kind {
		case FunctionText:
			if p.Text == nil || Text(*p.Text, "native function text", MaxMessageText, false) != nil || p.ReferenceKind != "" || p.Detail != nil {
				return invalidTool()
			}
		case FunctionImage:
			if p.Text != nil || (p.ReferenceKind != FunctionImageURL && p.ReferenceKind != FunctionFileID) || p.Detail != nil && *p.Detail != FunctionDetailAuto && *p.Detail != FunctionDetailLow && *p.Detail != FunctionDetailHigh && *p.Detail != FunctionDetailOriginal {
				return invalidTool()
			}
		case FunctionAudio:
			if p.Text != nil || p.Detail != nil || p.ReferenceKind != FunctionAudioURL {
				return invalidTool()
			}
		case FunctionEncrypted:
			if p.Text != nil || p.Detail != nil || p.ReferenceKind != "" {
				return invalidTool()
			}
		default:
			return invalidTool()
		}
	}
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > 512<<10 {
		return invalidTool()
	}
	return nil
}
func (o FunctionOutputUpdate) Validate() error {
	if o.ID.Validate() != nil || Text(o.NativeID, "native function output identity", 1024, true) != nil || o.Stage != FunctionOutputStarted && o.Stage != FunctionOutputCompleted {
		return invalidTool()
	}
	return o.Output.Validate()
}
