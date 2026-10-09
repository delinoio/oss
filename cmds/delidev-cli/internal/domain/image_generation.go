// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

const NativeImageGenerationV1 WorkerCapability = "native-image-generation-v1"

type ImageGenerationStatus string

const (
	ImageGenerationRunning   ImageGenerationStatus = "in_progress"
	ImageGenerationCompleted ImageGenerationStatus = "completed"
	ImageGenerationFailed    ImageGenerationStatus = "failed"
)

// The original native call identity is ExecutionArtifactUpdate.NativeID. Backend
// generation IDs and image token/cost usage are not exposed by the pinned wire.
type ImageGenerationObservation struct {
	Status                ImageGenerationStatus   `json:"status"`
	RevisedPrompt         *string                 `json:"revised_prompt,omitempty"`
	TransparentBackground *bool                   `json:"transparent_background,omitempty"`
	Failure               *ImageGenerationFailure `json:"failure,omitempty"`
	Outputs               []ImageAttachment       `json:"outputs"`
}
type ImageGenerationFailureKind string

const ImageGenerationUsageLimitExceeded ImageGenerationFailureKind = "usageLimitExceeded"

type ImageGenerationFailure struct {
	Type     ImageGenerationFailureKind `json:"type"`
	LimitID  string                     `json:"limit_id"`
	ResetsAt *int64                     `json:"resets_at"`
}

func (v ImageGenerationObservation) Validate() error {
	if !slices.Contains([]ImageGenerationStatus{ImageGenerationRunning, ImageGenerationCompleted, ImageGenerationFailed}, v.Status) || v.Outputs == nil || ValidateImageAttachments(v.Outputs) != nil || v.RevisedPrompt != nil && Text(*v.RevisedPrompt, "native image revised prompt", MaxPromptBytes, false) != nil {
		return invalidArtifact()
	}
	if v.Failure != nil && (v.Failure.Type != "usageLimitExceeded" || Text(v.Failure.LimitID, "native image usage limit", 1024, true) != nil || v.Failure.ResetsAt != nil && *v.Failure.ResetsAt < 0) {
		return invalidArtifact()
	}
	switch v.Status {
	case ImageGenerationRunning:
		if len(v.Outputs) != 0 || v.RevisedPrompt != nil || v.TransparentBackground != nil || v.Failure != nil {
			return invalidArtifact()
		}
	case ImageGenerationCompleted:
		if len(v.Outputs) != 1 || v.Outputs[0].MediaType != ImagePNG || v.Failure != nil {
			return invalidArtifact()
		}
	case ImageGenerationFailed:
		if len(v.Outputs) != 0 || v.TransparentBackground != nil {
			return invalidArtifact()
		}
	}
	return nil
}
