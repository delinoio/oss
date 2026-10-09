// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"slices"
)

// The pinned V2 UserInput union uses camelCase localImage, not textual paths.
// Source: openai/codex 0.151.0 d8673cb68e349c208659b986697773d3145dbb14
// and 0.159.2 8b9fa496bbf2c47aebd62e85a080b9a522a455b5,
// codex-rs/app-server-protocol/src/protocol/v2/turn.rs.
func (c *Client) nativeImageParts(ctx context.Context, input domain.SessionInput) ([]nativeTextInput, error) {
	result := []nativeTextInput{}
	if input.Prompt != "" {
		result = append(result, nativeTextInput{Type: nativeText, Text: input.Prompt})
	}
	if len(input.Attachments) == 0 {
		return result, nil
	}
	if c.imageRoot == "" || c.imageMachine.Validate() != nil {
		return nil, unsupportedImageBeforeSend()
	}
	models, err := c.readModelList(ctx, true)
	if err != nil {
		return nil, unsupportedImageBeforeSend()
	}
	supported := false
	for _, model := range models {
		if model.Model == c.execution.settings.Model && slices.Contains(model.Modalities, "image") {
			supported = true
			break
		}
	}
	if !supported {
		return nil, unsupportedImageBeforeSend()
	}
	paths, err := (imageinput.Manager{Root: c.imageRoot}).Resolve(c.imageMachine, input.Attachments)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		result = append(result, nativeTextInput{Type: "localImage", Path: path})
	}
	return result, nil
}
func decodeNativeInputParts(parts []json.RawMessage, lookup func(string) (domain.ImageAttachment, error)) (domain.SessionInput, error) {
	result := domain.SessionInput{Mode: domain.ExecuteMode}
	if len(parts) == 0 || len(parts) > domain.MaxInputImages+1 {
		return result, incompatible()
	}
	images := false
	hasText := false
	for _, raw := range parts {
		var part struct {
			Type     string            `json:"type"`
			Text     *string           `json:"text"`
			Elements []json.RawMessage `json:"text_elements"`
			Path     *string           `json:"path"`
			Detail   *string           `json:"detail"`
		}
		if domain.Decode(raw, &part) != nil {
			return result, incompatible()
		}
		switch part.Type {
		case "text":
			if hasText || images || part.Text == nil || part.Path != nil || part.Detail != nil || len(part.Elements) != 0 {
				return result, incompatible()
			}
			result.Prompt = *part.Text
			hasText = true
		case "localImage":
			if lookup == nil || part.Path == nil || part.Text != nil || part.Elements != nil || part.Detail != nil {
				return result, incompatible()
			}
			ref, err := lookup(*part.Path)
			if err != nil {
				return result, incompatible()
			}
			result.Attachments = append(result.Attachments, ref)
			images = true
		default:
			return result, incompatible()
		}
	}
	if result.Validate() != nil {
		return result, incompatible()
	}
	return result, nil
}
func (c *Client) nativeImageInput(parts []json.RawMessage) (domain.SessionInput, error) {
	return decodeNativeInputParts(parts, func(path string) (domain.ImageAttachment, error) {
		if c.imageRoot == "" || c.imageMachine.Validate() != nil {
			return domain.ImageAttachment{}, incompatible()
		}
		return (imageinput.Manager{Root: c.imageRoot}).Lookup(c.imageMachine, path)
	})
}

// This private error is emitted only by image preparation before turn/start.
// A generic Unsupported error elsewhere cannot acquire this provenance.
type imageBeforeSendError struct{ cause error }

func (e *imageBeforeSendError) Error() string { return e.cause.Error() }
func (e *imageBeforeSendError) Unwrap() error { return e.cause }
func unsupportedImageBeforeSend() error {
	return &imageBeforeSendError{cause: domain.UnsupportedImageInput()}
}

type imageRejectionProof struct {
	request domain.ID
	input   domain.ID
	digest  [32]byte
}

// ProvesImageInputNotSent binds the original pre-wire rejection to the complete
// immutable input. It proves no turn/start send, never independent cleanup.
func (r TurnResult) ProvesImageInputNotSent(request, input domain.ID, digest [32]byte) bool {
	p := r.imageRejection
	return p != nil && r.TurnID == "" && r.RequestID == request && r.InputID == input && p.request == request && p.input == input && p.digest == digest
}
