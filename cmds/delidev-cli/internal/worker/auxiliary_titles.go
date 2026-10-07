package worker

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func watchAuxiliary(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) error {
	watchCtx, cancel := context.WithCancelCause(ctx)
	deadline := time.AfterFunc(domain.WorkerConnectionTimeout, func() {
		cancel(domain.Fail(domain.Unavailable, "The auxiliary Worker heartbeat expired.", "Reconnect and reconcile the accepted title operation before further inference."))
	})
	defer deadline.Stop()
	defer cancel(context.Canceled)
	stream, err := client.WatchAuxiliaryWork(watchCtx, authenticated(credential, &pb.WatchAuxiliaryWorkRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		if watchCtx.Err() != nil {
			return context.Cause(watchCtx)
		}
		return err
	}
	type received struct {
		message *pb.WatchAuxiliaryWorkResponse
		err     error
	}
	messages := make(chan received, 4)
	recvDone := make(chan struct{})
	var activeMu sync.Mutex
	var activeID domain.ID
	var activeCancel context.CancelFunc
	go func() {
		defer close(recvDone)
		for stream.Receive() {
			deadline.Reset(domain.WorkerConnectionTimeout)
			message := proto.Clone(stream.Msg()).(*pb.WatchAuxiliaryWorkResponse)
			if message.CancelJobId != "" {
				activeMu.Lock()
				if activeCancel != nil && activeID == domain.ID(message.CancelJobId) {
					activeCancel()
				}
				activeMu.Unlock()
			}
			select {
			case messages <- received{message: message}:
			case <-watchCtx.Done():
				return
			}
		}
		err := stream.Err()
		if err == nil {
			err = domain.Fail(domain.Unavailable, "The auxiliary Worker stream ended.", "Reconnect and reconcile the accepted title operation.")
		}
		select {
		case messages <- received{err: err}:
		case <-watchCtx.Done():
		}
	}()
	defer func() { cancel(context.Canceled); _ = stream.Close(); <-recvDone }()

	var lastAssigned domain.ID
	for {
		select {
		case <-watchCtx.Done():
			return context.Cause(watchCtx)
		case item := <-messages:
			if item.err != nil {
				if watchCtx.Err() != nil {
					return context.Cause(watchCtx)
				}
				return item.err
			}
			message := item.message
			if message == nil {
				return publicationUncertain()
			}
			if message.CancelJobId != "" {
				cancelID := domain.ID(message.CancelJobId)
				if message.Job != nil || message.Heartbeat || message.CancelRequested || cancelID.Validate() != nil || cancelID != lastAssigned {
					return publicationUncertain()
				}
				continue
			}
			if message.Job == nil {
				if !message.Heartbeat || message.CancelRequested {
					return publicationUncertain()
				}
				continue
			}
			if message.Heartbeat || message.Job.Kind != pb.EntityKind_ENTITY_KIND_JOB || message.Job.SchemaVersion != 1 || message.Job.Revision == 0 || domain.ID(message.Job.Id).Validate() != nil {
				return publicationUncertain()
			}
			var job domain.Job
			if domain.Decode(message.Job.DocumentJson, &job) != nil || job.Validate() != nil || job.Type != domain.GenerateSessionTitleJob ||
				domain.OwnershipBlocks(domain.OwnershipMachine, "", job.MachineID != credential.MachineID) ||
				domain.OwnershipBlocks(domain.OwnershipInstance, "", job.InstanceID != instance) ||
				job.State != domain.JobClaimed ||
				domain.OwnershipBlocks(domain.OwnershipDevice, "", job.AssignedDeviceID != credential.DeviceID) {
				return publicationUncertain()
			}
			id := domain.ID(message.Job.Id)
			jobCtx, stopJob := context.WithCancel(watchCtx)
			if message.CancelRequested {
				stopJob()
			}
			activeMu.Lock()
			if activeID != "" {
				activeMu.Unlock()
				stopJob()
				return publicationUncertain()
			}
			activeID, activeCancel, lastAssigned = id, stopJob, id
			activeMu.Unlock()

			work := assignment{context: jobCtx, cancel: stopJob}
			runErr := runAndReportJob(watchCtx, config, client, credential, instance, work, message.Job, job)
			activeMu.Lock()
			activeID, activeCancel = "", nil
			activeMu.Unlock()
			if runErr != nil {
				return runErr
			}
		}
	}
}
