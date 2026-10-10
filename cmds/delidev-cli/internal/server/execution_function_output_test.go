// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"path/filepath"
	"testing"
)

func TestFunctionOutputDurablePublicationRetainsOriginalItem(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "string", true: "structured"}[structured], func(t *testing.T) {
			f := newPublicationFixture(t)
			_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
			namespace, text, image, audio := "namespace", "exact output", "https://invalid.example/image", "file:///private/fixture"
			v := &domain.CodexFunctionOutput{NativeItemID: "original-result", Name: "fixture_tool", Namespace: &namespace, Variant: domain.FunctionOutputString, Text: &text}
			if structured {
				v.Variant = domain.FunctionOutputStructured
				v.Text = nil
				v.Content = []domain.FunctionOutputContent{{Kind: domain.FunctionOutputText, Text: &text}, {Kind: domain.FunctionOutputImage, ImageURL: &image}, {Kind: domain.FunctionOutputAudio, AudioURL: &audio}, {Kind: domain.FunctionOutputEncrypted}}
			}
			publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.FunctionOutputStartedEvent, ItemID: v.NativeItemID, FunctionOutput: v})
			rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
			if err != nil || len(rows) != 0 {
				t.Fatal("start fabricated result publication", err)
			}
			publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.FunctionOutputCompletedEvent, ItemID: v.NativeItemID, FunctionOutput: v})
			rows, err = f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
			if err != nil || len(rows) != 1 {
				t.Fatal("result did not publish exactly one observation", err)
			}
			m, err := store.Decode[domain.ExecutionMessage](rows[0])
			if err != nil || m.NativeID != v.NativeItemID || m.NativeThreadID != string(f.thread) || m.NativeTurnID != string(f.turn) || m.Role != domain.ProgressMessage || m.Progress == nil || m.Progress.FunctionOutput == nil || m.Progress.FunctionOutput.Namespace == nil || *m.Progress.FunctionOutput.Namespace != namespace || m.Tool != nil || m.Artifact != nil || m.State != domain.MessageComplete {
				t.Fatal("result provenance changed or became tool execution", err)
			}
			if structured && len(m.Progress.FunctionOutput.Content) != 4 {
				t.Fatal("ordered rich content lost")
			}
			e := codex.Event{Kind: codex.FunctionOutputCompletedEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: v.NativeItemID, FunctionOutput: v}
			if _, err := mapper.PublishCore(context.Background(), e); err == nil {
				t.Fatal("duplicate result accepted under a replacement product identity")
			}
		})
	}
}
func TestFunctionOutputLostAcknowledgmentReplaysOnlyOriginalPublication(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 3}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	text := "exact result"
	e := codex.Event{Kind: codex.FunctionOutputCompletedEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: "original-result", FunctionOutput: &domain.CodexFunctionOutput{NativeItemID: "original-result", Name: "fixture_tool", Variant: domain.FunctionOutputString, Text: &text}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost result acknowledgment did not retain uncertainty")
	}
	if _, err := mapper.PublishCore(context.Background(), e); err == nil {
		t.Fatal("uncertain result permitted native republishing")
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
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 || len(client.calls) != 4 || client.calls[2] != client.calls[3] {
		t.Fatal("receipt replay changed identity or duplicated result", err)
	}
}
