package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionWorkspaceRequest(tx *store.Tx, id domain.ID, session domain.Session) (workspace.PrepareRequest, error) {
	input := workspace.PrepareRequest{SessionID: id, MachineID: session.MachineID, Type: session.Workspace, Repositories: []workspace.RepositorySpec{}}
	if session.Workspace == domain.Local {
		return input, domain.Fail(domain.Unsupported, "Local session origin verification is not integrated in this build.", "Use a Worktree session until the client's own Worker identity can be verified.")
	}
	if session.Workspace == domain.GeneralChat {
		return input, nil
	}
	r, err := tx.Get(domain.ProjectKind, session.ProjectID)
	if err != nil {
		return input, err
	}
	project, err := store.Decode[domain.Project](r)
	if err != nil {
		return input, err
	}
	if len(project.Repositories) == 0 || len(project.Repositories) > 100 {
		return input, domain.Fail(domain.InvalidArgument, "Invalid project repository count.", "Configure 1 through 100 repositories for workspace preparation.")
	}
	settings := domain.DefaultSettings()
	records, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 1})
	if err != nil {
		return input, err
	}
	if len(records) != 0 {
		settings, err = store.Decode[domain.Settings](records[0])
		if err != nil {
			return input, err
		}
	}
	input.PrimaryRepository = project.PrimaryRepository
	for _, repositoryID := range project.Repositories {
		r, err := tx.Get(domain.RepositoryKind, repositoryID)
		if err != nil {
			return input, err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return input, err
		}
		spec := workspace.RepositorySpec{ID: r.ID, PreferredRemote: repo.PreferredRemote, Base: repo.Base, Starting: repo.Starting, AutoFetch: settings.AutomaticFetch && repo.AutoFetch}
		for _, checkout := range repo.Checkouts {
			if checkout.MachineID == session.MachineID {
				spec.Checkout = checkout.Path
				break
			}
		}
		if spec.Checkout == "" {
			return input, domain.Fail(domain.MissingInput, "A project repository has no checkout on the selected Worker.", "Inspect and configure every project repository on that machine.")
		}
		for _, start := range session.Starting {
			if start.RepositoryID == r.ID {
				spec.Starting = start.Reference
			}
		}
		input.Repositories = append(input.Repositories, spec)
	}
	return input, nil
}

func queueSessionWorkspace(tx *store.Tx, id domain.ID, session *domain.Session, input workspace.PrepareRequest) error {
	if _, _, err := activeMachine(tx, session.MachineID); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	jobID := domain.NewID()
	if _, err := tx.PutJob(jobID, 0, id, session.ProjectID, domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
		return err
	}
	session.Preparation = &domain.SessionPreparation{JobID: jobID, State: domain.PreparationPending}
	session.Problem = domain.SessionExecutionUnavailable()
	return nil
}

// Publication shares the job-result transaction. A result from an obsolete
// attempt cannot overwrite the current workspace or complete newer cleanup.
func finishSessionWorkspace(tx *store.Tx, jobID domain.ID) error {
	r, err := tx.Get(domain.JobKind, jobID)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](r)
	if err == nil && job.Type == domain.RecoverWorkspaceJob {
		return finishWorkspaceRecovery(tx, r, job)
	}
	if err != nil || job.Type != domain.PrepareWorkspaceJob {
		return err
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return err
	}
	if session.Preparation == nil || session.Preparation.JobID != jobID {
		return nil
	}
	switch job.State {
	case domain.JobSucceeded:
		session.Preparation.State = domain.PreparationReady
		session.Problem = domain.SessionExecutionUnavailable()
	case domain.JobFailed:
		session.Preparation.State = domain.PreparationFailed
		session.Problem = job.Problem
	case domain.JobCanceled:
		session.Preparation.State = domain.PreparationCanceled
		session.Problem = job.Problem
	case domain.JobUncertain:
		session.Preparation.State = domain.PreparationUncertain
		session.Recovery = domain.NeedsRecovery
		session.Problem = job.Problem
	default:
		return nil
	}
	if session.Dispatch != domain.DispatchPaused {
		session.Dispatch = domain.DispatchBlocked
	}
	if session.Archive == domain.ArchivePending && job.State.Terminal() && session.Recovery == domain.NoRecovery {
		session.Archive = domain.Archived
	}
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}

// Return true only when preparation has no outstanding native ownership.
// Claimed envelopes remain immutable so cancellation survives journal replay.
func stopSessionWorkspace(tx *store.Tx, session *domain.Session) (bool, error) {
	if session.Preparation == nil {
		return true, nil
	}
	if session.Preparation.RecoveryJobID != "" {
		r, err := tx.Get(domain.JobKind, session.Preparation.RecoveryJobID)
		if err != nil {
			return false, err
		}
		recovery, err := store.Decode[domain.Job](r)
		if err != nil {
			return false, err
		}
		if recovery.State == domain.JobQueued {
			now := time.Now().UTC()
			recovery.State, recovery.FinishedAt = domain.JobCanceled, &now
			recovery.Problem = domain.Fail(domain.Canceled, "Workspace recovery was canceled before dispatch.", "The original preparation remains uncertain.")
			if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, recovery); err != nil {
				return false, err
			}
			session.Recovery = domain.NeedsRecovery
		} else if recovery.State == domain.JobClaimed {
			if err := tx.RequestJobCancellation(r.ID); err != nil {
				return false, err
			}
		}
	}
	r, err := tx.Get(domain.JobKind, session.Preparation.JobID)
	if err != nil {
		return false, err
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		return false, err
	}
	switch job.State {
	case domain.JobQueued:
		now := time.Now().UTC()
		job.State, job.FinishedAt = domain.JobCanceled, &now
		job.Problem = domain.Fail(domain.Canceled, "Workspace preparation was canceled before dispatch.", "Prepare explicitly when ready; retained input has not executed.")
		if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job); err != nil {
			return false, err
		}
		session.Preparation.State = domain.PreparationCanceled
		session.Problem = job.Problem
		return true, nil
	case domain.JobClaimed:
		if err := tx.RequestJobCancellation(r.ID); err != nil {
			return false, err
		}
		session.Preparation.State = domain.PreparationStopping
		return false, nil
	case domain.JobUncertain:
		session.Preparation.State = domain.PreparationUncertain
		session.Recovery = domain.NeedsRecovery
		session.Problem = job.Problem
		return false, nil
	default:
		return job.State.Terminal(), nil
	}
}

func (s *Service) PrepareSessionWorkspace(ctx context.Context, req *connect.Request[pb.PrepareSessionWorkspaceRequest]) (*connect.Response[pb.PrepareSessionWorkspaceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		ID       string
		Revision uint64
	}{meta.Id, meta.ExpectedRevision}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.workspace.prepare", identity, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload its current preparation state before retrying.")
		}
		if session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted {
			return nil, domain.Fail(domain.RecoveryRequired, "The session cannot start workspace preparation in its current state.", "Restore visibility and reconcile native ownership before retrying.")
		}
		var input workspace.PrepareRequest
		if session.Preparation != nil {
			jobRecord, err := tx.Get(domain.JobKind, session.Preparation.JobID)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](jobRecord)
			if err != nil {
				return nil, err
			}
			switch job.State {
			case domain.JobSucceeded, domain.JobQueued, domain.JobClaimed:
				// Observing an existing attempt never repeats its native operation.
				return sessionReceipt{SessionID: r.ID}, nil
			case domain.JobFailed, domain.JobCanceled:
				if err := domain.Decode(job.Input, &input); err != nil {
					return nil, err
				}
			default:
				return nil, domain.Fail(domain.RecoveryRequired, "Workspace ownership is uncertain.", "Reconcile the original Worker journal before another preparation.")
			}
		} else {
			input, err = sessionWorkspaceRequest(tx, r.ID, session)
			if err != nil {
				return nil, err
			}
		}
		if err := queueSessionWorkspace(tx, r.ID, &session, input); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("session_workspace_accepted", "correlation_id", correlation, "session_id", meta.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.PrepareSessionWorkspaceResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
