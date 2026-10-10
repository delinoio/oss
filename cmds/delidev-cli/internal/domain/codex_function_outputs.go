// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
	"strings"
)

const CodexFunctionOutputV1 WorkerCapability = "codex-function-output-v1"
const MaxCodexFunctionContents = 128

type CodexFunctionOutputStage string

const (
	CodexFunctionOutputStarted   CodexFunctionOutputStage = "started"
	CodexFunctionOutputCompleted CodexFunctionOutputStage = "completed"
)

type CodexFunctionOutputVariant string

const (
	CodexFunctionString   CodexFunctionOutputVariant = "string"
	CodexFunctionContents CodexFunctionOutputVariant = "contents"
)

type CodexFunctionContentKind string

const (
	CodexFunctionText      CodexFunctionContentKind = "input_text"
	CodexFunctionImage     CodexFunctionContentKind = "input_image"
	CodexFunctionAudio     CodexFunctionContentKind = "input_audio"
	CodexFunctionEncrypted CodexFunctionContentKind = "encrypted_content"
)

type CodexFunctionReferenceKind string

const (
	CodexFunctionImageURL CodexFunctionReferenceKind = "image_url"
	CodexFunctionFileID   CodexFunctionReferenceKind = "file_id"
	CodexFunctionAudioURL CodexFunctionReferenceKind = "audio_url"
)

// References are presence metadata only. Original URLs, file identifiers and
// encrypted bytes belong exclusively to the protected native history.
type CodexFunctionContent struct {
	Type             CodexFunctionContentKind   `json:"type"`
	Text             *string                    `json:"text,omitempty"`
	ReferenceKind    CodexFunctionReferenceKind `json:"reference_kind,omitempty"`
	ReferencePresent bool                       `json:"reference_present,omitempty"`
	Detail           *string                    `json:"detail,omitempty"`
	Present          bool                       `json:"present,omitempty"`
}
type CodexFunctionOutputBody struct {
	Variant  CodexFunctionOutputVariant `json:"variant"`
	Text     *string                    `json:"text,omitempty"`
	Contents []CodexFunctionContent     `json:"contents,omitempty"`
}

// Empty structured output retains [] instead of becoming an absent variant.
func (v CodexFunctionOutputBody) MarshalJSON() ([]byte, error) {
	if v.Variant == CodexFunctionContents {
		return json.Marshal(struct {
			Variant  CodexFunctionOutputVariant `json:"variant"`
			Contents []CodexFunctionContent     `json:"contents"`
		}{v.Variant, v.Contents})
	}
	type plain CodexFunctionOutputBody
	return json.Marshal(plain(v))
}

type CodexFunctionOutput struct {
	Version   uint32                   `json:"version"`
	ID        ID                       `json:"id"`
	NativeID  string                   `json:"native_id"`
	Name      string                   `json:"name"`
	Namespace *string                  `json:"namespace"`
	Stage     CodexFunctionOutputStage `json:"stage"`
	Output    CodexFunctionOutputBody  `json:"output"`
}

func (v *CodexFunctionOutput) UnmarshalJSON(raw []byte) error {
	type plain CodexFunctionOutput
	var value plain
	if err := Decode(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["namespace"] == nil {
		return invalidCodexFunctionOutput()
	}
	*v = CodexFunctionOutput(value)
	return nil
}
func invalidCodexFunctionOutput() error {
	return Fail(Unsupported, "The native function output has an unsupported shape.", "Preserve the original protected history and inspect its supported observation profile.")
}
func (v CodexFunctionOutput) Validate() error {
	if v.Version != 1 || v.ID.Validate() != nil || Text(v.NativeID, "native function output identity", 1024, true) != nil || Text(v.Name, "native function name", 1024, true) != nil || v.Namespace != nil && Text(*v.Namespace, "native namespace", 1024, false) != nil || !slices.Contains([]CodexFunctionOutputStage{CodexFunctionOutputStarted, CodexFunctionOutputCompleted}, v.Stage) {
		return invalidCodexFunctionOutput()
	}
	return v.Output.Validate()
}
func (v CodexFunctionOutputBody) Validate() error {
	if v.Variant == CodexFunctionString {
		if v.Text == nil || v.Contents != nil || Text(*v.Text, "inert native function output", MaxMessageText, false) != nil {
			return invalidCodexFunctionOutput()
		}
		return nil
	}
	if v.Variant != CodexFunctionContents || v.Text != nil || v.Contents == nil || len(v.Contents) > MaxCodexFunctionContents {
		return invalidCodexFunctionOutput()
	}
	size := 0
	for _, c := range v.Contents {
		switch c.Type {
		case CodexFunctionText:
			if c.Text == nil || Text(*c.Text, "inert native function text", MaxMessageText, false) != nil || c.ReferenceKind != "" || c.ReferencePresent || c.Detail != nil || c.Present {
				return invalidCodexFunctionOutput()
			}
			size += len(*c.Text)
		case CodexFunctionImage:
			if c.Text != nil || !slices.Contains([]CodexFunctionReferenceKind{CodexFunctionImageURL, CodexFunctionFileID}, c.ReferenceKind) || !c.ReferencePresent || c.Present || c.Detail != nil && !slices.Contains([]string{"auto", "low", "high", "original"}, *c.Detail) {
				return invalidCodexFunctionOutput()
			}
		case CodexFunctionAudio:
			if c.Text != nil || c.ReferenceKind != CodexFunctionAudioURL || !c.ReferencePresent || c.Detail != nil || c.Present {
				return invalidCodexFunctionOutput()
			}
		case CodexFunctionEncrypted:
			if c.Text != nil || c.ReferenceKind != "" || c.ReferencePresent || c.Detail != nil || !c.Present {
				return invalidCodexFunctionOutput()
			}
		default:
			return invalidCodexFunctionOutput()
		}
	}
	if size > MaxMessageText {
		return invalidCodexFunctionOutput()
	}
	return nil
}
func (v CodexFunctionOutputBody) InertText() string {
	if v.Text != nil {
		return *v.Text
	}
	parts := []string{}
	for _, c := range v.Contents {
		if c.Text != nil {
			parts = append(parts, *c.Text)
		}
	}
	return strings.Join(parts, "")
}
