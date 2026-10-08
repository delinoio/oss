// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"net/http"
	"time"
)

type imageTransferReply struct {
	data    []byte
	problem error
}
type imageTransferReader struct {
	authority workspaceReader
	requests  chan *pb.AttachmentTransfer
	pending   *pb.AttachmentTransfer
	reply     chan imageTransferReply
	delivered bool
}

func imageTransferUnavailable() error {
	return domain.Fail(domain.Unavailable, "The image transfer connection is unavailable.", "Reconnect the selected Runner Device and inspect the original upload before retrying.")
}
func (s *Service) transferImage(ctx context.Context, value *pb.AttachmentTransfer) ([]byte, error) {
	ref, err := imageinput.FromProto(value.Attachment)
	if err != nil {
		return nil, err
	}
	s.imageTransfersMu.Lock()
	reader := s.imageTransferReaders[ref.MachineID]
	if reader == nil || reader.pending != nil {
		s.imageTransfersMu.Unlock()
		return nil, imageTransferUnavailable()
	}
	reader.pending = value
	reader.reply = make(chan imageTransferReply, 1)
	reader.delivered = false
	reply := reader.reply
	s.imageTransfersMu.Unlock()
	defer func() {
		s.imageTransfersMu.Lock()
		defer s.imageTransfersMu.Unlock()
		if reader.pending == value {
			reader.pending = nil
			reader.reply = nil
			reader.delivered = false
		}
	}()
	check := func(tx *store.Tx) error {
		if err := currentWorkspaceReader(tx, &reader.authority); err != nil {
			return err
		}
		_, upload, err := imageUploadRecord(tx, ref.ID)
		if err != nil {
			return err
		}
		if upload.Attachment != ref || upload.WorkerDeviceID != reader.authority.device {
			return imageTransferUnavailable()
		}
		return nil
	}
	if err := s.Store.Read(ctx, check); err != nil {
		return nil, err
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	select {
	case reader.requests <- value:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-reader.authority.done:
		return nil, imageTransferUnavailable()
	case <-deadline.C:
		return nil, imageTransferUnavailable()
	}
	select {
	case result := <-reply:
		if err := s.Store.Read(ctx, check); err != nil {
			return nil, err
		}
		return result.data, result.problem
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-reader.authority.done:
		return nil, imageTransferUnavailable()
	case <-deadline.C:
		return nil, imageTransferUnavailable()
	}
}
func (s *Service) WatchAttachmentTransfers(ctx context.Context, req *connect.Request[pb.WatchAttachmentTransfersRequest], stream *connect.ServerStream[pb.WatchAttachmentTransfersResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	primary, err := s.primaryWorkspaceStream(machine, instance)
	if err != nil {
		return rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader := &imageTransferReader{authority: workspaceReader{machine: machine, instance: instance, device: actor.DeviceID, primary: primary.ID, done: ctx.Done()}, requests: make(chan *pb.AttachmentTransfer, 1)}
	if err = s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, &reader.authority) }); err != nil {
		return rpc.Error(err, correlation)
	}
	s.imageTransfersMu.Lock()
	if s.imageTransferReaders == nil {
		s.imageTransferReaders = map[domain.ID]*imageTransferReader{}
	}
	if s.imageTransferReaders[machine] != nil || len(s.imageTransferReaders) >= 1024 {
		s.imageTransfersMu.Unlock()
		return rpc.Error(imageTransferUnavailable(), correlation)
	}
	s.imageTransferReaders[machine] = reader
	s.imageTransfersMu.Unlock()
	defer func() {
		s.imageTransfersMu.Lock()
		defer s.imageTransfersMu.Unlock()
		if s.imageTransferReaders[machine] == reader {
			delete(s.imageTransferReaders, machine)
		}
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(imageTransferUnavailable(), correlation)
	}
	send := func(value *pb.WatchAttachmentTransfersResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(value)
	}
	if err := send(&pb.WatchAttachmentTransfersResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-primary.Done:
			return nil
		case <-ticker.C:
			if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, &reader.authority) }); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchAttachmentTransfersResponse{Heartbeat: true}); err != nil {
				return err
			}
		case value := <-reader.requests:
			s.imageTransfersMu.Lock()
			current := reader.pending == value
			if current {
				reader.delivered = true
			}
			s.imageTransfersMu.Unlock()
			if !current {
				continue
			}
			if err := send(&pb.WatchAttachmentTransfersResponse{Transfer: value}); err != nil {
				return err
			}
		}
	}
}
func (s *Service) ReportAttachmentTransfer(ctx context.Context, req *connect.Request[pb.ReportAttachmentTransferRequest]) (*connect.Response[pb.ReportAttachmentTransferResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	s.imageTransfersMu.Lock()
	defer s.imageTransfersMu.Unlock()
	reader := s.imageTransferReaders[domain.ID(req.Msg.MachineId)]
	if reader == nil || reader.authority.instance != domain.ID(req.Msg.InstanceId) || reader.pending == nil || !reader.delivered || reader.pending.Id != req.Msg.TransferId {
		return nil, rpc.Error(imageTransferUnavailable(), c)
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, &reader.authority) }); err != nil {
		return nil, rpc.Error(err, c)
	}
	result := imageTransferReply{}
	if req.Msg.ProblemCode != "" {
		if len(req.Msg.Data) != 0 || req.Msg.Sha256 != "" {
			return nil, rpc.Error(domain.InvalidImageInput(), c)
		}
		switch domain.Code(req.Msg.ProblemCode) {
		case domain.InvalidArgument:
			result.problem = domain.InvalidImageInput()
		case domain.Conflict:
			result.problem = domain.Fail(domain.Conflict, "The original image transfer conflicts with Worker state.", "Inspect the original upload; preserve uncertain input identities.")
		default:
			result.problem = imageTransferUnavailable()
		}
	}
	if result.problem == nil {
		if reader.pending.Operation == pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ {
			expected := min(uint64(reader.pending.Limit), reader.pending.Attachment.ByteLength-reader.pending.Offset)
			if uint64(len(req.Msg.Data)) != expected || req.Msg.Sha256 != imageinput.Digest(req.Msg.Data) {
				return nil, rpc.Error(domain.InvalidImageInput(), c)
			}
			result.data = append([]byte(nil), req.Msg.Data...)
		} else if len(req.Msg.Data) != 0 || req.Msg.Sha256 != "" {
			return nil, rpc.Error(domain.InvalidImageInput(), c)
		}
	}
	select {
	case reader.reply <- result:
		reader.delivered = false
	default:
		return nil, rpc.Error(imageTransferUnavailable(), c)
	}
	return connect.NewResponse(&pb.ReportAttachmentTransferResponse{}), nil
}
