package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type nativeWriteKind string

const (
	nativeWriteCreate nativeWriteKind = "create"
	nativeWriteUpdate nativeWriteKind = "update"
)

// This is a native result observation, never an instruction to apply a patch.
// Keep every original hunk field and line in the independently pinned digest.
func validNativeFilePatches(patches *[]json.RawMessage) bool {
	if patches == nil || *patches == nil || len(*patches) > 4096 {
		return false
	}
	for _, raw := range *patches {
		var hunk struct {
			OldStart *uint32    `json:"oldStart"`
			OldLines *uint32    `json:"oldLines"`
			NewStart *uint32    `json:"newStart"`
			NewLines *uint32    `json:"newLines"`
			Lines    *[]*string `json:"lines"`
		}
		if decodeNativeObject(raw, &hunk) != nil || hunk.OldStart == nil || hunk.OldLines == nil || hunk.NewStart == nil || hunk.NewLines == nil || hunk.Lines == nil || *hunk.Lines == nil {
			return false
		}
		for _, line := range *hunk.Lines {
			if line == nil {
				return false
			}
		}
	}
	return true
}

func inlineWriteMetadata(raw []byte) ([sha256.Size]byte, bool) {
	var value struct {
		Kind     nativeWriteKind    `json:"type"`
		Path     *string            `json:"filePath"`
		Content  *string            `json:"content"`
		Original json.RawMessage    `json:"originalFile"`
		Patch    *[]json.RawMessage `json:"structuredPatch"`
		Modified *bool              `json:"userModified"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Path == nil || domain.Text(*value.Path, "native Write result path", 4096, true) != nil || value.Content == nil || !validNativeFilePatches(value.Patch) || value.Modified == nil || *value.Modified {
		return [sha256.Size]byte{}, false
	}
	switch value.Kind {
	case nativeWriteCreate:
		if !bytes.Equal(bytes.TrimSpace(value.Original), []byte("null")) || len(*value.Patch) != 0 {
			return [sha256.Size]byte{}, false
		}
	case nativeWriteUpdate:
		var original *string
		if json.Unmarshal(value.Original, &original) != nil || original == nil {
			return [sha256.Size]byte{}, false
		}
	default:
		return [sha256.Size]byte{}, false
	}
	digest, err := streamReplyDigest(raw)
	return digest, err == nil
}

func inlineEditMetadata(raw []byte) ([sha256.Size]byte, bool) {
	var value struct {
		Path       *string            `json:"filePath"`
		Old        *string            `json:"oldString"`
		New        *string            `json:"newString"`
		Original   *string            `json:"originalFile"`
		Patch      *[]json.RawMessage `json:"structuredPatch"`
		Modified   *bool              `json:"userModified"`
		ReplaceAll *bool              `json:"replaceAll"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Path == nil || domain.Text(*value.Path, "native Edit result path", 4096, true) != nil || value.Old == nil || value.New == nil || value.Original == nil || !validNativeFilePatches(value.Patch) || value.Modified == nil || *value.Modified || value.ReplaceAll == nil {
		return [sha256.Size]byte{}, false
	}
	digest, err := streamReplyDigest(raw)
	return digest, err == nil
}
