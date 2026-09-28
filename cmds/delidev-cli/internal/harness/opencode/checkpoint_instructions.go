package opencode

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func checkpointReadMetadata(raw json.RawMessage) (domain.OpenCodeReadMetadata, bool) {
	var metadata domain.OpenCodeReadMetadata
	if domain.Decode(raw, &metadata) != nil || metadata.Preview == nil || metadata.Truncated == nil || *metadata.Truncated || metadata.Loaded == nil || len(metadata.Loaded) > 1024 || metadata.Interrupted != nil && *metadata.Interrupted {
		return metadata, false
	}
	for _, path := range metadata.Loaded {
		if domain.Text(path, "native loaded instruction", 32768, false) != nil {
			return metadata, false
		}
	}
	return metadata, true
}

// The pinned native loader derives already-loaded paths from completed,
// uncompacted Read history. Its message-local in-flight claims are irrelevant
// after closure. Retain only this positive marker: the original complete part
// digest already binds the exact paths and inline instruction text. Paths do
// not grant permission to read current files or reconstruct missing history.
func checkpointLoadedInstructions(tool *NativeToolPart) bool {
	if tool == nil || tool.Name != string(checkpointReadTool) || tool.State != ToolCompleted {
		return false
	}
	metadata, valid := checkpointReadMetadata(tool.Metadata)
	return valid && len(metadata.Loaded) != 0
}
