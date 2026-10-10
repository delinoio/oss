package domain

import (
	"encoding/hex"
	"encoding/json"
	"slices"
)

const CodexDynamicTool ToolKind = "codex-dynamic"

type DynamicContentKind string

const (
	DynamicText  DynamicContentKind = "inputText"
	DynamicImage DynamicContentKind = "inputImage"
	DynamicAudio DynamicContentKind = "inputAudio"
)

// Media references remain inert commitments. Their original URLs/bytes belong
// to protected native history, never a product fetch or attachment claim.
type DynamicContent struct {
	Kind   DynamicContentKind `json:"kind"`
	Text   *string            `json:"text,omitempty"`
	Digest string             `json:"digest,omitempty"`
}
type CodexDynamicObservation struct {
	Namespace     *string          `json:"namespace"`
	Tool          string           `json:"tool"`
	ArgumentsJSON string           `json:"arguments_json"`
	ContentItems  []DynamicContent `json:"content_items"`
	Success       *bool            `json:"success"`
	DurationMS    *int64           `json:"duration_ms"`
}

func (v CodexDynamicObservation) Validate(status ToolStatus) error {
	if !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed}, status) || Text(v.Tool, "native dynamic tool", 1024, true) != nil || (v.Namespace != nil && Text(*v.Namespace, "native dynamic namespace", 1024, true) != nil) || len(v.ArgumentsJSON) > MaxMessageText || !validDynamicArguments(v.ArgumentsJSON) || len(v.ContentItems) > 128 || (v.DurationMS != nil && *v.DurationMS < 0) {
		return invalidTool()
	}
	for _, item := range v.ContentItems {
		switch item.Kind {
		case DynamicText:
			if item.Text == nil || item.Digest != "" || Text(*item.Text, "native dynamic text", MaxMessageText, false) != nil {
				return invalidTool()
			}
		case DynamicImage, DynamicAudio:
			if item.Text != nil || !dynamicDigest(item.Digest) {
				return invalidTool()
			}
		default:
			return invalidTool()
		}
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > MaxMessageText {
		return invalidTool()
	}
	return nil
}
func ValidateCodexDynamicTransition(prior, next ToolSnapshot) error {
	if prior.Kind != CodexDynamicTool || next.Kind != prior.Kind || prior.Validate() != nil || next.Validate() != nil || prior.Status != ToolRunning || next.Status == ToolRunning {
		return invalidTool()
	}
	a, b := prior.Dynamic, next.Dynamic
	if a.Tool != b.Tool || a.ArgumentsJSON != b.ArgumentsJSON || (a.Namespace == nil) != (b.Namespace == nil) || a.Namespace != nil && *a.Namespace != *b.Namespace {
		return invalidTool()
	}
	return nil
}

func dynamicDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == hex.EncodeToString(raw)
}

func validDynamicArguments(raw string) bool {
	var value any
	return DecodeBounded([]byte(raw), &value, MaxMessageText) == nil
}
