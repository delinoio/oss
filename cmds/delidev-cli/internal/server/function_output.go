// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Result evidence has its own item lifecycle. It never creates a dispatch,
// approval, tool-success, turn-completion or credential-cleanup proof.
func publishFunctionOutput(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	update := event.FunctionOutput
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil || input.Configuration.Harness != domain.Codex || !machineCapabilityContains(machine.WorkerCapabilities, domain.CodexFunctionOutputsV1) || update == nil || update.Validate() != nil {
		return executionEventConflict()
	}
	value := domain.ExecutionMessage{ContextRevision: input.ContextRevision, ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, Role: domain.ToolMessage, Text: "", State: domain.MessageStreaming, FirstSequence: event.Sequence, FunctionOutput: &update.Output}
	revision := uint64(0)
	prior, err := tx.Get(domain.MessageKind, update.ID)
	if err == nil {
		saved, decodeErr := store.Decode[domain.ExecutionMessage](prior)
		if decodeErr != nil || prior.SessionID != session.ID || saved.ExecutionID != input.ExecutionID || saved.NativeThreadID != event.NativeThreadID || saved.NativeTurnID != event.NativeTurnID || saved.NativeID != update.NativeID || saved.Role != domain.ToolMessage || saved.State != domain.MessageStreaming || saved.Tool != nil || saved.FunctionOutput == nil || !sameFunctionOutput(saved.FunctionOutput, &update.Output) || update.Stage != domain.FunctionOutputCompleted {
			return executionEventConflict()
		}
		revision = prior.Revision
		value = saved
	} else if domain.SafeError(err).Code != domain.NotFound {
		return err
	}
	if update.Stage == domain.FunctionOutputCompleted {
		value.State = domain.MessageComplete
	}
	value.LastSequence = event.Sequence
	if _, err := tx.Put(domain.MessageKind, update.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, update.ID, event.NativeThreadID, event.NativeTurnID, update.NativeID, value.State)
}

func sameFunctionOutput(a, b *domain.FunctionOutput) bool {
	left, le := json.Marshal(a)
	right, re := json.Marshal(b)
	return le == nil && re == nil && bytes.Equal(left, right)
}
