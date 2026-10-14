package worker

import (
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
)

// openCodeClaimImages maps the immutable ordered attachment references to the
// path-free identities that bind the native input claim digest.
func openCodeClaimImages(attachments []domain.ImageAttachment) []opencode.InputImage {
	if len(attachments) == 0 {
		return nil
	}
	images := make([]opencode.InputImage, len(attachments))
	for i, ref := range attachments {
		images[i] = opencode.InputImage{MediaType: ref.MediaType, SHA256: ref.SHA256, Bytes: ref.ByteLength}
	}
	return images
}

// openCodeInputImages resolves the validated private Worker bytes of an input in
// attachment order. Only counts, media types and digests are logged; names and
// private paths never are.
func openCodeInputImages(root string, input domain.ExecutionJobInput, logger *slog.Logger) ([]opencode.InputImage, error) {
	images := openCodeClaimImages(input.Input.Attachments)
	if len(images) == 0 {
		return nil, nil
	}
	if input.Configuration.Harness != domain.OpenCode || !input.Configuration.ImageInputDeclared || input.Configuration.SidechatPolicy != "" || domain.ValidateImageAttachmentsForHarness(domain.OpenCode, input.Input.Attachments) != nil {
		return nil, domain.UnsupportedImageInput()
	}
	paths, err := (imageinput.Manager{Root: root}).Resolve(input.MachineID, input.Input.Attachments)
	if err != nil {
		return nil, err
	}
	media := make([]string, len(images))
	digests := make([]string, len(images))
	for i := range images {
		images[i].Path, media[i], digests[i] = paths[i], string(images[i].MediaType), images[i].SHA256
	}
	if logger != nil {
		logger.Info("opencode_image_input_resolved", "execution_id", input.ExecutionID, "image_count", len(images), "image_media_types", media, "image_sha256", digests)
	}
	return images, nil
}
