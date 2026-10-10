// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"path/filepath"
	"reflect"
	"testing"
)

func functionOutputServerObservation() *codex.FunctionOutput {
	text := "Original inert <script>result()</script>"
	namespace := "original namespace"
	return &codex.FunctionOutput{ID: "original-output", Observation: domain.FunctionOutputObservation{Name: "original_tool", Namespace: &namespace, Variant: domain.FunctionOutputStructured, Parts: []domain.FunctionOutputPart{{Kind: domain.FunctionOutputText, Text: &text}, {Kind: domain.FunctionOutputImage, Reference: domain.FunctionOutputImageURL}, {Kind: domain.FunctionOutputEncrypted}, {Kind: domain.FunctionOutputAudio, Reference: domain.FunctionOutputAudioURL}}}}
}
func TestFunctionOutputDurablePublicationReplaysLostAcknowledgmentWithoutNativeMutation(t *testing.T) {
	for _, variant := range []domain.FunctionOutputVariant{domain.FunctionOutputString, domain.FunctionOutputStructured} {
		t.Run(string(variant), func(t *testing.T) {
			f := newPublicationFixture(t)
			cfg := publicationWorkerConfig(t, f)
			client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
			cfg.Client = client
			publisher, mapper := bindNativeMapper(t, f, cfg)
			native := functionOutputServerObservation()
			if variant == domain.FunctionOutputString {
				text := "original string output"
				native.Observation.Variant = variant
				native.Observation.Text = &text
				native.Observation.Parts = nil
			}
			publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.FunctionOutputStartedEvent, ItemID: native.ID, FunctionOutput: native})
			event := codex.Event{Kind: codex.FunctionOutputCompletedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: native.ID, Correlated: true, FunctionOutput: native}
			if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
				t.Fatal("lost result publication acknowledgment was promoted to success")
			}
			if _, err := mapper.PublishCore(context.Background(), event); err == nil {
				t.Fatal("uncertain result allowed new publication")
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
			if len(client.calls) != 5 || client.calls[3] != client.calls[4] {
				t.Fatal("result replay replaced original receipt")
			}
			rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
			if err != nil || len(rows) != 1 {
				t.Fatal("replay duplicated durable result", err)
			}
			message, err := store.Decode[domain.ExecutionMessage](rows[0])
			if err != nil || message.Role != domain.ArtifactMessage || message.NativeID != native.ID || message.Artifact == nil || message.Artifact.Completed == nil || !reflect.DeepEqual(*message.Artifact.Completed.FunctionOutput, native.Observation) || message.Tool != nil || message.Text != "" {
				t.Fatal("typed inert result lost original order/identity", err)
			}
			row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			state, decodeErr := store.Decode[domain.Session](row)
			if err != nil || decodeErr != nil || state.Outcome != domain.ExecutionRunning || state.Execution.CleanupVerified || state.Execution.LastSequence != 4 {
				t.Fatal("result fabricated execution completion or cleanup", err, decodeErr)
			}
		})
	}
}
func TestFunctionOutputServerRejectsChangedIdentityAndForeignScope(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	native := functionOutputServerObservation()
	id := domain.NewID()
	start := f.event(domain.ExecutionArtifactStarted, 3)
	start.Artifact = &domain.ExecutionArtifactUpdate{ID: id, NativeID: native.ID, Snapshot: &domain.ArtifactSnapshot{Kind: domain.FunctionOutputArtifact, FunctionOutput: &native.Observation}}
	f.publish(t, start)
	for _, fault := range []string{"name", "namespace", "variant", "thread", "turn", "native", "private-field", "mixed"} {
		event := f.event(domain.ExecutionArtifactCompleted, 4)
		output := native.Observation
		event.Artifact = &domain.ExecutionArtifactUpdate{ID: id, NativeID: native.ID, Snapshot: &domain.ArtifactSnapshot{Kind: domain.FunctionOutputArtifact, FunctionOutput: &output}}
		switch fault {
		case "name":
			output.Name = "changed"
		case "namespace":
			output.Namespace = nil
		case "variant":
			text := "changed"
			output.Variant = domain.FunctionOutputString
			output.Text = &text
			output.Parts = nil
		case "thread":
			event.NativeThreadID = string(domain.NewID())
		case "turn":
			event.NativeTurnID = string(domain.NewID())
		case "native":
			event.Artifact.NativeID = "foreign"
		case "private-field":
			output.Parts = append([]domain.FunctionOutputPart(nil), output.Parts...)
			private := "ciphertext"
			output.Parts[2].Text = &private
		case "mixed":
			event.Artifact.Snapshot.Text = "flattened content"
		}
		if _, err := f.call(f.requestEvent(t, event)); err == nil {
			t.Fatal("changed output identity or private/mixed content accepted", fault)
		}
	}
}
