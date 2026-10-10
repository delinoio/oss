// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
)

const FunctionOutputArtifact ArtifactKind = "function-call-output"

type FunctionOutputVariant string
type FunctionOutputPartKind string
type FunctionOutputReferenceKind string
type FunctionOutputImageDetail string

const (
	FunctionOutputString         FunctionOutputVariant       = "string"
	FunctionOutputStructured     FunctionOutputVariant       = "structured"
	FunctionOutputText           FunctionOutputPartKind      = "input_text"
	FunctionOutputImage          FunctionOutputPartKind      = "input_image"
	FunctionOutputAudio          FunctionOutputPartKind      = "input_audio"
	FunctionOutputEncrypted      FunctionOutputPartKind      = "encrypted_content"
	FunctionOutputImageURL       FunctionOutputReferenceKind = "image_url"
	FunctionOutputFileID         FunctionOutputReferenceKind = "file_id"
	FunctionOutputAudioURL       FunctionOutputReferenceKind = "audio_url"
	FunctionOutputDetailAuto     FunctionOutputImageDetail   = "auto"
	FunctionOutputDetailLow      FunctionOutputImageDetail   = "low"
	FunctionOutputDetailHigh     FunctionOutputImageDetail   = "high"
	FunctionOutputDetailOriginal FunctionOutputImageDetail   = "original"
)

// Media reference values and encrypted bytes belong exclusively to original
// protected native history. This ordered projection has no retrieval authority.
type FunctionOutputPart struct {
	Kind      FunctionOutputPartKind      `json:"kind"`
	Text      *string                     `json:"text,omitempty"`
	Reference FunctionOutputReferenceKind `json:"reference,omitempty"`
	Detail    *FunctionOutputImageDetail  `json:"detail,omitempty"`
}
type FunctionOutputObservation struct {
	Name      string                `json:"name"`
	Namespace *string               `json:"namespace"`
	Variant   FunctionOutputVariant `json:"variant"`
	Text      *string               `json:"text,omitempty"`
	Parts     []FunctionOutputPart  `json:"parts"`
}

func (v FunctionOutputObservation) Validate() error {
	if Text(v.Name, "native function name", 1024, true) != nil || v.Namespace != nil && Text(*v.Namespace, "native function namespace", 1024, false) != nil {
		return invalidArtifact()
	}
	switch v.Variant {
	case FunctionOutputString:
		if v.Text == nil || v.Parts != nil || Text(*v.Text, "native function output", MaxMessageText, false) != nil {
			return invalidArtifact()
		}
	case FunctionOutputStructured:
		if v.Text != nil || v.Parts == nil || len(v.Parts) > MaxArtifactParts {
			return invalidArtifact()
		}
		for _, p := range v.Parts {
			switch p.Kind {
			case FunctionOutputText:
				if p.Text == nil || Text(*p.Text, "native function text", MaxMessageText, false) != nil || p.Reference != "" || p.Detail != nil {
					return invalidArtifact()
				}
			case FunctionOutputImage:
				if p.Text != nil || !slices.Contains([]FunctionOutputReferenceKind{FunctionOutputImageURL, FunctionOutputFileID}, p.Reference) || p.Detail != nil && !slices.Contains([]FunctionOutputImageDetail{FunctionOutputDetailAuto, FunctionOutputDetailLow, FunctionOutputDetailHigh, FunctionOutputDetailOriginal}, *p.Detail) {
					return invalidArtifact()
				}
			case FunctionOutputAudio:
				if p.Text != nil || p.Reference != FunctionOutputAudioURL || p.Detail != nil {
					return invalidArtifact()
				}
			case FunctionOutputEncrypted:
				if p.Text != nil || p.Reference != "" || p.Detail != nil {
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

// A result may evolve between lifecycle observations, but its original function
// identity and output union cannot change or become a dynamic tool invocation.
func SameFunctionOutputIdentity(a, b *FunctionOutputObservation) bool {
	if a == nil || b == nil || a.Name != b.Name || a.Variant != b.Variant || (a.Namespace == nil) != (b.Namespace == nil) {
		return false
	}
	return a.Namespace == nil || *a.Namespace == *b.Namespace
}
func (v FunctionOutputObservation) Identity() string {
	raw, _ := json.Marshal(struct {
		Name      string
		Namespace *string
		Variant   FunctionOutputVariant
	}{v.Name, v.Namespace, v.Variant})
	return string(raw)
}

func (v *FunctionOutputObservation) UnmarshalJSON(raw []byte) error {
	type wire FunctionOutputObservation
	var decoded wire
	var fields map[string]json.RawMessage
	if Decode(raw, &decoded) != nil || Decode(raw, &fields) != nil {
		return invalidArtifact()
	}
	for _, key := range []string{"name", "namespace", "variant", "parts"} {
		if _, exists := fields[key]; !exists {
			return invalidArtifact()
		}
	}
	*v = FunctionOutputObservation(decoded)
	return v.Validate()
}
