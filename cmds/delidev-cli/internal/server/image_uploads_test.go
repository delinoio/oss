// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func imageRPCFixture(t *testing.T) (*firstDispatchFixture, delidevv1connect.AttachmentServiceClient, []byte) {
	t.Helper()
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	_, err := f.workerClient.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1, pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1}}))
	if err != nil {
		t.Fatal(err)
	}
	f.machine = currentCatalogResource(t, f.accountFixture, f.machine)
	client := delidevv1connect.NewAttachmentServiceClient(http.DefaultClient, f.endpoint.URL)
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return f, client, out.Bytes()
}
func imageWorkerFixture(t *testing.T, f *firstDispatchFixture, client delidevv1connect.AttachmentServiceClient) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.WatchAttachmentTransfers(ctx, ownerRequest(f.workerIdentity, &pb.WatchAttachmentTransfersRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal(stream.Err())
	}
	root := t.TempDir()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m := imageinput.Manager{Root: root}
		for stream.Receive() {
			v := stream.Msg().Transfer
			if v == nil {
				continue
			}
			data, problem := m.Transfer(domain.ID(f.machine.Id), v)
			report := &pb.ReportAttachmentTransferRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, TransferId: v.Id, Data: data}
			if len(data) > 0 {
				report.Sha256 = imageinput.Digest(data)
			}
			if problem != nil {
				report.ProblemCode = string(domain.SafeError(problem).Code)
			}
			_, err := client.ReportAttachmentTransfer(ctx, ownerRequest(f.workerIdentity, report))
			if err != nil && ctx.Err() == nil {
				t.Error(err)
				return
			}
		}
	}()
	stop := func() {
		cancel()
		stream.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("image fixture did not join")
		}
	}
	t.Cleanup(stop)
	return root, stop
}
func beginImage(t *testing.T, f *firstDispatchFixture, c delidevv1connect.AttachmentServiceClient, raw []byte, draft, operation domain.ID, session string) *pb.AttachmentUpload {
	t.Helper()
	reply, err := c.BeginUpload(context.Background(), ownerRequest(f.identity, &pb.BeginUploadRequest{RequestId: string(domain.NewID()), DraftId: string(draft), OperationId: string(operation), MachineId: f.machine.Id, MachineRevision: f.machine.Revision, SessionId: session, MediaType: pb.ImageMediaType_IMAGE_MEDIA_TYPE_PNG, ByteLength: uint64(len(raw)), Sha256: imageinput.Digest(raw)}))
	if err != nil {
		t.Fatal(err)
	}
	return reply.Msg.Upload
}
func finishImage(t *testing.T, f *firstDispatchFixture, c delidevv1connect.AttachmentServiceClient, u *pb.AttachmentUpload, raw []byte) *pb.AttachmentUpload {
	t.Helper()
	q := &pb.WriteChunkRequest{AttachmentId: u.Attachment.Id, Data: raw, Sha256: imageinput.Digest(raw)}
	for range 2 {
		if _, err := c.WriteChunk(context.Background(), ownerRequest(f.identity, q)); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := c.FinishUpload(context.Background(), ownerRequest(f.identity, &pb.FinishUploadRequest{AttachmentId: u.Attachment.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Msg.Upload.State != pb.AttachmentState_ATTACHMENT_STATE_READY {
		t.Fatal(reply.Msg.Upload)
	}
	return reply.Msg.Upload
}
func TestImageRPCOrderedClaimExactRetryReadbackAndImmutableEdit(t *testing.T) {
	f, c, raw := imageRPCFixture(t)
	root, _ := imageWorkerFixture(t, f, c)
	ctx := context.Background()
	operation := domain.NewID()
	draft := domain.NewID()
	first := finishImage(t, f, c, beginImage(t, f, c, raw, draft, operation, ""), raw)
	second := finishImage(t, f, c, beginImage(t, f, c, raw, draft, operation, ""), raw)
	selection := f.selection
	selection.Prompt = ""
	document, _ := json.Marshal(selection)
	req := &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: document, Attachments: []*pb.ImageAttachment{second.Attachment, first.Attachment}}
	reply, err := sessionClient(f.accountFixture).CreateSession(ctx, ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	change := reply.Msg.Change
	var queued domain.QueuedInput
	if domain.Decode(change.Input.DocumentJson, &queued) != nil || len(queued.Attachments) != 2 || string(queued.Attachments[0].ID) != second.Attachment.Id || string(queued.Attachments[1].ID) != first.Attachment.Id {
		t.Fatal("ordered receipt lost", queued)
	}
	replay, err := sessionClient(f.accountFixture).CreateSession(ctx, ownerRequest(f.identity, req))
	if err != nil || replay.Msg.Change.Session.Id != change.Session.Id || !replay.Msg.Change.Replayed {
		t.Fatal(replay, err)
	}

	edit, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(change.Input, domain.NewID()), SessionId: change.Session.Id, Prompt: "Updated caption"}))
	if err != nil {
		t.Fatal(err)
	}
	var edited domain.QueuedInput
	if domain.Decode(edit.Msg.Change.Input.DocumentJson, &edited) != nil || edited.Prompt != "Updated caption" || len(edited.Attachments) != 2 || edited.Attachments[0] != queued.Attachments[0] || edited.Attachments[1] != queued.Attachments[1] {
		t.Fatal("old-client edit changed image refs", edited)
	}
	read, err := c.ReadAttachment(ctx, ownerRequest(f.identity, &pb.ReadAttachmentRequest{SessionId: change.Session.Id, AttachmentId: first.Attachment.Id, Limit: domain.MaxImageChunkBytes}))
	if err != nil || !bytes.Equal(read.Msg.Data, raw) || read.Msg.Sha256 != imageinput.Digest(raw) || !read.Msg.Complete {
		t.Fatal(read, err)
	}
	if _, err := c.ReadAttachment(ctx, ownerRequest(f.identity, &pb.ReadAttachmentRequest{SessionId: f.change.Session.Id, AttachmentId: first.Attachment.Id, Limit: domain.MaxImageChunkBytes})); err == nil {
		t.Fatal("foreign session read image")
	}
	if _, err := c.DeleteDraftAttachment(ctx, ownerRequest(f.identity, &pb.DeleteDraftAttachmentRequest{RequestId: string(domain.NewID()), AttachmentId: first.Attachment.Id})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("accepted draft removal", err)
	}
	if _, err := os.Stat(filepath.Join(root, "image-inputs", first.Attachment.Id+".data")); err != nil {
		t.Fatal("private Worker bytes missing", err)
	}
	var upload domain.ImageUpload
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, v, e := imageUploadRecord(tx, domain.ID(first.Attachment.Id))
		upload = v
		return e
	}); err != nil || upload.State != domain.ImageClaimed || upload.InputID != domain.ID(change.Input.Id) || len(upload.Owners) != 1 {
		t.Fatal(upload, err)
	}
}
func TestImageOfflineDraftRemovalSurvivesReconciliation(t *testing.T) {
	f, c, raw := imageRPCFixture(t)
	root, stop := imageWorkerFixture(t, f, c)
	u := finishImage(t, f, c, beginImage(t, f, c, raw, domain.NewID(), domain.NewID(), ""), raw)
	stop()
	ctx := context.Background()
	request := &pb.DeleteDraftAttachmentRequest{RequestId: string(domain.NewID()), AttachmentId: u.Attachment.Id}
	reply, err := c.DeleteDraftAttachment(ctx, ownerRequest(f.identity, request))
	if err != nil || reply.Msg.Upload.State != pb.AttachmentState_ATTACHMENT_STATE_DELETING {
		t.Fatal(reply, err)
	}
	// Reconnect the original Worker storage; a disconnected draft stays pending.
	clientCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.WatchAttachmentTransfers(clientCtx, ownerRequest(f.workerIdentity, &pb.WatchAttachmentTransfersRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	done := make(chan error, 1)
	go func() {
		done <- f.service.reconcileImageDraftCleanups(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}))
	}()
	if !stream.Receive() || stream.Msg().Transfer == nil {
		t.Fatal(stream.Err())
	}
	transfer := stream.Msg().Transfer
	data, problem := (imageinput.Manager{Root: root}).Transfer(domain.ID(f.machine.Id), transfer)
	if problem != nil || len(data) != 0 {
		t.Fatal(problem)
	}
	if _, err := c.ReportAttachmentTransfer(ctx, ownerRequest(f.workerIdentity, &pb.ReportAttachmentTransferRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, TransferId: transfer.Id})); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := c.GetUpload(ctx, ownerRequest(f.identity, &pb.GetUploadRequest{AttachmentId: u.Attachment.Id}))
	if err != nil || got.Msg.Upload.State != pb.AttachmentState_ATTACHMENT_STATE_DELETED {
		t.Fatal(got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "image-inputs", u.Attachment.Id+".data")); !os.IsNotExist(err) {
		t.Fatal("bytes retained", err)
	}
	again, err := c.DeleteDraftAttachment(ctx, ownerRequest(f.identity, request))
	if err != nil || !again.Msg.Replayed {
		t.Fatal(again, err)
	}
}
