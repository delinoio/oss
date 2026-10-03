// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestExecutionNativeCompactionJoinsOrderedPublicationAndBlocksEarlyTerminal(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	started := f.event(domain.ExecutionProgressObserved, 3)
	started.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.NativeCompactionProgress, Compaction: &domain.NativeCompactionObservation{Harness: domain.Codex, Trigger: domain.NativeAutomaticCompaction, Stage: domain.NativeCompactionStarted, NativeItemID: "original-context-item"}}}
	receipt := f.publish(t, started)
	if reply, err := f.call(receipt); err != nil || !reply.Msg.Replayed {
		t.Fatal("original context receipt did not replay without mutation", err)
	}
	for _, kind := range []string{"early-terminal", "foreign-item", "foreign-harness", "repeated-start", "foreign-turn"} {
		event := f.event(domain.ExecutionProgressObserved, 4)
		v := *started.Progress.Progress.Compaction
		v.Stage = domain.NativeCompactionCompleted
		event.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.NativeCompactionProgress, Compaction: &v}}
		switch kind {
		case "early-terminal":
			event.Progress = nil
			event.Kind = domain.ExecutionTurnFinished
			event.Outcome = domain.ExecutionSucceeded
		case "foreign-item":
			v.NativeItemID = "foreign-context-item"
		case "foreign-harness":
			v.Harness = domain.OpenCode
		case "repeated-start":
			v.Stage = domain.NativeCompactionStarted
		case "foreign-turn":
			event.NativeTurnID = string(domain.NewID())
		}
		if _, err := f.call(f.requestEvent(t, event)); err == nil {
			t.Fatal("invalid context publication accepted", kind)
		}
	}
	completed := f.event(domain.ExecutionProgressObserved, 4)
	v := *started.Progress.Progress.Compaction
	v.Stage = domain.NativeCompactionCompleted
	completed.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.NativeCompactionProgress, Compaction: &v}}
	f.publish(t, completed)
	terminal := f.event(domain.ExecutionTurnFinished, 5)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	for _, event := range []domain.ExecutionEvent{started, completed} {
		record, err := f.service.Store.Get(context.Background(), domain.MessageKind, event.Progress.ID)
		if err != nil {
			t.Fatal(err)
		}
		message, err := store.Decode[domain.ExecutionMessage](record)
		if err != nil || record.Revision != 1 || message.Role != domain.ProgressMessage || message.NativeID != "" || message.Progress.Compaction.Stage != event.Progress.Progress.Compaction.Stage {
			t.Fatal("native context rewrote immutable history or became a canonical message", err)
		}
	}
}
