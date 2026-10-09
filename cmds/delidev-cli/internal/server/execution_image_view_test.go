// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type imageViewUnsupportedPeer struct {
	delidevv1connect.WorkerServiceClient
}

func (c imageViewUnsupportedPeer) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	var e domain.ExecutionEvent
	if domain.Decode(req.Msg.EventJson, &e) == nil && e.Tool != nil && e.Tool.Snapshot != nil && e.Tool.Snapshot.Kind == domain.ImageViewTool {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("The peer does not support image-view observations."))
	}
	return c.WorkerServiceClient.PublishExecution(ctx, req)
}

func TestExecutionImageViewOlderPeerPreservesUncertainty(t *testing.T) {
	f, location := newImageViewPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	cfg.Client = imageViewUnsupportedPeer{WorkerServiceClient: f.client}
	publisher, mapper := bindNativeMapper(t, f, cfg)
	native := &codex.Tool{ID: "original-image", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: location}
	event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: native.ID, Correlated: true, Tool: native}
	if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
		t.Fatal("older peer silently discarded image observation")
	}
	if _, err := mapper.PublishCore(context.Background(), event); err == nil {
		t.Fatal("uncertain image observation permitted another publication")
	}
	if err := publisher.ReplayPending(context.Background()); err == nil {
		t.Fatal("unsupported peer settled pending receipt")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatal("older peer fabricated image history")
	}
	raw, err := os.ReadFile(filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"))
	var journal struct {
		Pending *struct {
			Event domain.ExecutionEvent `json:"event"`
		} `json:"pending"`
	}
	if err != nil || json.Unmarshal(raw, &journal) != nil || journal.Pending == nil || journal.Pending.Event.Tool == nil || journal.Pending.Event.Tool.Snapshot.ImageView.Location != "original.png" {
		t.Fatal("older peer discarded original pending outbox")
	}
}

func newImageViewPublicationFixture(t *testing.T) (*publicationFixture, string) {
	t.Helper()
	root := t.TempDir()
	location := ""
	authority := newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) {
		p := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
		raw, _ := json.Marshal(p)
		digest := sha256.Sum256(raw)
		location = filepath.Join(root, "workspaces", string(input.SessionID), "chat")
		m := workspace.Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, State: workspace.Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: location, Repositories: []workspace.PreparedRepository{}, CreatedAt: time.Now().UTC()}
		input.Preparation = raw
		input.Manifest, _ = json.Marshal(m)
	}, false)
	if err := os.MkdirAll(location, 0700); err != nil {
		t.Fatal(err)
	}
	location = filepath.Join(location, "original.png")
	// The native fixture supplies a valid local image. Publication must never
	// inspect its bytes or adopt the original file.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(location, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return publicationFixtureFromAuthority(t, authority), location
}

func TestExecutionImageViewLostReceiptReplaysNoFileRead(t *testing.T) {
	f, location := newImageViewPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	native := &codex.Tool{ID: "original-image", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: location}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: native.ID, Tool: native})
	native.Status = codex.ToolCompleted
	event := codex.Event{Kind: codex.ToolCompletedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: native.ID, Correlated: true, Tool: native}
	if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
		t.Fatal("lost image acknowledgment was not retained")
	}
	if err := os.WriteFile(location, []byte("replacement file; never reread"), 0600); err != nil {
		t.Fatal(err)
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
		t.Fatal("image replay substituted the original immutable request")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("image replay duplicated history")
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool.Completed == nil || m.Tool.Completed.ImageView.Location != "original.png" || *m.Tool.Started.ImageView != *m.Tool.Completed.ImageView {
		t.Fatal("replay changed original metadata")
	}
	actual, err := os.ReadFile(location)
	if err != nil || string(actual) != "replacement file; never reread" {
		t.Fatal("publication replay adopted or removed the replacement source")
	}
}

func TestExecutionImageViewNativeFailureDoesNotFabricateResultOrCleanup(t *testing.T) {
	f, location := newImageViewPublicationFixture(t)
	original, readErr := os.ReadFile(location)
	if readErr != nil {
		t.Fatal(readErr)
	}
	publisher, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnFailed, Problem: domain.Fail(domain.Unavailable, "Native image processing failed.", "Retain the original failure.")}})
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, decodeErr := store.Decode[domain.Session](r)
	if err != nil || decodeErr != nil || s.Outcome != domain.ExecutionFailed || s.Execution.CleanupVerified {
		t.Fatal("native failure became image success or cleanup")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatal("native failure fabricated an image result")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(location)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("observation cleanup removed a source file")
	}
}

func TestExecutionImageViewRetainsOriginalReferenceAndIndependentCleanup(t *testing.T) {
	f, location := newImageViewPublicationFixture(t)
	publisher, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	native := &codex.Tool{ID: "native-view-original", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: location}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: native.ID, Tool: native})
	if err := os.Remove(location); err != nil {
		t.Fatal(err)
	}
	native.Status = codex.ToolCompleted
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolCompletedEvent, ItemID: native.ID, Tool: native})
	// Replay is the durable publication only: no native call or file reopening.
	if err := publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("original image result lost: %v", err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool == nil || m.Tool.Completed == nil || m.NativeID != native.ID || m.NativeThreadID != string(f.thread) || m.NativeTurnID != string(f.turn) || m.ExecutionID != f.input.ExecutionID || m.Tool.Started.ImageView == nil || *m.Tool.Started.ImageView != *m.Tool.Completed.ImageView || m.Text != "" || len(m.Attachments) != 0 {
		t.Fatal("image observation became input or lost original receipt")
	}
	ref := m.Tool.Started.ImageView
	if ref.Location != "original.png" || ref.MachineID != f.input.MachineID {
		t.Fatal("original Worker/scope lost")
	}
	request := connect.NewRequest(&pb.ReadAttachmentRequest{SessionId: string(f.input.SessionID), AttachmentId: string(ref.ReferenceID), Limit: 1})
	request.Header().Set("Authorization", "Bearer "+f.service.Identity.Token)
	if _, err := f.service.ReadAttachment(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), request); err == nil {
		t.Fatal("metadata reference granted byte retrieval")
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnCompleted}})
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, decodeErr := store.Decode[domain.Session](r)
	if err != nil || decodeErr != nil || s.Execution.CleanupVerified || s.Outcome != domain.ExecutionSucceeded {
		t.Fatal("image completion fabricated execution cleanup")
	}
}

func TestExecutionImageViewRejectsChangedScopeAndReference(t *testing.T) {
	for _, bad := range []string{"outside", "changed-path", "changed-reference", "changed-machine", "changed-manifest"} {
		t.Run(bad, func(t *testing.T) {
			f, location := newImageViewPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			ref, err := workspace.ObserveImageViewLocation(f.input, "linux", location, domain.NewID())
			if err != nil {
				t.Fatal(err)
			}
			id := ref.ReferenceID
			e := f.toolEvent(domain.ExecutionToolStarted, 3, id, "native-view-original")
			e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.ImageViewTool, Status: domain.ToolRunning, ImageView: &ref}
			original := f.publish(t, e)
			if response, err := f.call(original); err != nil || !response.Msg.Replayed {
				t.Fatal("original image receipt did not replay")
			}
			switch bad {
			case "outside":
				ref.Location = "../outside.png"
			case "changed-path":
				ref.Location = "another.png"
			case "changed-reference":
				ref.ReferenceID = domain.NewID()
			case "changed-machine":
				ref.MachineID = domain.NewID()
			case "changed-manifest":
				ref.ManifestDigest = hex.EncodeToString(make([]byte, 32))
			}
			completion := f.toolEvent(domain.ExecutionToolCompleted, 4, id, "native-view-original")
			completion.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.ImageViewTool, Status: domain.ToolCompleted, ImageView: &ref}
			if _, err := f.call(f.requestEvent(t, completion)); err == nil {
				t.Fatal("changed observation replaced original native result")
			}
			row, _ := f.service.Store.Get(context.Background(), domain.MessageKind, id)
			prior, _ := store.Decode[domain.ExecutionMessage](row)
			if prior.Tool.Completed != nil || prior.LastSequence != 3 {
				t.Fatal("rejected completion mutated immutable history")
			}
		})
	}
}
