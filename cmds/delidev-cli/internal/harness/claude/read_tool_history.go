package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This first closed-tool profile covers only an inline text Read. Native
// file-result references, callbacks, children, media and writing tools require
// their own auxiliary-history evidence before they can replace a process.
type inlineReadEvidence struct {
	NativeID string
	Metadata [sha256.Size]byte
}

type checkpointReadTool struct {
	ID             string           `json:"id"`
	Input          domain.ID        `json:"input_id"`
	Turn           string           `json:"native_turn_id"`
	Message        string           `json:"provider_message_id"`
	Index          uint32           `json:"block_index"`
	InputDigest    string           `json:"input_sha256"`
	Caller         NativeCallerKind `json:"caller,omitempty"`
	Result         string           `json:"result_native_id"`
	MetadataDigest string           `json:"result_metadata_sha256"`
}

func inlineReadMetadata(raw []byte) ([sha256.Size]byte, bool) {
	var value struct {
		Type string `json:"type"`
		File *struct {
			Path    *string `json:"filePath"`
			Content *string `json:"content"`
			Lines   *uint32 `json:"numLines"`
			Start   *uint32 `json:"startLine"`
			Total   *uint32 `json:"totalLines"`
		} `json:"file"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Type != "text" || value.File == nil || value.File.Path == nil || domain.Text(*value.File.Path, "native Read result path", 4096, true) != nil || value.File.Content == nil || value.File.Lines == nil || value.File.Start == nil || value.File.Total == nil {
		return [sha256.Size]byte{}, false
	}
	digest, err := streamReplyDigest(raw)
	return digest, err == nil
}

func (b *ExecutionBinding) closedReadTools() ([]checkpointReadTool, error) {
	var result []checkpointReadTool
	if len(b.content.tools) > 4096 {
		return nil, continuationUnavailable()
	}
	for id, tool := range b.content.tools {
		if tool.name != "Read" || tool.parent != "" || !tool.finished || !tool.streamed || tool.read == nil || tool.caller.ToolID != "" || (tool.caller.Kind != "" && tool.caller.Kind != DirectCaller) {
			return nil, continuationUnavailable()
		}
		result = append(result, checkpointReadTool{ID: id, Input: tool.ownerInput, Turn: tool.ownerTurn, Message: tool.message, Index: tool.index, InputDigest: hex.EncodeToString(tool.input[:]), Caller: tool.caller.Kind, Result: tool.read.NativeID, MetadataDigest: hex.EncodeToString(tool.read.Metadata[:])})
	}
	slices.SortFunc(result, func(a, b checkpointReadTool) int { return strings.Compare(a.ID, b.ID) })
	return result, nil
}

func verifyReadToolHistory(ctx context.Context, raw []byte, tools []checkpointReadTool, messages []HistoryMessageProof) error {
	expected := map[string]checkpointReadTool{}
	results := map[string]checkpointReadTool{}
	for _, tool := range tools {
		if _, exists := expected[tool.ID]; exists {
			return historyUncertain()
		}
		if _, exists := results[tool.Result]; exists {
			return historyUncertain()
		}
		expected[tool.ID], results[tool.Result] = tool, tool
	}
	proofs := map[string]bool{}
	for _, proof := range messages {
		proofs[proof.NativeID] = true
	}
	seen := map[string]bool{}
	started, finished := map[string]bool{}, map[string]bool{}
	positions := map[string]uint32{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		var record struct {
			ID       string          `json:"uuid"`
			Kind     string          `json:"type"`
			Message  json.RawMessage `json:"message"`
			Metadata json.RawMessage `json:"toolUseResult"`
		}
		if json.Unmarshal(line, &record) != nil {
			return historyUncertain()
		}
		if !proofs[record.ID] || seen[record.ID] {
			continue
		}
		seen[record.ID] = true
		var message struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(record.Message, &message) != nil {
			return historyUncertain()
		}
		if record.Kind == "assistant" {
			var blocks []json.RawMessage
			if json.Unmarshal(message.Content, &blocks) != nil {
				return historyUncertain()
			}
			for _, raw := range blocks {
				var block struct {
					Type   string          `json:"type"`
					ID     string          `json:"id"`
					Name   string          `json:"name"`
					Input  json.RawMessage `json:"input"`
					Caller json.RawMessage `json:"caller"`
				}
				if json.Unmarshal(raw, &block) != nil {
					return historyUncertain()
				}
				index := positions[message.ID]
				positions[message.ID]++
				if block.Type != "tool_use" {
					continue
				}
				tool, exists := expected[block.ID]
				digest, err := streamReplyDigest(block.Input)
				caller, callerErr := decodeToolCaller(block.Caller)
				if !exists || started[tool.ID] || block.Name != "Read" || tool.Message != message.ID || tool.Index != index || err != nil || hex.EncodeToString(digest[:]) != tool.InputDigest || callerErr != nil || caller.Kind != tool.Caller || caller.ToolID != "" {
					return historyUncertain()
				}
				started[tool.ID] = true
			}
		} else if tool, exists := results[record.ID]; exists {
			var blocks []struct {
				Type    string  `json:"type"`
				ID      string  `json:"tool_use_id"`
				Error   *bool   `json:"is_error"`
				Content *string `json:"content"`
			}
			metadata, valid := inlineReadMetadata(record.Metadata)
			if record.Kind != "user" || !started[tool.ID] || finished[tool.ID] || json.Unmarshal(message.Content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "tool_result" || blocks[0].ID != tool.ID || (blocks[0].Error != nil && *blocks[0].Error) || blocks[0].Content == nil || strings.Contains(*blocks[0].Content, "<persisted-output>") || !valid || hex.EncodeToString(metadata[:]) != tool.MetadataDigest {
				return historyUncertain()
			}
			finished[tool.ID] = true
		}
	}
	if len(started) != len(tools) || len(finished) != len(tools) {
		return historyUncertain()
	}
	return nil
}
