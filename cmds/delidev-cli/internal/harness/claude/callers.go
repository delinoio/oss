package claude

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type NativeCallerKind string

const (
	DirectCaller       NativeCallerKind = "direct"
	CodeCaller20250825 NativeCallerKind = "code_execution_20250825"
	CodeCaller20260120 NativeCallerKind = "code_execution_20260120"
)

// Caller describes original provider ancestry, independently of the native
// Agent parent and local callback authority. The zero value means omitted.
type NativeToolCaller struct {
	Kind   NativeCallerKind `json:"type"`
	ToolID string           `json:"tool_id,omitempty"`
}

func decodeToolCaller(raw json.RawMessage) (NativeToolCaller, error) {
	if len(raw) == 0 {
		return NativeToolCaller{}, nil
	}
	var wire struct {
		Kind   NativeCallerKind `json:"type"`
		ToolID *string          `json:"tool_id"`
	}
	var fields map[string]json.RawMessage
	if decodeNativeObject(raw, &wire) != nil || json.Unmarshal(raw, &fields) != nil {
		return NativeToolCaller{}, lifecycleUncertain()
	}
	switch wire.Kind {
	case DirectCaller:
		if fields["tool_id"] != nil {
			return NativeToolCaller{}, lifecycleUncertain()
		}
		return NativeToolCaller{Kind: wire.Kind}, nil
	case CodeCaller20250825, CodeCaller20260120:
		if wire.ToolID == nil || domain.Text(*wire.ToolID, "native caller tool identity", 1024, true) != nil {
			return NativeToolCaller{}, lifecycleUncertain()
		}
		return NativeToolCaller{Kind: wire.Kind, ToolID: *wire.ToolID}, nil
	default:
		return NativeToolCaller{}, domain.Fail(domain.Unsupported, "The Claude Code tool caller is not supported by its pinned native profile.", "Retain the original provider operation without substituting a direct call.")
	}
}

func (b *ExecutionBinding) validateToolCaller(caller NativeToolCaller, parent, message string, changes map[string]serverToolState) error {
	if caller.ToolID == "" {
		return nil
	}
	tool, ok := changes[caller.ToolID]
	if !ok {
		tool, ok = b.content.serverTools[caller.ToolID]
	}
	if !ok || tool.name != ServerCodeExecution || tool.finished || tool.parent != parent || tool.currentMessage() != message || tool.continuation {
		return lifecycleUncertain()
	}
	return nil
}

func (s serverToolState) currentMessage() string {
	if s.activeMessage != "" {
		return s.activeMessage
	}
	return s.message
}

// Only the original provider stop can make unfinished server work eligible
// for the immediately following native message. It is not a product retry,
// input acceptance, task-notification turn or replacement-process resume.
func (b *ExecutionBinding) suspendServerMessage(message *providerMessageState) {
	if message.stop == nil || (*message.stop != "pause_turn" && *message.stop != "tool_use") {
		return
	}
	changes := map[string]serverToolState{}
	for id, tool := range b.content.serverTools {
		if tool.finished || tool.parent != message.parent {
			continue
		}
		if tool.currentMessage() != message.id || tool.continuation || (*message.stop == "tool_use" && !b.hasPendingProgrammaticLocal(id)) {
			return
		}
		tool.continuation = true
		changes[id] = tool
	}
	for id, tool := range changes {
		b.content.serverTools[id] = tool
	}
}

func (b *ExecutionBinding) hasPendingProgrammaticLocal(id string) bool {
	for _, local := range b.content.tools {
		if local.finished {
			continue
		}
		caller := local.caller.ToolID
		for depth := 0; caller != "" && depth < 128; depth++ {
			if caller == id {
				return true
			}
			caller = b.content.serverTools[caller].caller.ToolID
		}
	}
	return false
}

func (b *ExecutionBinding) continueServerMessage(parent, message string) error {
	changes := map[string]serverToolState{}
	for id, tool := range b.content.serverTools {
		if tool.finished || tool.parent != parent {
			continue
		}
		if !tool.continuation || b.hasPendingProgrammaticLocal(id) {
			return lifecycleUncertain()
		}
		tool.activeMessage, tool.continuation = message, false
		changes[id] = tool
	}
	for id, tool := range changes {
		b.content.serverTools[id] = tool
	}
	if len(changes) != 0 && b.logger != nil {
		b.logger.Debug("Claude Code provider operations continued", "owner_id", b.owner, "operations", len(changes))
	}
	return nil
}

func (b *ExecutionBinding) hasOpenProgrammaticChild(id string, changes map[string]serverToolState, local map[string]nativeToolState) bool {
	for key, tool := range b.content.serverTools {
		if changed, ok := changes[key]; ok {
			tool = changed
		}
		if tool.caller.ToolID == id && !tool.finished {
			return true
		}
	}
	for _, tool := range changes {
		if tool.caller.ToolID == id && !tool.finished {
			return true
		}
	}
	for _, tools := range []map[string]nativeToolState{b.content.tools, local} {
		for _, tool := range tools {
			if tool.caller.ToolID == id && !tool.finished {
				return true
			}
		}
	}
	return false
}
