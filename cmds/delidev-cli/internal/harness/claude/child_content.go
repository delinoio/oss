package claude

import "encoding/json"

// A forwarded child user/context message is native transcript content. It does
// not accept any queued product input, answer a question or start a root turn.
func (b *ExecutionBinding) observeChildInput(parent string, rawBlocks []json.RawMessage, agentType, description *string) ([]ContentEvent, error) {
	if !b.activeChildTask(parent) {
		return nil, lifecycleUncertain()
	}
	blocks := make([]NativeContentBlock, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		block, err := decodeContentBlock(raw)
		if err != nil {
			return nil, err
		}
		if !userContentBlock(block.Kind) {
			return nil, lifecycleUncertain()
		}
		blocks = append(blocks, block)
	}
	return []ContentEvent{{Kind: ChildInputObserved, ParentToolID: parent, Blocks: blocks, SubagentType: agentType, TaskDescription: description}}, nil
}

// Claude forwards completed child messages without the main loop's partial
// stream. Preserve the original block array as one snapshot; do not fabricate
// block deltas, provider indices or a main-loop message_stop. Several snapshots
// may share one provider message ID while their native envelope UUIDs differ.
func (b *ExecutionBinding) observeChildSnapshot(parent string, message providerMessage, agentType, description *string) ([]ContentEvent, error) {
	if !b.activeChildTask(parent) || len(message.Content) == 0 {
		return nil, lifecycleUncertain()
	}
	key := parent + "\x00" + message.ID
	if b.content.snapshots == nil {
		b.content.snapshots = map[string]string{}
	}
	if model, ok := b.content.snapshots[key]; ok {
		if model != message.Model {
			return nil, lifecycleUncertain()
		}
	} else if b.content.seen[key] || len(b.content.seen) >= 4096 {
		return nil, lifecycleUncertain()
	}
	blocks := make([]NativeContentBlock, 0, len(message.Content))
	tools := map[string]nativeToolState{}
	for _, raw := range message.Content {
		block, err := decodeContentBlock(raw)
		if err != nil {
			return nil, err
		}
		if !assistantBlock(block.Kind) {
			return nil, lifecycleUncertain()
		}
		if block.Tool != nil {
			tool := block.Tool
			if !b.advertisedTools[tool.Name] || b.content.tools[tool.ID].name != "" || tools[tool.ID].name != "" {
				return nil, lifecycleUncertain()
			}
			digest, err := streamReplyDigest(tool.Input)
			if err != nil {
				return nil, err
			}
			ownerInput, ownerTurn := b.contentOwner(parent)
			tools[tool.ID] = nativeToolState{ownerInput: ownerInput, ownerTurn: ownerTurn, name: tool.Name, parent: parent, message: message.ID, input: digest}
		}
		blocks = append(blocks, block)
	}
	if len(tools)+b.content.openTools > 128 || len(tools)+len(b.content.tools) > 4096 {
		return nil, lifecycleUncertain()
	}
	// All sibling content and ownership checks succeed before any child tool
	// can become a callback target. Returned display values are independent.
	for id, tool := range tools {
		b.content.tools[id] = tool
	}
	b.content.openTools += len(tools)
	b.content.snapshots[key] = message.Model
	b.content.seen[key] = true
	if b.logger != nil {
		b.logger.Debug("Claude Code child message observed", "owner_id", b.owner, "blocks", len(blocks), "tools", len(tools))
	}
	return []ContentEvent{{Kind: ProviderMessageSnapshot, MessageID: message.ID, Model: message.Model, ParentToolID: parent, Blocks: blocks, SubagentType: agentType, TaskDescription: description, Usage: message.Usage, StopReason: message.Stop, StopSequence: message.Sequence}}, nil
}
