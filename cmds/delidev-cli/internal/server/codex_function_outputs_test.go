// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func functionServerFixture(t *testing.T, capability bool) *publicationFixture {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	if capability {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture-function-capability", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.MachineKind, f.input.MachineID)
			if err != nil {
				return nil, err
			}
			m, err := store.Decode[domain.Machine](r)
			if err != nil {
				return nil, err
			}
			m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexFunctionOutputV1)
			return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	return f
}
func functionServerEvent(f *publicationFixture) domain.ExecutionEvent {
	text := "<script>inert()</script>"
	v := &domain.CodexFunctionOutput{Version: 1, ID: domain.NewID(), NativeID: "original-function", Name: "exact_name", Stage: domain.CodexFunctionOutputStarted, Output: domain.CodexFunctionOutputBody{Variant: domain.CodexFunctionContents, Contents: []domain.CodexFunctionContent{{Type: domain.CodexFunctionText, Text: &text}, {Type: domain.CodexFunctionAudio, ReferenceKind: domain.CodexFunctionAudioURL, ReferencePresent: true}, {Type: domain.CodexFunctionEncrypted, Present: true}}}}
	event := f.event(domain.ExecutionCodexFunctionOutputObserved, 3)
	event.CodexFunctionOutput = v
	return event
}
func TestCodexFunctionOutputServerRetentionExactReplayAndNoOutcome(t *testing.T) {
	f := functionServerFixture(t, true)
	event := functionServerEvent(f)
	first := f.publish(t, event)
	if r, err := f.call(first); err != nil || !r.Msg.Replayed {
		t.Fatal("start receipt replay failed", err)
	}
	event.Sequence = 4
	event.CodexFunctionOutput.Stage = domain.CodexFunctionOutputCompleted
	completed := f.publish(t, event)
	if r, err := f.call(completed); err != nil || !r.Msg.Replayed {
		t.Fatal("completion receipt replay failed", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.MessageKind, event.CodexFunctionOutput.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.Decode[domain.ExecutionMessage](row)
	if err != nil || v.CodexFunctionOutput == nil || v.Role != domain.ToolMessage || v.State != domain.MessageComplete || v.FirstSequence != 3 || v.LastSequence != 4 || v.Text != "<script>inert()</script>" || v.ExecutionID != f.input.ExecutionID || v.NativeThreadID != string(f.thread) || v.CodexFunctionOutput.Namespace != nil {
		t.Fatal("original result/provenance lost", err)
	}
	original, _ := json.Marshal(event.CodexFunctionOutput)
	retained, _ := json.Marshal(v.CodexFunctionOutput)
	if !bytes.Equal(original, retained) {
		t.Fatal("safe ordered projection changed")
	}
	sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, decodeErr := store.Decode[domain.Session](sr)
	if err != nil || decodeErr != nil || session.Outcome != domain.ExecutionRunning || session.ActiveExecutionID != f.input.ExecutionID {
		t.Fatal("result granted root completion or cleanup", err, decodeErr)
	}
}
func TestCodexFunctionOutputServerRejectsAuthorityAndLifecycleAtomically(t *testing.T) {
	for _, fault := range []string{"capability", "completed-before-start", "foreign-thread", "foreign-turn", "namespace-change", "name-change", "public-reference", "mixed-payload"} {
		t.Run(fault, func(t *testing.T) {
			f := functionServerFixture(t, fault != "capability")
			event := functionServerEvent(f)
			if fault == "namespace-change" || fault == "name-change" {
				f.publish(t, event)
				event.Sequence = 4
				event.CodexFunctionOutput.Stage = domain.CodexFunctionOutputCompleted
			}
			switch fault {
			case "completed-before-start":
				event.CodexFunctionOutput.Stage = domain.CodexFunctionOutputCompleted
			case "foreign-thread":
				event.NativeThreadID = string(domain.NewID())
			case "foreign-turn":
				event.NativeTurnID = string(domain.NewID())
			case "namespace-change":
				s := ""
				event.CodexFunctionOutput.Namespace = &s
			case "name-change":
				event.CodexFunctionOutput.Name = "changed"
			case "mixed-payload":
				event.Message = &domain.ExecutionMessageUpdate{ID: domain.NewID()}
			}
			request := f.requestEvent(t, event)
			if fault == "public-reference" {
				request.EventJson = bytes.Replace(request.EventJson, []byte(`"reference_present":true`), []byte(`"reference_present":true,"audio_url":"private-sentinel"`), 1)
			}
			before, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.call(request); err == nil {
				t.Fatal("invalid public output admitted")
			}
			after, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("rejection partially committed")
			}
		})
	}
}
