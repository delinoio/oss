// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"reflect"
)

type dynamicCallRecord struct {
	descriptor domain.CodexDynamicTool
	lifecycle  *domain.CodexDynamicTool
	request    *domain.CodexDynamicTool
}

func sameDynamicDescriptor(a, b domain.CodexDynamicTool) bool {
	return a.CallID == b.CallID && a.Tool == b.Tool && reflect.DeepEqual(a.Namespace, b.Namespace) && a.Arguments == b.Arguments
}
func applyDynamicObservation(calls map[string]*dynamicCallRecord, requests map[string]string, arrivals map[domain.ID]string, value domain.CodexDynamicTool) error {
	if value.Validate() != nil {
		return executionEventConflict()
	}
	call := calls[value.CallID]
	if call == nil {
		if len(calls) >= 4096 {
			return executionEventConflict()
		}
		call = &dynamicCallRecord{descriptor: value}
		calls[value.CallID] = call
	} else if !sameDynamicDescriptor(call.descriptor, value) {
		return executionEventConflict()
	}
	switch value.Stage {
	case domain.DynamicToolStarted, domain.DynamicToolCompleted:
		if old := call.lifecycle; old != nil && old.Stage == domain.DynamicToolCompleted {
			a, b := *old, value
			a.ID = ""
			b.ID = ""
			if !reflect.DeepEqual(a, b) {
				return executionEventConflict()
			}
		}
		copy := value
		call.lifecycle = &copy
	case domain.DynamicToolRequested:
		key, _ := value.RequestID.Key()
		if call.request != nil || requests[key] != "" || arrivals[value.ArrivalID] != "" {
			return executionEventConflict()
		}
		requests[key] = value.CallID
		arrivals[value.ArrivalID] = value.CallID
		copy := value
		call.request = &copy
	case domain.DynamicToolReplied, domain.DynamicToolResolved:
		old := call.request
		if old == nil || old.ArrivalID != value.ArrivalID || !reflect.DeepEqual(old.RequestID, value.RequestID) {
			return executionEventConflict()
		}
		if value.Stage == domain.DynamicToolReplied {
			if *old.RequestResolved || old.ResponseID != "" && old.ResponseID != value.ResponseID || value.Delivery == domain.DynamicSendStarted && old.Delivery != domain.DynamicNotSent || value.Delivery != domain.DynamicSendStarted && old.Delivery != domain.DynamicSendStarted {
				return executionEventConflict()
			}
		} else if old.Delivery != value.Delivery || old.ResponseID != value.ResponseID || !reflect.DeepEqual(old.NegativeOutcome, value.NegativeOutcome) {
			return executionEventConflict()
		}
		copy := value
		call.request = &copy
	}
	return nil
}
func publishCodexDynamicTool(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	value := event.CodexDynamicTool
	if value == nil || input.Configuration.Harness != domain.Codex || input.Configuration.SidechatPolicy != "" || p.CodexDynamicObservations >= 4096 {
		return executionEventConflict()
	}
	journal, err := tx.CodexDynamicToolJournal(input.SessionID, input.ExecutionID)
	if err != nil {
		return err
	}
	calls := map[string]*dynamicCallRecord{}
	requests := map[string]string{}
	arrivals := map[domain.ID]string{}
	previous := uint64(0)
	for _, message := range journal {
		if message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.FirstSequence <= previous || message.LastSequence != message.FirstSequence || message.State != domain.MessageComplete || message.Inherited != nil || message.Role != domain.ToolMessage || message.CodexDynamicTool == nil {
			return executionEventConflict()
		}
		if err := applyDynamicObservation(calls, requests, arrivals, *message.CodexDynamicTool); err != nil {
			return err
		}
		previous = message.FirstSequence
	}
	if err := applyDynamicObservation(calls, requests, arrivals, *value); err != nil {
		return err
	}
	text := ""
	for _, content := range value.ContentItems {
		if content.Text != nil {
			text += *content.Text
		}
	}
	if value.NegativeOutcome != nil {
		text = domain.DynamicUnavailableText
	}
	// Complete denotes this immutable observation only. Native item completion,
	// original response delivery/resolution and root cleanup are separate fields.
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: value.CallID, Role: domain.ToolMessage, State: domain.MessageComplete, Text: text, FirstSequence: event.Sequence, LastSequence: event.Sequence, CodexDynamicTool: value}
	if _, err := tx.Put(domain.MessageKind, value.ID, 0, sr.ID, sr.ProjectID, message); err != nil {
		return err
	}
	p.CodexDynamicObservations++
	return nil
}
