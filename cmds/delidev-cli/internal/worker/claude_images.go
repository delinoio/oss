// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"log/slog"
)

func claudeInputImages(root string, input domain.ExecutionJobInput, logger *slog.Logger) ([]claude.ImageInput, error) {
	if len(input.Input.Attachments) == 0 {
		return nil, nil
	}
	if input.Configuration.Harness != domain.ClaudeCode || input.ValidateImages() != nil {
		return nil, domain.UnsupportedImageInput()
	}
	paths, err := (imageinput.Manager{Root: root}).Resolve(input.MachineID, input.Input.Attachments)
	if err != nil {
		return nil, err
	}
	images := make([]claude.ImageInput, len(paths))
	for i, path := range paths {
		raw, err := security.ReadPrivate(path, domain.MaxInputImageBytes)
		if err != nil {
			return nil, err
		}
		ref := input.Input.Attachments[i]
		if err := ref.ValidateContent(raw); err != nil {
			return nil, err
		}
		images[i] = claude.ImageInput{Reference: ref, Bytes: raw}
	}
	if logger != nil {
		logger.Info("claude_image_input_resolved", "execution_id", input.ExecutionID, "image_count", len(images))
	}
	return images, nil
}
