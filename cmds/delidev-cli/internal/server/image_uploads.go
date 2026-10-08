// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func imageClient(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return actor, domain.Fail(domain.PermissionDenied, "Image uploads require an owner or paired client.", "Use an authenticated product client.")
	}
	return actor, nil
}
func imageUploadRecord(tx *store.Tx, id domain.ID) (store.Record, domain.ImageUpload, error) {
	row, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return row, domain.ImageUpload{}, err
	}
	job, err := store.Decode[domain.Job](row)
	var upload domain.ImageUpload
	if err != nil || job.Type != domain.ImageAttachmentJob || domain.Decode(job.Input, &upload) != nil || upload.Version != 1 || upload.WorkerDeviceID.Validate() != nil || upload.Attachment.ID != id || upload.Attachment.Validate() != nil || upload.Attachment.MachineID != job.MachineID {
		return row, upload, domain.InvalidImageInput()
	}
	return row, upload, nil
}
func putImageUpload(tx *store.Tx, row store.Record, value domain.ImageUpload) (store.Record, error) {
	if err := tx.ValidateImageCapacity(value); err != nil {
		return store.Record{}, err
	}
	raw, _ := json.Marshal(value)
	job := domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: value.Attachment.MachineID, Input: raw, AcceptedAt: time.Now().UTC()}
	if row.Revision > 0 {
		original, err := store.Decode[domain.Job](row)
		if err != nil || original.Type != domain.ImageAttachmentJob || original.MachineID != value.Attachment.MachineID {
			return store.Record{}, domain.InvalidImageInput()
		}
		job = original
		job.Input = raw
	}
	return tx.PutJob(value.Attachment.ID, row.Revision, "", "", job)
}
func ownedImageUpload(tx *store.Tx, id domain.ID, actor domain.Principal) (store.Record, domain.ImageUpload, error) {
	row, value, err := imageUploadRecord(tx, id)
	if err == nil && value.Actor != actor {
		err = domain.Fail(domain.PermissionDenied, "This image belongs to another draft owner.", "Use the original authenticated image operation.")
	}
	return row, value, err
}
func imageUploadMessage(value domain.ImageUpload) *pb.AttachmentUpload {
	state := map[domain.ImageUploadState]pb.AttachmentState{domain.ImageUploading: pb.AttachmentState_ATTACHMENT_STATE_UPLOADING, domain.ImageReady: pb.AttachmentState_ATTACHMENT_STATE_READY, domain.ImageClaimed: pb.AttachmentState_ATTACHMENT_STATE_CLAIMED, domain.ImageDeleting: pb.AttachmentState_ATTACHMENT_STATE_DELETING, domain.ImageDeleted: pb.AttachmentState_ATTACHMENT_STATE_DELETED}[value.State]
	return &pb.AttachmentUpload{Attachment: imageinput.ToProto(value.Attachment), State: state, UploadedBytes: value.UploadedBytes, DraftId: string(value.DraftID), OperationId: string(value.OperationID)}
}
func (s *Service) readOwnedImageUpload(ctx context.Context, id domain.ID) (domain.ImageUpload, error) {
	actor, err := imageClient(ctx)
	if err != nil {
		return domain.ImageUpload{}, err
	}
	var value domain.ImageUpload
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, value, err = ownedImageUpload(tx, id, actor)
		return err
	})
	if err == nil && value.Quarantined && value.State != domain.ImageDeleting && value.State != domain.ImageDeleted {
		err = domain.Fail(domain.RecoveryRequired, "Restored images require original cleanup reconciliation.", "Retain the original session and Runner cleanup obligations.")
	}
	return value, err
}
func (s *Service) BeginUpload(ctx context.Context, req *connect.Request[pb.BeginUploadRequest]) (*connect.Response[pb.BeginUploadResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := imageClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	q := req.Msg
	for _, id := range []string{q.RequestId, q.DraftId, q.OperationId, q.MachineId} {
		if domain.ID(id).Validate() != nil {
			return nil, rpc.Error(domain.InvalidImageInput(), c)
		}
	}
	if q.MachineRevision == 0 || q.SessionId != "" && domain.ID(q.SessionId).Validate() != nil {
		return nil, rpc.Error(domain.InvalidImageInput(), c)
	}
	prototype := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(q.MachineId), MediaType: imageinput.MediaType(q.MediaType), ByteLength: q.ByteLength, SHA256: q.Sha256}
	if err := prototype.Validate(); err != nil {
		return nil, rpc.Error(err, c)
	}
	identity := struct {
		Actor   domain.Principal
		Request *pb.BeginUploadRequest
	}{actor, q}
	result, err := s.Store.Mutate(ctx, domain.ID(q.RequestId), "image.begin", identity, func(tx *store.Tx) (any, error) {
		row, machine, err := activeMachine(tx, prototype.MachineID)
		if err != nil {
			return nil, err
		}
		if row.Revision != q.MachineRevision {
			return nil, domain.Fail(domain.Conflict, "The selected Runner Device revision changed.", "Refresh the explicit Runner selection before starting a new image upload.")
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.ImageInputsV1) {
			return nil, domain.UnsupportedImageInput()
		}
		if err := tx.WorkerUpdateAdmission(prototype.MachineID); err != nil {
			return nil, err
		}
		if q.SessionId != "" {
			_, session, err := sessionRecord(tx, domain.ID(q.SessionId))
			if err != nil {
				return nil, err
			}
			if session.MachineID != prototype.MachineID || session.Archive != domain.NotArchived || session.IsSidechat() {
				return nil, domain.UnsupportedImageInput()
			}
		}
		device, err := tx.InstallationWorkerDevice(prototype.MachineID)
		if err != nil {
			return nil, err
		}
		value := domain.ImageUpload{WorkerDeviceID: device, Version: 1, Attachment: prototype, Actor: actor, DraftID: domain.ID(q.DraftId), OperationID: domain.ID(q.OperationId), MachineRevision: q.MachineRevision, SessionID: domain.ID(q.SessionId), State: domain.ImageUploading}
		if err := validateImageDraftBound(tx, value); err != nil {
			return nil, err
		}
		if _, err := putImageUpload(tx, store.Record{}, value); err != nil {
			return nil, err
		}
		return prototype.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var id domain.ID
	if json.Unmarshal(result.Data, &id) != nil {
		return nil, rpc.Error(domain.InvalidImageInput(), c)
	}
	value, err := s.readOwnedImageUpload(ctx, id)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "image_upload_started", "attachment_id", id, "machine_id", value.Attachment.MachineID, "replayed", result.Replayed)
	return connect.NewResponse(&pb.BeginUploadResponse{Upload: imageUploadMessage(value), Replayed: result.Replayed}), nil
}
func (s *Service) WriteChunk(ctx context.Context, req *connect.Request[pb.WriteChunkRequest]) (*connect.Response[pb.WriteChunkResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	q := req.Msg
	value, err := s.readOwnedImageUpload(ctx, domain.ID(q.AttachmentId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if value.Quarantined || value.State != domain.ImageUploading || len(q.Data) == 0 || len(q.Data) > domain.MaxImageChunkBytes || q.Sha256 != imageinput.Digest(q.Data) || q.Offset > value.UploadedBytes || q.Offset > value.Attachment.ByteLength || uint64(len(q.Data)) > value.Attachment.ByteLength-q.Offset {
		return nil, rpc.Error(domain.InvalidImageInput(), c)
	}
	transfer := &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(value.Attachment), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_WRITE, Offset: q.Offset, Data: q.Data, Sha256: q.Sha256}
	if _, err = s.transferImage(ctx, transfer); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := imageClient(ctx)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "image.chunk", struct {
		ID     domain.ID
		Offset uint64
		Length int
		Digest string
	}{value.Attachment.ID, q.Offset, len(q.Data), q.Sha256}, func(tx *store.Tx) (any, error) {
		row, current, err := ownedImageUpload(tx, value.Attachment.ID, actor)
		if err != nil {
			return nil, err
		}
		if current.State != domain.ImageUploading || current.Attachment != value.Attachment {
			return nil, domain.InvalidImageInput()
		}
		if q.Offset > current.UploadedBytes {
			return nil, domain.InvalidImageInput()
		}
		current.UploadedBytes = max(current.UploadedBytes, q.Offset+uint64(len(q.Data)))
		_, err = putImageUpload(tx, row, current)
		return current.Attachment.ID, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	value, err = s.readOwnedImageUpload(ctx, value.Attachment.ID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.WriteChunkResponse{Upload: imageUploadMessage(value)}), nil
}
func (s *Service) FinishUpload(ctx context.Context, req *connect.Request[pb.FinishUploadRequest]) (*connect.Response[pb.FinishUploadResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	value, err := s.readOwnedImageUpload(ctx, domain.ID(req.Msg.AttachmentId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if value.Quarantined {
		return nil, rpc.Error(domain.InvalidImageInput(), c)
	}
	if value.State == domain.ImageReady || value.State == domain.ImageClaimed {
		return connect.NewResponse(&pb.FinishUploadResponse{Upload: imageUploadMessage(value)}), nil
	}
	if value.Quarantined || value.State != domain.ImageUploading || value.UploadedBytes != value.Attachment.ByteLength {
		return nil, rpc.Error(domain.InvalidImageInput(), c)
	}
	if _, err = s.transferImage(ctx, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(value.Attachment), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH}); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := imageClient(ctx)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "image.finish", value.Attachment, func(tx *store.Tx) (any, error) {
		row, current, err := ownedImageUpload(tx, value.Attachment.ID, actor)
		if err != nil {
			return nil, err
		}
		if current.Attachment != value.Attachment || current.State != domain.ImageUploading && current.State != domain.ImageReady {
			return nil, domain.InvalidImageInput()
		}
		current.State = domain.ImageReady
		_, err = putImageUpload(tx, row, current)
		return current.Attachment.ID, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	value, err = s.readOwnedImageUpload(ctx, value.Attachment.ID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "image_upload_ready", "attachment_id", value.Attachment.ID, "machine_id", value.Attachment.MachineID)
	return connect.NewResponse(&pb.FinishUploadResponse{Upload: imageUploadMessage(value)}), nil
}
func (s *Service) GetUpload(ctx context.Context, req *connect.Request[pb.GetUploadRequest]) (*connect.Response[pb.GetUploadResponse], error) {
	value, err := s.readOwnedImageUpload(ctx, domain.ID(req.Msg.AttachmentId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetUploadResponse{Upload: imageUploadMessage(value)}), nil
}
func (s *Service) DeleteDraftAttachment(ctx context.Context, req *connect.Request[pb.DeleteDraftAttachmentRequest]) (*connect.Response[pb.DeleteDraftAttachmentResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := imageClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	identity := struct {
		Actor domain.Principal
		ID    string
	}{actor, req.Msg.AttachmentId}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "image.delete-draft", identity, func(tx *store.Tx) (any, error) {
		row, value, err := ownedImageUpload(tx, domain.ID(req.Msg.AttachmentId), actor)
		if err != nil {
			return nil, err
		}
		if value.State == domain.ImageClaimed || len(value.Owners) > 0 {
			return nil, domain.Fail(domain.Conflict, "Accepted images belong to their retained sessions.", "Use permanent session deletion for accepted content.")
		}
		if value.State != domain.ImageDeleted {
			value.State = domain.ImageDeleting
			if _, err = putImageUpload(tx, row, value); err != nil {
				return nil, err
			}
		}
		return value.Attachment.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	value, err := s.readOwnedImageUpload(ctx, domain.ID(req.Msg.AttachmentId))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if value.State == domain.ImageDeleting {
		_ = s.cleanupImageUpload(ctx, value)
		value, err = s.readOwnedImageUpload(ctx, value.Attachment.ID)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	return connect.NewResponse(&pb.DeleteDraftAttachmentResponse{Upload: imageUploadMessage(value), Replayed: result.Replayed}), nil
}
func (s *Service) cleanupImageUpload(ctx context.Context, value domain.ImageUpload) error {
	if value.State != domain.ImageDeleting || value.InputID != "" || len(value.Owners) != 0 {
		return domain.InvalidImageInput()
	}
	if _, err := s.transferImage(ctx, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(value.Attachment), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE}); err != nil {
		return err
	}
	_, err := s.Store.Mutate(ctx, domain.NewID(), "image.removal-observed", value.Attachment, func(tx *store.Tx) (any, error) {
		row, current, err := imageUploadRecord(tx, value.Attachment.ID)
		if err != nil {
			return nil, err
		}
		if current.State == domain.ImageDeleted {
			return current.Attachment.ID, nil
		}
		if current.State != domain.ImageDeleting || current.InputID != "" || len(current.Owners) != 0 || current.Attachment != value.Attachment {
			return nil, domain.InvalidImageInput()
		}
		current.State = domain.ImageDeleted
		_, err = putImageUpload(tx, row, current)
		return current.Attachment.ID, err
	})
	if err == nil {
		s.logger.InfoContext(ctx, "image_removal_observed", "attachment_id", value.Attachment.ID, "machine_id", value.Attachment.MachineID)
	}
	return err
}
func (s *Service) ReadAttachment(ctx context.Context, req *connect.Request[pb.ReadAttachmentRequest]) (*connect.Response[pb.ReadAttachmentResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if _, err := imageClient(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	q := req.Msg
	var value domain.ImageUpload
	check := func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, session, err := sessionRecord(tx, domain.ID(q.SessionId))
		if err != nil {
			return err
		}
		_, value, err = imageUploadRecord(tx, domain.ID(q.AttachmentId))
		if err != nil {
			return err
		}
		if value.Quarantined || value.State != domain.ImageClaimed || tx.ImageAttachmentReadable(domain.ID(q.SessionId), value.Attachment) != nil || session.MachineID != value.Attachment.MachineID || q.Limit == 0 || q.Limit > domain.MaxImageChunkBytes || q.Offset > value.Attachment.ByteLength {
			return domain.InvalidImageInput()
		}
		return nil
	}
	if err := s.Store.Read(ctx, check); err != nil {
		return nil, rpc.Error(err, c)
	}
	data, err := s.transferImage(ctx, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(value.Attachment), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ, Offset: q.Offset, Limit: q.Limit})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if err = s.Store.Read(ctx, check); err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.ReadAttachmentResponse{Data: data, Sha256: imageinput.Digest(data), Complete: q.Offset+uint64(len(data)) == value.Attachment.ByteLength}), nil
}
