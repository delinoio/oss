// SPDX-License-Identifier: Apache-2.0
package codex

import "encoding/json"

// Codex 0.159.2 restricts its built-in image executor to ChatGPT/actor auth.
// Register the supported native dynamic-tool interface for this selected API
// backend instead of changing native identity or pretending to have actor auth.
// The spec is persisted by native thread creation and retained on native resume.
func openRouterImageTools() []json.RawMessage {
	return []json.RawMessage{json.RawMessage(`{"type":"namespace","name":"delidev_image","description":"Image generation through the original OpenRouter account.","tools":[{"type":"function","name":"imagegen","description":"Generate or edit a PNG image. The code-mode result is a PNG data URL string. Display it with image(result), not generatedImage(result). Use referenced_image_paths for existing local image files, or num_last_images_to_include for retained image results; do not combine them.","inputSchema":{"type":"object","additionalProperties":false,"properties":{"prompt":{"type":"string"},"transparent_background":{"type":"boolean"},"referenced_image_paths":{"type":"array","maxItems":5,"items":{"type":"string"}},"num_last_images_to_include":{"type":"integer","minimum":1,"maximum":5}},"required":["prompt"]}}]}`)}
}
