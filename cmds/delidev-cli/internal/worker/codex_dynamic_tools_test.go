// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"testing"
)

type dynamicReplyFixture struct {
	t      *testing.T
	mapper *CodexEventPublisher
	event  codex.Event
	sends  int
	lose   bool
}

func (f *dynamicReplyFixture) InspectDynamicTool(context.Context, domain.ID) (codex.Event, error) {
	return f.event, nil
}
func (f *dynamicReplyFixture) RejectDynamicTool(ctx context.Context, arrival, response, turn domain.ID) (codex.DynamicTool, error) {
	if ctx.Err() != nil {
		return codex.DynamicTool{}, ctx.Err()
	}
	f.sends++
	entries, err := os.ReadDir(filepath.Join(f.mapper.publisher.config.Root, "jobs", string(f.mapper.publisher.job), "dynamic-tools"))
	if err != nil || len(entries) != 1 {
		f.t.Fatal("send lacked exclusive private intent", err)
	}
	raw, err := security.ReadPrivate(filepath.Join(f.mapper.publisher.config.Root, "jobs", string(f.mapper.publisher.job), "dynamic-tools", entries[0].Name()), 5<<20)
	var journal dynamicToolIntent
	if err != nil || domain.DecodeBounded(raw, &journal, 5<<20) != nil || journal.Observation.Delivery != domain.DynamicSendStarted || journal.OriginalWireToken != arrival || journal.Observation.ResponseID != response || journal.TurnID != turn || journal.ServerID != f.mapper.publisher.config.Credential.ServerID || journal.DeviceID != f.mapper.publisher.config.Credential.DeviceID || journal.Actor != domain.WorkerDevice || journal.MachineID != f.mapper.publisher.input.MachineID || journal.SessionID != f.mapper.publisher.input.SessionID || journal.ExecutionID != f.mapper.publisher.execution || journal.InstanceID != f.mapper.publisher.config.Instance || journal.AssignmentDigest != f.mapper.publisher.state.AssignmentDigest || journal.AssignmentRevision != f.mapper.publisher.state.Revision || string(journal.Original) != string(f.event.DynamicTool.OriginalJSON) || string(journal.OriginalRequestID) != `7` {
		f.t.Fatal("send crossed an incomplete or rewritten owner barrier", err)
	}
	value := f.event.DynamicTool.Observation
	value.ID = domain.NewID()
	value.Stage = domain.DynamicToolReplied
	value.ResponseID = response
	negative := false
	value.NegativeOutcome = &negative
	value.Delivery = domain.DynamicTransmitted
	if f.lose {
		value.Delivery = domain.DynamicUncertain
		return codex.DynamicTool{Observation: value}, errors.New("fixture lost original acknowledgment")
	}
	return codex.DynamicTool{Observation: value}, nil
}
func TestDynamicToolPrivateIntentPrecedesOnlyNativeAttemptAndLostAckNeverResends(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "transmitted", true: "uncertain"}[lose], func(t *testing.T) {
			_, p, rpc := codexProviderFixture(t, false, codex.APIProvider)
			mapper := NewCodexEventPublisher(p)
			mapper.thread, mapper.turn = domain.NewID(), domain.NewID()
			p.state.LastSequence = 2
			negative := false
			number := int64(7)
			value := domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: domain.DynamicToolRequested, CallID: "original-call", Tool: "unavailable", Arguments: domain.DynamicArguments{Present: true, Type: domain.DynamicObject, Digest: dynamicPrivateDigest([]byte(`{"secret":"private"}`))}, ArrivalID: domain.NewID(), RequestID: &domain.InteractionRequestID{Kind: domain.InteractionNumberID, Number: &number}, Delivery: domain.DynamicNotSent, RequestResolved: &negative}
			raw, _ := json.Marshal(map[string]any{"threadId": mapper.thread, "turnId": mapper.turn, "callId": value.CallID, "namespace": nil, "tool": value.Tool, "arguments": map[string]any{"secret": "private"}})
			native := nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/call", ID: json.RawMessage(`7`), Token: value.ArrivalID, Params: raw}
			event := codex.Event{Kind: codex.DynamicToolRequestedEvent, ThreadID: mapper.thread, TurnID: mapper.turn, ItemID: value.CallID, Correlated: true, DynamicTool: &codex.DynamicTool{Observation: value, OriginalJSON: raw, OriginalRequest: &native}}
			fixture := &dynamicReplyFixture{t: t, mapper: mapper, event: event, lose: lose}
			err := mapper.HandleDynamicRequest(context.Background(), fixture, event, true)
			if (err != nil) != lose || fixture.sends != 1 || len(rpc.events) != 3 {
				t.Fatal("original negative attempt or independent observations changed", err, fixture.sends, len(rpc.events))
			}
			// A new mapper simulates process/controller restart; its existing journal
			// fences both transmitted and uncertain requests, without a native send.
			restarted := NewCodexEventPublisher(p)
			restarted.thread, restarted.turn = mapper.thread, mapper.turn
			if err := restarted.HandleDynamicRequest(context.Background(), fixture, event, true); err == nil || fixture.sends != 1 {
				t.Fatal("restart repeated retained original send")
			}
			for _, public := range rpc.events {
				if string(public) == string(raw) || containsDynamicPrivate(public) {
					t.Fatal("private native content escaped outbox")
				}
			}
		})
	}
}
func containsDynamicPrivate(raw []byte) bool {
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	_, ok := value["original"]
	return ok
}
func TestDynamicToolExclusiveIntentNeverReplacesPartialOrSymlinkFence(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "original.json")
	if err := exclusiveDynamicIntent(path, []byte(`{"original":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := exclusiveDynamicIntent(path, []byte(`{"replacement":true}`)); err == nil {
		t.Fatal("existing fence overwritten")
	}
	raw, _ := security.ReadPrivate(path, 1024)
	if string(raw) != `{"original":true}` {
		t.Fatal("original fence changed")
	}
	symlink := filepath.Join(directory, "link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if err := exclusiveDynamicIntent(symlink, []byte(`{}`)); err == nil {
		t.Fatal("symlink fence replaced")
	}
}

func TestDynamicToolPublicationAckLossBeforeWireLeavesNoResendFence(t *testing.T) {
	_, p, rpc := codexProviderFixture(t, false, codex.APIProvider)
	mapper := NewCodexEventPublisher(p)
	mapper.thread, mapper.turn = domain.NewID(), domain.NewID()
	p.state.LastSequence = 2
	number := int64(7)
	resolved := false
	value := domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: domain.DynamicToolRequested, CallID: "original-call", Tool: "unavailable", Arguments: domain.DynamicArguments{Present: true, Type: domain.DynamicNull, Digest: dynamicPrivateDigest([]byte("null"))}, ArrivalID: domain.NewID(), RequestID: &domain.InteractionRequestID{Kind: domain.InteractionNumberID, Number: &number}, Delivery: domain.DynamicNotSent, RequestResolved: &resolved}
	raw, _ := json.Marshal(map[string]any{"threadId": mapper.thread, "turnId": mapper.turn, "callId": value.CallID, "namespace": nil, "tool": value.Tool, "arguments": nil})
	native := nativewire.Event{Kind: nativewire.ServerRequest, Method: "item/tool/call", ID: json.RawMessage(`7`), Token: value.ArrivalID, Params: raw}
	event := codex.Event{Kind: codex.DynamicToolRequestedEvent, ThreadID: mapper.thread, TurnID: mapper.turn, ItemID: value.CallID, Correlated: true, DynamicTool: &codex.DynamicTool{Observation: value, OriginalJSON: raw, OriginalRequest: &native}}
	fixture := &dynamicReplyFixture{t: t, mapper: mapper, event: event}
	rpc.lose = true
	if err := mapper.HandleDynamicRequest(context.Background(), fixture, event, true); err == nil || fixture.sends != 0 {
		t.Fatal("lost original observation acknowledgment reached native wire")
	}
	original := append([]byte(nil), rpc.events[0]...)
	rpc.lose = false
	if err := p.ReplayPending(context.Background()); err != nil || string(rpc.events[1]) != string(original) || fixture.sends != 0 {
		t.Fatal("receipt replay changed the original observation or sent native reply", err)
	}
	restarted := NewCodexEventPublisher(p)
	restarted.thread, restarted.turn = mapper.thread, mapper.turn
	if err := restarted.HandleDynamicRequest(context.Background(), fixture, event, true); err == nil || fixture.sends != 0 {
		t.Fatal("prepared original ownership permitted resend after restart")
	}
}
