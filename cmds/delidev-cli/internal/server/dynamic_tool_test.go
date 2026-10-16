// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"path/filepath"
	"strings"
	"testing"
)

func dynamicPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	f := newPublicationFixture(t)
	f.input.NativeDynamicTools = true
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.dynamic-capability", nil, func(tx *store.Tx) (any, error) {
		row, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexDynamicToolsV1)
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		if _, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job); err != nil {
			return nil, err
		}
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestDynamicLostPublicationReceiptReplaysOnlyOriginalFact(t *testing.T) {
	f := dynamicPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 5}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	identity := domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	native := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "dynamic-call", Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "dynamic-call", Stage: domain.DynamicStarted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}}
	publishNativeEvent(t, f, mapper, native)
	request := &domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "9223372036854775807", Identity: identity}
	native.DynamicTool = &domain.DynamicToolUpdate{NativeID: native.ItemID, Stage: domain.DynamicRequested, Request: request}
	publishNativeEvent(t, f, mapper, native)
	reply := &domain.DynamicToolReply{Token: request.Token, ClaimID: domain.NewID(), Delivery: domain.DynamicUncertain, Unavailable: true}
	native.DynamicTool = &domain.DynamicToolUpdate{NativeID: native.ItemID, Stage: domain.DynamicReplied, Reply: reply}
	if handled, err := mapper.PublishCore(context.Background(), native); !handled || err == nil {
		t.Fatal("lost receipt not retained")
	}
	if _, err := mapper.PublishCore(context.Background(), native); err == nil {
		t.Fatal("lost receipt authorized another fact")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 6 || client.calls[4] != client.calls[5] {
		t.Fatal("receipt replay replaced original event")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("fact replay duplicated native tool", err)
	}
	retained, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || retained.DynamicTool == nil || retained.DynamicTool.Request.IDValue != request.IDValue || retained.DynamicTool.Reply.Delivery != domain.DynamicUncertain || retained.DynamicTool.Resolved || retained.State != domain.MessageStreaming || retained.NativeID != native.ItemID || retained.Tool != nil {
		t.Fatal("reply collapsed independent facts", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, decodeErr := store.Decode[domain.Session](row)
	if err != nil || decodeErr != nil || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified {
		t.Fatal("unavailable reply settled root/cleanup")
	}
}
func TestDynamicServerRejectsChangedOriginalOwnershipAndRequestReuse(t *testing.T) {
	f := dynamicPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	publisher, mapper := bindNativeMapper(t, f, cfg)
	defer publisher.Close()
	identity := domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
	first := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "first", Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: "first", Stage: domain.DynamicStarted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}}
	publishNativeEvent(t, f, mapper, first)
	request := domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "7", Identity: identity}
	first.DynamicTool = &domain.DynamicToolUpdate{NativeID: "first", Stage: domain.DynamicRequested, Request: &request}
	publishNativeEvent(t, f, mapper, first)
	second := first
	second.ItemID = "second"
	second.DynamicTool = &domain.DynamicToolUpdate{NativeID: "second", Stage: domain.DynamicStarted, Snapshot: &domain.DynamicToolSnapshot{Identity: identity, Status: "inProgress"}}
	publishNativeEvent(t, f, mapper, second)
	request.Token = domain.NewID()
	second.DynamicTool = &domain.DynamicToolUpdate{NativeID: "second", Stage: domain.DynamicRequested, Request: &request}
	if _, err := mapper.PublishCore(context.Background(), second); err == nil {
		t.Fatal("native request reuse obtained second reply ownership")
	}
	rows, _ := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	for _, row := range rows {
		message, _ := store.Decode[domain.ExecutionMessage](row)
		if message.NativeID == "second" && message.DynamicTool.Request != nil {
			t.Fatal("rejected request mutated retained evidence")
		}
	}
}

func TestDynamicExecutedReplyRequiresSelectedOriginalBackend(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			f := dynamicPublicationFixture(t)
			if selected {
				f.input.NativeOpenRouterTools = true
				f.input.NativeImageGeneration = true
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.original-image-backend", nil, func(tx *store.Tx) (any, error) {
					row, err := tx.Get(domain.JobKind, f.job)
					if err != nil {
						return nil, err
					}
					job, err := store.Decode[domain.Job](row)
					if err != nil {
						return nil, err
					}
					job.Input, _ = json.Marshal(f.input)
					return tx.Put(domain.JobKind, row.ID, row.Revision, row.SessionID, row.ProjectID, job)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
			namespace := "delidev_image"
			identity := domain.DynamicToolIdentity{Tool: "imagegen", Namespace: &namespace, ArgumentsDigest: strings.Repeat("a", 64)}
			event := codex.Event{Kind: codex.DynamicToolEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "selected-image", Correlated: true}
			request := &domain.DynamicToolRequest{Token: domain.NewID(), IDKind: domain.InteractionNumberID, IDValue: "7", Identity: identity}
			event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicRequested, Request: request}
			publishNativeEvent(t, f, mapper, event)
			event.DynamicTool = &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: domain.DynamicReplied, Reply: &domain.DynamicToolReply{Token: request.Token, ClaimID: domain.NewID(), Delivery: domain.DynamicTransmitted, Unavailable: false}}
			_, err := mapper.PublishCore(context.Background(), event)
			if (err == nil) != selected {
				t.Fatal("executed reply escaped original backend admission", err)
			}
		})
	}
}
