// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Source: openai/codex c1382380de69521303b416720a52f42d51af6248,
// ext/items/src/image_generation.rs and ext/image-generation/src/tool.rs.
// Each call returns one base64 PNG. Backend IDs are deliberately not wire fields.
type ImageGeneration struct {
	ID          string
	Observation domain.ImageGenerationObservation
	Bytes       []byte `json:"-"`
}

func decodeImageGeneration(raw json.RawMessage, completed bool) (*ImageGeneration, error) {
	var item struct {
		Type                  string                       `json:"type"`
		ID                    string                       `json:"id"`
		Status                domain.ImageGenerationStatus `json:"status"`
		RevisedPrompt         *string                      `json:"revisedPrompt"`
		Result                *string                      `json:"result"`
		TransparentBackground *bool                        `json:"transparentBackground"`
		Failure               *struct {
			Type     domain.ImageGenerationFailureKind `json:"type"`
			LimitID  string                            `json:"limitId"`
			ResetsAt *int64                            `json:"resetsAt"`
		} `json:"failure"`
		SavedPath *string `json:"savedPath"`
	}
	if domain.DecodeBounded(raw, &item, 16<<20) != nil || item.Type != "imageGeneration" || domain.Text(item.ID, "native image identity", 1024, true) != nil || item.Result == nil || item.SavedPath != nil && (!filepath.IsAbs(*item.SavedPath) || domain.Text(*item.SavedPath, "native image path", 4096, true) != nil) {
		return nil, incompatible()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, incompatible()
	}
	for _, key := range []string{"type", "id", "status", "revisedPrompt", "result", "failure"} {
		if _, exists := fields[key]; !exists {
			return nil, incompatible()
		}
	}
	for _, key := range []string{"transparentBackground", "savedPath"} {
		if value, exists := fields[key]; exists && string(value) == "null" {
			return nil, incompatible()
		}
	}
	if item.Failure != nil {
		var failureFields map[string]json.RawMessage
		if json.Unmarshal(fields["failure"], &failureFields) != nil {
			return nil, incompatible()
		}
		for _, key := range []string{"type", "limitId", "resetsAt"} {
			if _, exists := failureFields[key]; !exists {
				return nil, incompatible()
			}
		}
	}
	result := &ImageGeneration{ID: item.ID, Observation: domain.ImageGenerationObservation{Status: item.Status, RevisedPrompt: item.RevisedPrompt, TransparentBackground: item.TransparentBackground, Outputs: []domain.ImageAttachment{}}}
	if item.Failure != nil {
		result.Observation.Failure = &domain.ImageGenerationFailure{Type: item.Failure.Type, LimitID: item.Failure.LimitID, ResetsAt: item.Failure.ResetsAt}
	}
	if !completed {
		if item.Status != domain.ImageGenerationRunning || *item.Result != "" || item.SavedPath != nil || result.Observation.Validate() != nil {
			return nil, incompatible()
		}
		return result, nil
	}
	switch item.Status {
	case domain.ImageGenerationCompleted:
		if item.Failure != nil || len(*item.Result) > base64.StdEncoding.EncodedLen(domain.MaxInputImageBytes) {
			return nil, incompatible()
		}
		bytes, err := base64.StdEncoding.Strict().DecodeString(*item.Result)
		if err != nil || domain.ValidateImageContent(bytes, domain.ImagePNG) != nil {
			return nil, incompatible()
		}
		result.Bytes = bytes
		// Reference allocation belongs to the durable original Worker controller.
		if item.RevisedPrompt != nil && domain.Text(*item.RevisedPrompt, "native image revised prompt", domain.MaxPromptBytes, false) != nil {
			return nil, incompatible()
		}
	case domain.ImageGenerationFailed:
		if *item.Result != "" || item.SavedPath != nil || result.Observation.Validate() != nil {
			return nil, incompatible()
		}
	default:
		return nil, incompatible()
	}
	return result, nil
}
func (c *Client) observeImageGeneration(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	// The original managed profile independently verifies built-in OpenAI account,
	// auth and effective provider. A feature flag or custom proxy is insufficient.
	if !c.imageGeneration || c.managedHome == "" || c.api != nil || c.sidechat != "" || c.imageRoot == "" || c.imageMachine.Validate() != nil {
		return Event{}, domain.Fail(domain.Unsupported, "The selected native provider cannot publish generated images.", "Use the original supported managed OpenAI account; no provider substitution is available.")
	}
	turn, known := c.execution.turns[turnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != turnID {
		return Event{}, incompatible()
	}
	completed := native.Method == "item/completed"
	image, err := decodeImageGeneration(raw, completed)
	if err != nil {
		return Event{}, err
	}
	kind := ImageGenerationStartedEvent
	if completed {
		kind = ImageGenerationCompletedEvent
	}
	return Event{Kind: kind, ThreadID: c.thread, TurnID: turnID, ItemID: image.ID, ImageGeneration: image, Correlated: true}, nil
}

func (c *Client) nativeFrameLimit() int {
	if c.managedHome != "" && c.api == nil && c.imageRoot != "" {
		return 16 << 20
	}
	return nativewire.MaxFrame
}

// Request support only for the admitted original managed route. The later config
// read verifies the request; actual account/provider proof remains independent.
func configureImageGeneration(config *Config) error {
	if config.EnableImageGeneration {
		if config.Mode != ThreadProtocol || !config.ManagedAuthentication || config.API != nil || config.Sidechat != "" || config.ImageRoot == "" || config.ImageMachineID.Validate() != nil {
			return incompatible()
		}
		config.Process.Args = append(config.Process.Args, "-c", "features.image_generation=true")
	} else if config.Mode == ThreadProtocol && config.ManagedAuthentication {
		config.Process.Args = append(config.Process.Args, "-c", "features.image_generation=false")
	}
	return nil
}
