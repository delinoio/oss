package server

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const maxActiveTitleJobs = 4

func countClaimedTitleJobs(tx *store.Tx) (int, error) {
	count := 0
	var after domain.ID
	for {
		records, err := tx.Jobs("", "", domain.JobClaimed, after, store.MaxPage)
		if err != nil {
			return 0, err
		}
		for _, record := range records {
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return 0, err
			}
			if job.Type == domain.GenerateSessionTitleJob {
				count++
			}
			after = record.ID
		}
		if len(records) < store.MaxPage || count >= maxActiveTitleJobs {
			return count, nil
		}
	}
}

func retireQueuedTitle(tx *store.Tx, record store.Record, job domain.Job, state domain.JobState, titleState domain.SessionTitleState, reason domain.SessionTitleReason, problem *domain.Error) (store.Record, error) {
	now := time.Now().UTC()
	job.State, job.FinishedAt, job.Problem = state, &now, problem
	updated, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job)
	if err != nil {
		return store.Record{}, err
	}
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return store.Record{}, err
	}
	if session.TitleJobID == record.ID && session.NameOwner == domain.AutomaticNameOwner {
		session.TitleState, session.TitleReason = titleState, reason
		if session.Archive == domain.ArchivePending && session.ActiveExecutionID == "" && session.Recovery == domain.NoRecovery {
			stopped, err := stopSessionWorkspace(tx, &session)
			if err != nil {
				return store.Record{}, err
			}
			if stopped && session.Recovery == domain.NoRecovery {
				session.Archive = domain.Archived
			}
		}
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return store.Record{}, err
		}
	}
	return updated, nil
}

func claimTitleJob(ctx context.Context, s *Service, machine, instance, device, jobID domain.ID) (store.Record, error) {
	result, err := s.Store.Mutate(ctx, domain.NewID(), "worker.auxiliary-title.claim", struct{ Machine, Instance, Device, Job domain.ID }{machine, instance, device, jobID}, func(tx *store.Tx) (any, error) {
		if err := currentInstance(tx, machine, instance); err != nil {
			return nil, err
		}
		if err := workerNetworkAdmission(tx, machine); err != nil {
			return nil, err
		}
		record, err := tx.Get(domain.JobKind, jobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil || job.Type != domain.GenerateSessionTitleJob || job.State != domain.JobQueued || job.MachineID != machine {
			return nil, domain.Fail(domain.Conflict, "The queued title operation changed before Worker assignment.", "Inspect its retained state; do not create a replacement title operation.")
		}
		_, currentMachine, err := activeMachine(tx, machine)
		if err != nil {
			return nil, err
		}
		if !hasTitleCapability(currentMachine.WorkerCapabilities) {
			updated, err := retireQueuedTitle(tx, record, job, domain.JobFailed, domain.TitleUnsupported, domain.TitleReasonCapabilityAbsent, domain.Fail(domain.Unsupported, "The Worker title capability is no longer active.", "Keep the placeholder and reconnect a verified Worker."))
			return updated, err
		}
		count, err := countClaimedTitleJobs(tx)
		if err != nil {
			return nil, err
		}
		if count >= maxActiveTitleJobs {
			return nil, domain.Fail(domain.ResourceExhausted, "The bounded title execution lane is full.", "Keep this title durably queued until an auxiliary Worker finishes.")
		}
		active, err := tx.Jobs(machine, "", domain.JobClaimed, "", store.MaxPage)
		if err != nil {
			return nil, err
		}
		for _, activeRecord := range active {
			activeJob, err := store.Decode[domain.Job](activeRecord)
			if err != nil {
				return nil, err
			}
			if activeJob.Type == domain.GenerateSessionTitleJob {
				return nil, domain.Fail(domain.ResourceExhausted, "This Worker already owns an active title operation.", "Keep later title work durably queued on its original Worker.")
			}
		}
		var input domain.AuxiliaryTitleInput
		if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.SessionID != record.SessionID || input.ProjectID != record.ProjectID || input.MachineID != machine || input.OriginalJobID != job.ParentID {
			updated, err := retireQueuedTitle(tx, record, job, domain.JobFailed, domain.TitleFailed, domain.TitleReasonInvalidOutput, domain.Fail(domain.InvalidArgument, "The immutable title assignment is inconsistent.", "Preserve the accepted session and inspect its original execution."))
			return updated, err
		}
		sr, session, err := sessionRecord(tx, record.SessionID)
		if err != nil {
			return nil, err
		}
		if session.TitleJobID != record.ID || session.TitleOperationID != input.OperationID || session.NameOwner != domain.AutomaticNameOwner || session.NameGeneration != input.NameGeneration || session.TitleState != domain.TitleQueued || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery {
			updated, err := retireQueuedTitle(tx, record, job, domain.JobCanceled, domain.TitleSkipped, domain.TitleReasonCanceled, domain.Fail(domain.Canceled, "The title operation no longer owns an active session.", "Keep its current title and do not run another inference."))
			return updated, err
		}
		if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
			if domain.SafeError(err).Code == domain.ResourceExhausted {
				updated, retireErr := retireQueuedTitle(tx, record, job, domain.JobCanceled, domain.TitleSkipped, domain.TitleReasonBudgetReached, domain.SafeError(err))
				return updated, retireErr
			}
			return nil, err
		}
		parent, err := tx.Get(domain.JobKind, job.ParentID)
		if err != nil {
			return nil, err
		}
		originalJob, err := store.Decode[domain.Job](parent)
		var original domain.ExecutionJobInput
		originalGrant, grantErr := tx.ExecutionGrantForJob(job.ParentID)
		if err != nil || grantErr != nil || originalJob.Type != domain.ExecuteSessionJob || originalJob.State != domain.JobSucceeded || originalJob.MachineID != machine || originalJob.InstanceID != input.OriginalInstanceID || originalJob.AssignedDeviceID != input.OriginalDeviceID || input.OriginalInstanceID != instance || input.OriginalDeviceID != device || originalGrant.InstanceID != input.OriginalInstanceID || originalGrant.DeviceID != input.OriginalDeviceID || originalGrant.ServerEpoch != s.executionAuthority.epoch || domain.Decode(originalJob.Input, &original) != nil || original.Validate() != nil || original.ExecutionID != input.OriginalExecutionID || original.Configuration.AgentID != input.AgentID || original.Configuration.Harness != input.Harness || original.Installation.Version != input.NativeVersion || original.AccountID != input.AccountID || original.ConnectionID != input.ConnectionID || original.Configuration.ProviderID != input.ProviderID || original.Configuration.ModelID != input.ModelID || original.Configuration.NativeModel != input.NativeModel || original.Installation.ResolvedPath != input.Executable {
			updated, err := retireQueuedTitle(tx, record, job, domain.JobCanceled, domain.TitleSkipped, domain.TitleReasonAuthorityLost, domain.Fail(domain.PermissionDenied, "The title job no longer matches its original successful execution.", "Keep the placeholder; no original Agent, account, model, executable or Worker may be substituted."))
			return updated, err
		}
		if _, available, err := titleProfileAvailable(tx, original); err != nil || !available {
			updated, retireErr := retireQueuedTitle(tx, record, job, domain.JobFailed, domain.TitleUnsupported, domain.TitleReasonCapabilityAbsent, domain.Fail(domain.Unsupported, "The original Worker no longer proves the pinned title profile.", "Keep the title unsupported; no fallback harness is allowed."))
			return updated, retireErr
		}
		if session.ProjectID != "" {
			project, err := tx.ExecutionProjectPolicy(session)
			if err != nil || !project.Agents.Allows(input.AgentID) || !project.Accounts.Allows(input.AccountID) {
				updated, retireErr := retireQueuedTitle(tx, record, job, domain.JobCanceled, domain.TitleSkipped, domain.TitleReasonAuthorityLost, domain.Fail(domain.PermissionDenied, "Current project policy does not authorize title inference.", "Keep the placeholder and inspect the current project policy."))
				return updated, retireErr
			}
		}
		_, account, err := accountFromTx(tx, input.AccountID, 0)
		if err != nil || !account.Enabled || account.Type != domain.APIAccount || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.ProviderID != input.ProviderID {
			updated, retireErr := retireQueuedTitle(tx, record, job, domain.JobCanceled, domain.TitleSkipped, domain.TitleReasonAuthorityLost, domain.Fail(domain.Unauthenticated, "The original account connection no longer authorizes title inference.", "Keep the placeholder; do not substitute another account."))
			return updated, retireErr
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, instance, device
		updated, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job)
		if err != nil {
			return nil, err
		}
		session.TitleState = domain.TitleRunning
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return updated, nil
	})
	if err != nil {
		return store.Record{}, err
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return store.Record{}, err
	}
	return record, nil
}

func (s *Service) WatchAuxiliaryWork(ctx context.Context, req *connect.Request[pb.WatchAuxiliaryWorkRequest], stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	actor, _ := domain.PrincipalFrom(ctx)
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := currentInstance(tx, machine, instance); err != nil {
			return err
		}
		_, value, err := activeMachine(tx, machine)
		if err != nil {
			return err
		}
		if !hasTitleCapability(value.WorkerCapabilities) {
			return domain.Fail(domain.Unsupported, "This Worker did not negotiate the automatic title profile.", "Use its primary work lane only.")
		}
		return nil
	}); err != nil {
		return rpc.Error(err, correlation)
	}
	if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
		return rpc.Error(err, correlation)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamID := domain.NewID()
	s.connectionsMu.Lock()
	if s.auxiliaryStreams == nil {
		s.auxiliaryStreams = map[domain.ID]workerStream{}
	}
	if previous, ok := s.auxiliaryStreams[machine]; ok {
		previous.Cancel()
	}
	s.auxiliaryStreams[machine] = workerStream{ID: streamID, Cancel: cancel, Instance: instance, Done: ctx.Done()}
	s.connectionsMu.Unlock()
	defer func() {
		s.connectionsMu.Lock()
		defer s.connectionsMu.Unlock()
		if s.auxiliaryStreams[machine].ID == streamID {
			delete(s.auxiliaryStreams, machine)
		}
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(domain.Fail(domain.Internal, "Streaming deadline controller is unavailable.", "Reconnect to the server."), correlation)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	send := func(message *pb.WatchAuxiliaryWorkResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	if err := send(&pb.WatchAuxiliaryWorkResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var after, inFlight, cancellationSent domain.ID
	for {
		changed := s.Store.Changed()
		var records []store.Record
		var cancelJob domain.ID
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			if inFlight != "" {
				record, err := tx.Get(domain.JobKind, inFlight)
				if err != nil {
					return err
				}
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return err
				}
				if job.State == domain.JobClaimed && job.InstanceID == instance && job.AssignedDeviceID == actor.DeviceID {
					requested, err := tx.JobCancellationRequested(inFlight)
					if err == nil && requested && cancellationSent != inFlight {
						cancelJob = inFlight
					}
					return err
				}
				inFlight, cancellationSent = "", ""
			}
			var err error
			if err := workerNetworkReady(tx, machine); err != nil {
				if domain.SafeError(err).Code == domain.RecoveryRequired {
					return nil
				}
				return err
			}
			records, err = tx.Jobs(machine, "", "", after, store.MaxPage)
			return err
		})
		if err != nil {
			return rpc.Error(err, correlation)
		}
		if cancelJob != "" {
			if err := send(&pb.WatchAuxiliaryWorkResponse{CancelJobId: string(cancelJob)}); err != nil {
				return err
			}
			cancellationSent = cancelJob
		}
		assigned := false
		waitForCapacity := false
		for _, record := range records {
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return rpc.Error(err, correlation)
			}
			if job.Type != domain.GenerateSessionTitleJob {
				after = record.ID
				continue
			}
			if job.State == domain.JobQueued {
				candidateID := record.ID
				record, err = claimTitleJob(ctx, s, machine, instance, actor.DeviceID, candidateID)
				if err != nil {
					if domain.SafeError(err).Code == domain.ResourceExhausted {
						waitForCapacity = true
						break
					}
					if domain.SafeError(err).Code == domain.Conflict {
						after = candidateID
						continue
					}
					return rpc.Error(err, correlation)
				}
				job, err = store.Decode[domain.Job](record)
				if err != nil {
					return rpc.Error(err, correlation)
				}
			}
			if job.State == domain.JobClaimed && job.InstanceID == instance && job.AssignedDeviceID == actor.DeviceID {
				cancelRequested, err := s.titleCancellationRequested(ctx, record.ID)
				if err != nil {
					return rpc.Error(err, correlation)
				}
				if err := send(&pb.WatchAuxiliaryWorkResponse{Job: rpc.Resource(record), CancelRequested: cancelRequested}); err != nil {
					return err
				}
				inFlight, cancellationSent, assigned = record.ID, "", true
				if cancelRequested {
					cancellationSent = record.ID
				}
				after = record.ID
				break
			}
			after = record.ID
		}
		if assigned {
			continue
		}
		if len(records) == store.MaxPage && !waitForCapacity {
			continue
		}
		if len(records) < store.MaxPage {
			after = ""
		}
		select {
		case <-ctx.Done():
			return rpc.Error(ctx.Err(), correlation)
		case <-changed:
		case <-ticker.C:
			if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchAuxiliaryWorkResponse{Heartbeat: true}); err != nil {
				return err
			}
		}
	}
}

func (s *Service) titleCancellationRequested(ctx context.Context, jobID domain.ID) (bool, error) {
	var requested bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		requested, err = tx.JobCancellationRequested(jobID)
		return err
	})
	return requested, err
}
