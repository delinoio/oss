// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"time"
)

func watchImageTransfers(ctx context.Context, config Config, client delidevv1connect.AttachmentServiceClient, credential Credential, instance domain.ID) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := receiveImageTransfers(ctx, config, client, credential, instance)
		if ctx.Err() != nil {
			return
		}
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "image_transfer_interrupted", "machine_id", credential.MachineID, "code", rpc.ClientError(err).Code)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(15*time.Second, backoff*2)
	}
}
func receiveImageTransfers(ctx context.Context, config Config, client delidevv1connect.AttachmentServiceClient, credential Credential, instance domain.ID) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline := time.AfterFunc(domain.WorkerConnectionTimeout, cancel)
	defer deadline.Stop()
	stream, err := client.WatchAttachmentTransfers(ctx, authenticated(credential, &pb.WatchAttachmentTransfersRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	manager := imageinput.Manager{Root: config.Root}
	for stream.Receive() {
		deadline.Reset(domain.WorkerConnectionTimeout)
		message := stream.Msg()
		if message.Heartbeat {
			if message.Transfer != nil {
				return domain.InvalidImageInput()
			}
			continue
		}
		if message.Transfer == nil {
			return domain.InvalidImageInput()
		}
		value := message.Transfer
		data, problem := manager.Transfer(credential.MachineID, value)
		report := &pb.ReportAttachmentTransferRequest{MachineId: string(credential.MachineID), InstanceId: string(instance), TransferId: value.Id}
		if problem != nil {
			report.ProblemCode = string(domain.SafeError(problem).Code)
		} else if value.Operation == pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ {
			report.Data = data
			report.Sha256 = imageinput.Digest(data)
		}
		attempt, stop := context.WithTimeout(ctx, 5*time.Second)
		_, err = client.ReportAttachmentTransfer(attempt, authenticated(credential, report))
		stop()
		if err != nil && ctx.Err() != nil {
			return err
		}
		if config.Logger != nil {
			config.Logger.InfoContext(ctx, "image_transfer_observed", "attachment_id", value.GetAttachment().GetId(), "machine_id", credential.MachineID, "phase", value.Operation.String(), "code", report.ProblemCode)
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return domain.Fail(domain.Unavailable, "Image transfer stream disconnected.", "Reconnect the original Runner Device.")
}
