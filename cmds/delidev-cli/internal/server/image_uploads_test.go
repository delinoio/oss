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
	"slices"
	"testing"
	"time"
)

func imageRPCFixture(t *testing.T, harnesses ...domain.Harness) (*firstDispatchFixture, delidevv1connect.AttachmentServiceClient, []byte) {
	t.Helper()
	harness := domain.Codex
	if len(harnesses) == 1 {
		harness = harnesses[0]
	}
	f := newFirstDispatchFixtureForHarness(t, harness)
	ctx := context.Background()
	_, err := f.workerClient.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_INSTRUCTIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1, pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1}}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.service.Store.Mutate(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), domain.NewID(), "fixture.image-model", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if err != nil {
			return nil, err
		}
		agent, err := store.Decode[domain.Agent](row)
		if err != nil {
			return nil, err
		}
		modelRow, err := tx.Get(domain.ModelKind, agent.ModelID)
		if err != nil {
			return nil, err
		}
		model, err := store.Decode[domain.Model](modelRow)
		if err != nil {
			return nil, err
		}
		model.InputModalities = []string{"text", "image"}
		return tx.Put(domain.ModelKind, modelRow.ID, modelRow.Revision, "", "", model)
	})
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

	_, err = f.service.Store.Mutate(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), domain.NewID(), "fixture.changed-model-metadata", nil, func(tx *store.Tx) (any, error) {
		agentRow, err := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if err != nil {
			return nil, err
		}
		agent, err := store.Decode[domain.Agent](agentRow)
		if err != nil {
			return nil, err
		}
		row, err := tx.Get(domain.ModelKind, agent.ModelID)
		if err != nil {
			return nil, err
		}
		model, err := store.Decode[domain.Model](row)
		if err != nil {
			return nil, err
		}
		model.InputModalities = []string{"text"}
		return tx.Put(domain.ModelKind, row.ID, row.Revision, "", "", model)
	})
	if err != nil {
		t.Fatal(err)
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

	explicit := &pb.EditQueuedInputRequest{Mutation: acctMutation(edit.Msg.Change.Input, domain.NewID()), SessionId: change.Session.Id, Prompt: "", Attachments: []*pb.ImageAttachment{second.Attachment, first.Attachment}}
	explicitReply, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, explicit))
	if err != nil {
		t.Fatal("typed image-only edit rejected", err)
	}
	var typed domain.QueuedInput
	if domain.Decode(explicitReply.Msg.Change.Input.DocumentJson, &typed) != nil || typed.Prompt != "" || !slices.Equal(typed.Attachments, queued.Attachments) {
		t.Fatal("typed edit replaced original images", typed)
	}
	typedReplay, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, explicit))
	if err != nil || !typedReplay.Msg.Change.Replayed || typedReplay.Msg.Change.Input.Revision != explicitReply.Msg.Change.Input.Revision {
		t.Fatal("typed edit exact retry lost", typedReplay, err)
	}
	for _, refs := range [][]*pb.ImageAttachment{{first.Attachment, second.Attachment}, {second.Attachment}} {
		rejected := &pb.EditQueuedInputRequest{Mutation: acctMutation(explicitReply.Msg.Change.Input, domain.NewID()), SessionId: change.Session.Id, Prompt: "changed", Attachments: refs}
		if _, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, rejected)); connect.CodeOf(err) != connect.CodeAborted {
			t.Fatal("reordered or removed accepted image admitted", err)
		}
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

func TestUnsupportedImageRoutesNeverClaimOrCreateNativeWork(t *testing.T) {
	for _, harness := range []domain.Harness{domain.ClaudeCode, domain.OpenCode, domain.GrokBuild} {
		t.Run(string(harness), func(t *testing.T) {
			f, c, raw := imageRPCFixture(t, harness)
			imageWorkerFixture(t, f, c)
			operation := domain.NewID()
			u := finishImage(t, f, c, beginImage(t, f, c, raw, domain.NewID(), operation, ""), raw)
			selection := f.selection
			selection.Prompt = ""
			document, _ := json.Marshal(selection)
			_, err := sessionClient(f.accountFixture).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: document, Attachments: []*pb.ImageAttachment{u.Attachment}}))
			if connect.CodeOf(err) != connect.CodeUnimplemented {
				t.Fatal("unsupported image accepted", err)
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				_, upload, err := imageUploadRecord(tx, domain.ID(u.Attachment.Id))
				if err != nil {
					return err
				}
				if upload.State != domain.ImageReady || len(upload.Owners) != 0 || upload.InputID != "" {
					t.Fatal("unsupported route claimed", upload)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestStagedImageRejectsChangedRunnerRevisionAndDevice(t *testing.T) {
	for _, changeDevice := range []bool{false, true} {
		t.Run(map[bool]string{false: "revision", true: "device"}[changeDevice], func(t *testing.T) {
			f, c, raw := imageRPCFixture(t)
			imageWorkerFixture(t, f, c)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			operation := domain.NewID()
			u := finishImage(t, f, c, beginImage(t, f, c, raw, domain.NewID(), operation, ""), raw)
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.changed-runner", nil, func(tx *store.Tx) (any, error) {
				if changeDevice {
					row, value, err := imageUploadRecord(tx, domain.ID(u.Attachment.Id))
					if err != nil {
						return nil, err
					}
					value.WorkerDeviceID = domain.NewID()
					return putImageUpload(tx, row, value)
				}
				row, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				machine.Name = "Changed explicit Runner selection"
				return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			selection := f.selection
			document, _ := json.Marshal(selection)
			_, err = sessionClient(f.accountFixture).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: document, Attachments: []*pb.ImageAttachment{u.Attachment}}))
			if err == nil {
				t.Fatal("stale staging claimed")
			}
			if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
				_, value, err := imageUploadRecord(tx, domain.ID(u.Attachment.Id))
				if err == nil && (value.State != domain.ImageReady || len(value.Owners) != 0) {
					t.Fatal("stale staging mutated", value)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestImageStagingBoundRejectsNinthBeforeWorkerWrite(t *testing.T) {
	f, c, raw := imageRPCFixture(t)
	draft, operation := domain.NewID(), domain.NewID()
	for range domain.MaxInputImages {
		beginImage(t, f, c, raw, draft, operation, "")
	}
	_, err := c.BeginUpload(context.Background(), ownerRequest(f.identity, &pb.BeginUploadRequest{RequestId: string(domain.NewID()), DraftId: string(draft), OperationId: string(operation), MachineId: f.machine.Id, MachineRevision: f.machine.Revision, MediaType: pb.ImageMediaType_IMAGE_MEDIA_TYPE_PNG, ByteLength: uint64(len(raw)), Sha256: imageinput.Digest(raw)}))
	if err == nil {
		t.Fatal("ninth staged")
	}
}

func TestImageMixedSourcesAndImmutableDeclaredSnapshot(t *testing.T) {
	f, _, raw := imageRPCFixture(t)
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: imageinput.Digest(raw)}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.mixed-image-sources", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if err != nil {
			return nil, err
		}
		agent, err := store.Decode[domain.Agent](row)
		if err != nil {
			return nil, err
		}
		modelRow, err := tx.Get(domain.ModelKind, agent.ModelID)
		if err != nil {
			return nil, err
		}
		model, err := store.Decode[domain.Model](modelRow)
		if err != nil {
			return nil, err
		}
		model.InputModalities = []string{"text"}
		model.NativeID += "-text-only"
		other := domain.NewID()
		if _, err := tx.Put(domain.ModelKind, other, 0, "", "", model); err != nil {
			return nil, err
		}
		first := agent.SourceRoutes()[0]
		second := first
		second.ModelID = other
		agent.Routes = []domain.AgentSourceRoute{first, second}
		agent.ModelID = ""
		agent.Accounts = nil
		agent.Routing = nil
		return tx.Put(domain.AgentKind, row.ID, row.Revision, "", "", agent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		return checkImageRoute(tx, domain.ID(f.agent.Id), domain.ID(f.machine.Id), []domain.ImageAttachment{ref})
	}); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("mixed unsupported model admitted", err)
	}
	snapshot := domain.Session{AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id), InitialExecution: &domain.InitialExecution{Configuration: domain.ExecutionConfiguration{Harness: domain.Codex, ImageInputDeclared: true}}}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error { return checkSessionImageRoute(tx, snapshot, []domain.ImageAttachment{ref}) }); err != nil {
		t.Fatal("mutable metadata replaced original declaration", err)
	}
	snapshot.InitialExecution.Configuration.ImageInputDeclared = false
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error { return checkSessionImageRoute(tx, snapshot, []domain.ImageAttachment{ref}) }); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unknown original modality admitted", err)
	}
}

func TestImageSessionCapacityRejectsBeforeStagingAndPreservesExactReceipt(t *testing.T) {
	f, c, raw := imageRPCFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	session := domain.NewID()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.session-image-capacity", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, "", "", domain.Session{Name: "Capacity fixture", MachineID: domain.ID(f.machine.Id), Archive: domain.NotArchived}); err != nil {
			return nil, err
		}
		device, err := tx.InstallationWorkerDevice(domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		for i := 0; i < domain.MaxSessionImageAttachments-1; i++ {
			v := domain.ImageUpload{Version: 1, Attachment: domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: imageinput.Digest(raw)}, WorkerDeviceID: device, Actor: domain.Principal{Type: domain.OwnerDevice}, DraftID: domain.NewID(), OperationID: domain.NewID(), MachineRevision: f.machine.Revision, SessionID: session, State: domain.ImageReady, UploadedBytes: uint64(len(raw))}
			body, _ := json.Marshal(v)
			if _, err := tx.PutJob(v.Attachment.ID, 0, "", "", domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: v.Attachment.MachineID, Input: body, AcceptedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.BeginUploadRequest{RequestId: string(domain.NewID()), DraftId: string(domain.NewID()), OperationId: string(domain.NewID()), MachineId: f.machine.Id, MachineRevision: f.machine.Revision, SessionId: string(session), MediaType: pb.ImageMediaType_IMAGE_MEDIA_TYPE_PNG, ByteLength: uint64(len(raw)), Sha256: imageinput.Digest(raw)}
	admitted, err := c.BeginUpload(context.Background(), ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.BeginUpload(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Upload.Attachment.Id != admitted.Msg.Upload.Attachment.Id {
		t.Fatal("boundary receipt was not idempotent", err)
	}
	overflow := &pb.BeginUploadRequest{RequestId: string(domain.NewID()), DraftId: string(domain.NewID()), OperationId: string(domain.NewID()), MachineId: request.MachineId, MachineRevision: request.MachineRevision, SessionId: request.SessionId, MediaType: request.MediaType, ByteLength: request.ByteLength, Sha256: request.Sha256}
	if _, err := c.BeginUpload(context.Background(), ownerRequest(f.identity, overflow)); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("overflow staged: %v", err)
	}
	// A rejected attempt creates neither an accepted receipt nor a new attachment.
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		var after domain.ID
		for {
			rows, err := tx.List(store.Filter{Kind: domain.JobKind, After: after, Limit: store.MaxPage})
			if err != nil {
				return err
			}
			for _, row := range rows {
				after = row.ID
				job, _ := store.Decode[domain.Job](row)
				var v domain.ImageUpload
				if job.Type == domain.ImageAttachmentJob && domain.Decode(job.Input, &v) == nil && string(v.OperationID) == overflow.OperationId {
					t.Fatal("rejected upload metadata was written")
				}
			}
			if len(rows) < store.MaxPage {
				return nil
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
}
