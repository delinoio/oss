package worker

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
			if domain.Decode(message.Job.DocumentJson, &job) != nil || job.Validate() != nil || job.Type != domain.GenerateSessionTitleJob || job.MachineID != credential.MachineID || job.InstanceID != instance || job.State != domain.JobClaimed || job.AssignedDeviceID != credential.DeviceID {
				return publicationUncertain()
			}
			id := domain.ID(message.Job.Id)
			lock, err := security.TryLock(filepath.Join(config.Root, "jobs", string(id)+".lock"))
			if err != nil {
				return domain.Fail(domain.RecoveryRequired, "The local title operation is already owned or cannot be locked.", "Preserve the Worker journal and reconcile the accepted title job without resending its prompt.")
			}
			jobCtx, stopJob := context.WithCancel(watchCtx)
			if message.CancelRequested {
				stopJob()
			}
			activeMu.Lock()
			if activeID != "" {
				activeMu.Unlock()
				stopJob()
				_ = lock.Close()
				return publicationUncertain()
			}
			activeID, activeCancel, lastAssigned = id, stopJob, id
			activeMu.Unlock()

			jobConfig := config
			jobConfig.execution = &PublicationConfig{Root: config.Root, Credential: credential, Instance: instance, Assignment: message.Job, Client: client, Logger: config.Logger}
			result, runErr := runJob(jobCtx, jobConfig, instance, message.Job, job)
			stopJob()
			activeMu.Lock()
			activeID, activeCancel = "", nil
			activeMu.Unlock()
			if closeErr := lock.Close(); closeErr != nil {
				return domain.Fail(domain.RecoveryRequired, "The local title operation lock could not be released safely.", "Preserve its journal and reconcile the accepted title operation before retrying.")
			}
			if runErr != nil {
				return runErr
			}
			if watchCtx.Err() != nil {
				return context.Cause(watchCtx)
			}
			report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(result.ReportID), Id: string(result.JobID), ExpectedRevision: result.Revision}, MachineId: string(credential.MachineID), InstanceId: string(instance), OutputJson: result.Output}
			if result.Problem != nil {
				report.Problem = &pb.ErrorDetail{Code: string(result.Problem.Code)}
			}
			attempt, stopReport := context.WithTimeout(watchCtx, 30*time.Second)
			_, err = client.ReportWork(attempt, authenticated(credential, report))
			stopReport()
			if err != nil {
				if watchCtx.Err() != nil {
					return context.Cause(watchCtx)
				}
				return err
			}
			result.State = journalReported
			if err := writeJSON(filepath.Join(config.Root, "jobs", string(id)+".json"), result); err != nil {
				return err
			}
			if config.Logger != nil {
				config.Logger.InfoContext(watchCtx, "worker auxiliary title job reported", "machine_id", credential.MachineID, "job_id", id, "reported_problem", result.Problem != nil)
			}
		}
	}
}
