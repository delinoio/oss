// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishCodexFunctionOutput(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, event domain.ExecutionEvent) error {
	v := event.CodexFunctionOutput
	if v == nil || v.Validate() != nil || input.Configuration.Harness != domain.Codex || input.Configuration.SidechatPolicy != "" {
		return executionEventConflict()
	}
	var revision uint64
	state := domain.MessageStreaming
	first := event.Sequence
	if v.Stage == domain.CodexFunctionOutputStarted {
		if exists, err := tx.HasExecutionNativeMessage(input.ExecutionID, v.NativeID); err != nil {
			return err
		} else if exists {
			return executionEventConflict()
		}
	} else {
		r, err := tx.Get(domain.MessageKind, v.ID)
		if err != nil {
			return err
		}
		previous, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil || r.SessionID != sr.ID || previous.ExecutionID != input.ExecutionID || previous.NativeThreadID != event.NativeThreadID || previous.NativeTurnID != event.NativeTurnID || previous.NativeID != v.NativeID || previous.Role != domain.ToolMessage || previous.State != domain.MessageStreaming || previous.CodexFunctionOutput == nil || previous.CodexFunctionOutput.Validate() != nil || previous.CodexFunctionOutput.Stage != domain.CodexFunctionOutputStarted || previous.CodexFunctionOutput.ID != v.ID || previous.CodexFunctionOutput.Name != v.Name || !sameCodexFunctionNamespace(previous.CodexFunctionOutput.Namespace, v.Namespace) {
			return executionEventConflict()
		}
		revision = r.Revision
		first = previous.FirstSequence
		state = domain.MessageComplete
	}
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: v.NativeID, Role: domain.ToolMessage, State: state, Text: v.Output.InertText(), FirstSequence: first, LastSequence: event.Sequence, CodexFunctionOutput: v}
	if _, err := tx.Put(domain.MessageKind, v.ID, revision, sr.ID, sr.ProjectID, message); err != nil {
		return err
	}
	return tx.BindExecutionMessage(sr.ID, input.ExecutionID, v.ID, event.NativeThreadID, event.NativeTurnID, v.NativeID, state)
}
func sameCodexFunctionNamespace(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
