// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishDynamicTool(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	update := event.DynamicTool
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil || !input.NativeDynamicTools || input.Configuration.Harness != domain.Codex || !machineCapabilityContains(machine.WorkerCapabilities, domain.CodexDynamicToolsV1) || update == nil || update.Validate() != nil || input.Configuration.SidechatPolicy != "" && (update.Stage == domain.DynamicRequested || update.Stage == domain.DynamicReplied || update.Stage == domain.DynamicResolved) {
		return executionEventConflict()
	}
	value := domain.ExecutionMessage{ContextRevision: input.ContextRevision, ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, Role: domain.ToolMessage, Text: "", State: domain.MessageStreaming, FirstSequence: event.Sequence}
	revision := uint64(0)
	var original *domain.DynamicTool
	prior, err := tx.Get(domain.MessageKind, update.ID)
	if err == nil {
		saved, decodeErr := store.Decode[domain.ExecutionMessage](prior)
		if decodeErr != nil || prior.SessionID != session.ID || saved.ExecutionID != input.ExecutionID || saved.NativeThreadID != event.NativeThreadID || saved.NativeTurnID != event.NativeTurnID || saved.NativeID != update.NativeID || saved.Role != domain.ToolMessage || saved.Text != "" || saved.DynamicTool == nil || saved.Tool != nil {
			return executionEventConflict()
		}
		value = saved
		revision = prior.Revision
		original = saved.DynamicTool
	} else if domain.SafeError(err).Code != domain.NotFound {
		return err
	} else if update.Stage != domain.DynamicStarted && update.Stage != domain.DynamicCompleted && update.Stage != domain.DynamicRequested {
		return executionEventConflict()
	}
	if update.Request != nil {
		unique, err := tx.DynamicRequestUnique(session.ID, input.ExecutionID, update.ID, *update.Request)
		if err != nil {
			return err
		}
		if !unique {
			return executionEventConflict()
		}
	}
	if update.Reply != nil && !update.Reply.Unavailable {
		if !input.NativeOpenRouterTools || original == nil || original.Request == nil || original.Request.Identity.Tool != "imagegen" || original.Request.Identity.Namespace == nil || *original.Request.Identity.Namespace != "delidev_image" {
			return executionEventConflict()
		}
	}
	next, err := domain.ApplyDynamicTool(original, *update)
	if err != nil {
		return executionEventConflict()
	}
	value.DynamicTool = &next
	if next.Snapshot != nil && next.Snapshot.Status != "inProgress" {
		value.State = domain.MessageComplete
	}
	value.LastSequence = event.Sequence
	if _, err := tx.Put(domain.MessageKind, update.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, update.ID, event.NativeThreadID, event.NativeTurnID, update.NativeID, value.State)
}
