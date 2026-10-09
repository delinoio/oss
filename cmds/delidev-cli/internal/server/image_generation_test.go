// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNativeGeneratedImagesPublishOrderedMetadataPartialFailureAndAuthenticatedRead(t *testing.T) {
	sf := newSubscriptionFixture(t)
	clear(sf.login())
	_, err := sf.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.generation-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, sf.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeImageGenerationV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	takeSubscriptionExecutionFixture(t, sf)
	f := publicationFixtureFromAuthority(t, sf.authorityFixture)
	cfg := publicationWorkerConfig(t, f)
	publisher, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	mapper := worker.NewCodexEventPublisher(publisher)
	if err = mapper.BindThread(context.Background(), codex.ThreadResult{RequestID: f.input.ThreadRequestID, Thread: &codex.Thread{ID: f.thread}, Effective: &codex.EffectiveSettings{Model: f.input.Configuration.NativeModel, Provider: "openai", Sandbox: codex.Sandbox{Type: codex.ReadOnly}, ApprovalPolicy: codex.ApprovalOnRequest, ApprovalsReviewer: "user"}}); err != nil {
		t.Fatal(err)
	}
	if err = mapper.AcceptInput(context.Background(), codex.TurnResult{RequestID: f.input.TurnRequestID, InputID: f.input.InputID, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 3, 3))) != nil {
		t.Fatal("encode")
	}
	raw := buffer.Bytes()
	for _, id := range []string{"original-one", "original-two", "original-failed"} {
		generation := &codex.ImageGeneration{ID: id, Observation: domain.ImageGenerationObservation{Status: domain.ImageGenerationRunning, Outputs: []domain.ImageAttachment{}}}
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ImageGenerationStartedEvent, ItemID: id, ImageGeneration: generation})
		if id == "original-failed" {
			generation.Observation.Status = domain.ImageGenerationFailed
			generation.Observation.Failure = &domain.ImageGenerationFailure{Type: "usageLimitExceeded", LimitID: "actual-native-limit"}
		} else {
			generation.Observation.Status = domain.ImageGenerationCompleted
			generation.Bytes = raw
		}
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ImageGenerationCompletedEvent, ItemID: id, ImageGeneration: generation})
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnFailed}})
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatal("ordered generation transcript lost", err)
	}
	var refs []domain.ImageAttachment
	for i, row := range rows {
		v, e := store.Decode[domain.ExecutionMessage](row)
		if e != nil || v.Artifact == nil || v.Artifact.Completed == nil || v.NativeID != []string{"original-one", "original-two", "original-failed"}[i] || v.Artifact.Started.ImageGeneration.Status != domain.ImageGenerationRunning {
			t.Fatal("native order/identity lost", e)
		}
		refs = append(refs, v.Artifact.Completed.ImageGeneration.Outputs...)
		if bytes.Contains(row.Data, []byte("savedPath")) || bytes.Contains(row.Data, []byte("result")) || bytes.Contains(row.Data, raw) {
			t.Fatal("server retained native bytes/path")
		}
	}
	if len(refs) != 2 || refs[0].ID == refs[1].ID {
		t.Fatal("partial success fabricated or original outputs conflated")
	}
	if err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		for _, ref := range refs {
			_, v, e := tx.ImageUploadRecord(ref.ID)
			if e != nil || v.GeneratedExecutionID != f.input.ExecutionID || v.WorkerDeviceID != f.device || tx.ImageAttachmentReadable(f.input.SessionID, ref) != nil {
				return domain.InvalidImageInput()
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// This real authenticated stream fixture serves only original Worker storage;
	// the server resource/receipt database never receives the image bytes.
	client := delidevv1connect.NewAttachmentServiceClient(http.DefaultClient, f.http.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primary, err := f.client.WatchWork(ctx, subscriptionRequest(f.workerToken, &pb.WatchWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if !primary.Receive() {
		t.Fatal("original primary stream", primary.Err())
	}
	stream, err := client.WatchAttachmentTransfers(ctx, subscriptionRequest(f.workerToken, &pb.WatchAttachmentTransfersRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("original image stream", stream.Err())
	}
	done := make(chan error, 1)
	go func() {
		for stream.Receive() {
			q := stream.Msg()
			if q.Heartbeat {
				continue
			}
			data, e := (imageinput.Manager{Root: cfg.Root}).Transfer(f.input.MachineID, q.Transfer)
			if e != nil {
				done <- e
				return
			}
			_, e = client.ReportAttachmentTransfer(ctx, subscriptionRequest(f.workerToken, &pb.ReportAttachmentTransferRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance), TransferId: q.Transfer.Id, Data: data, Sha256: imageinput.Digest(data)}))
			done <- e
			return
		}
		done <- stream.Err()
	}()
	reply, err := client.ReadAttachment(ctx, subscriptionRequest(f.service.Identity.Token, &pb.ReadAttachmentRequest{SessionId: string(f.input.SessionID), AttachmentId: string(refs[0].ID), Limit: domain.MaxImageChunkBytes}))
	if err != nil || !bytes.Equal(reply.Msg.Data, raw) {
		t.Fatal("authenticated original read failed", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = client.ReadAttachment(ctx, subscriptionRequest("wrong-token", &pb.ReadAttachmentRequest{SessionId: string(f.input.SessionID), AttachmentId: string(refs[0].ID), Limit: domain.MaxImageChunkBytes})); err == nil {
		t.Fatal("unauthenticated read admitted")
	}
	// The independent ownership metadata boundary protects original output even
	// though managed subscription Fork activation itself remains independently gated.
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.output-owner", nil, func(tx *store.Tx) (any, error) {
		r, v, e := tx.ImageUploadRecord(refs[0].ID)
		if e != nil {
			return nil, e
		}
		v.Owners = append(v.Owners, domain.NewID())
		if _, e = tx.PutImageUpload(r, v); e != nil {
			return nil, e
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal("independent output owner invalid", err)
	}
}
func TestNativeGeneratedImageMetadataRejectsUnavailableProvider(t *testing.T) {
	f := newPublicationFixture(t)
	event := f.event(domain.ExecutionArtifactStarted, 3)
	event.Artifact = &domain.ExecutionArtifactUpdate{ID: domain.NewID(), NativeID: "unavailable", Snapshot: &domain.ArtifactSnapshot{Kind: domain.ImageGenerationArtifact, ImageGeneration: &domain.ImageGenerationObservation{Status: domain.ImageGenerationRunning, Outputs: []domain.ImageAttachment{}}}}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.unavailable-generation", nil, func(tx *store.Tx) (any, error) {
		sr, e := tx.Get(domain.SessionKind, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		return nil, publishGeneratedImageMetadata(tx, f.input, sr, event)
	})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("custom proxy invented image generation entitlement", err)
	}
	encoded, _ := json.Marshal(event)
	if bytes.Contains(encoded, []byte("Bytes")) {
		t.Fatal("private bytes serialized")
	}
}
