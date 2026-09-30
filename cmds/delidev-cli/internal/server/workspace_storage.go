// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type storageReceipt struct {
	JobID domain.ID `json:"job_id"`
}

func storageAction(a pb.WorkspaceStorageAction) (workspace.StorageAction, error) {
	switch a {
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW:
		return workspace.StoragePreview, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE:
		return workspace.StorageCreate, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP:
		return workspace.StorageCleanup, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_INSPECT:
		return workspace.StorageInspect, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RESTORE:
		return workspace.StorageRestore, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER:
		return workspace.StorageRecover, nil
	case pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_DELETE:
		return workspace.StorageDelete, nil
	}
	return "", domain.Fail(domain.InvalidArgument, "Unknown workspace storage operation.", "Select preview, create, cleanup, inspect, restore, delete or recover.")
}
func storageIdle(tx *store.Tx, id domain.ID, s domain.Session, ignored domain.ID) error {
	if s.Workspace == domain.Local {
		return domain.Fail(domain.PermissionDenied, "Original Local checkouts cannot be cleaned.", "Use an independently owned backup workflow.")
	}
	if s.ActiveExecutionID != "" || s.Recovery != domain.NoRecovery || s.Archive == domain.ArchivePending || s.Preparation == nil || s.Preparation.State != domain.PreparationReady || (s.Execution != nil && !s.Execution.CleanupVerified) || s.TitleState == domain.TitleRunning || s.TitleState == domain.TitleUncertain || s.TitleState == domain.TitleQueued {
		return domain.Fail(domain.Conflict, "Workspace storage requires inactive, independently stopped work.", "Stop and reconcile every execution and dependent resource before cleanup.")
	}
	if s.Storage != nil && (s.Storage.State == domain.WorkspaceStoragePending || s.Storage.State == domain.WorkspaceStorageUncertain) && ignored != s.Storage.JobID {
		return domain.Fail(domain.RecoveryRequired, "A storage operation retains ownership.", "Inspect the original accepted operation and its Worker recovery evidence.")
	}
	// Forward sockets have independent owners and cleanup reports, rather than
	// job records. Stop acceptance alone cannot release parent storage ownership.
	pending, err := tx.SessionForwardsPending(id)
	if err != nil {
		return err
	}
	if pending {
		return domain.Fail(domain.Conflict, "Session forwards have not confirmed cleanup.", "Stop every forward and wait for both original peers before workspace storage.")
	}
	var after domain.ID
	for inspected := 0; inspected < 4096; {
		jobs, err := tx.List(store.Filter{Kind: domain.JobKind, SessionID: id, After: after, Limit: store.MaxPage})
		if err != nil {
			return err
		}
		for _, record := range jobs {
			inspected++
			after = record.ID
			if record.ID == ignored {
				continue
			}
			j, err := store.Decode[domain.Job](record)
			if err != nil {
				return err
			}
			if j.State == domain.JobQueued || j.State == domain.JobClaimed || j.State == domain.JobUncertain {
				// Explicit recovery carries an immutable bounded lineage. Its
				// original ancestors are inspected separately at acceptance.
				if ignored != "" {
					current, err := tx.Get(domain.JobKind, ignored)
					if err != nil {
						return err
					}
					body, err := store.Decode[domain.Job](current)
					if err != nil {
						return err
					}
					var operation workspace.StorageRequest
					if domain.Decode(body.Input, &operation) != nil {
						return workspace.ResultUncertain()
					}
					owned := false
					if operation.Recovery != nil {
						for _, claim := range operation.Recovery.Claims {
							if claim.JobID == record.ID {
								owned = true
							}
						}
					}
					if owned {
						continue
					}
				}
				return domain.Fail(domain.Conflict, "Dependent jobs have not confirmed cleanup.", "Settle every dependent job before parent storage operations.")
			}
		}
		if len(jobs) < store.MaxPage {
			return nil
		}
	}
	return domain.Fail(domain.ResourceExhausted, "Session ownership inventory exceeds its bound.", "Inspect retained work before storage cleanup.")
}
func (s *Service) RequestWorkspaceStorage(ctx context.Context, req *connect.Request[pb.RequestWorkspaceStorageRequest]) (*connect.Response[pb.RequestWorkspaceStorageResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	action, err := storageAction(req.Msg.Action)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Actor                       domain.Principal
		ID                          string
		Revision                    uint64
		Action                      workspace.StorageAction
		Snapshot, Preview, Recovery string
	}{actor, meta.Id, meta.ExpectedRevision, action, req.Msg.SnapshotId, req.Msg.PreviewJobId, req.Msg.RecoveryJobId}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "workspace.storage", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if sr.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload the session before accepting storage work.")
		}
		if err := storageIdle(tx, sr.ID, session, domain.ID(req.Msg.RecoveryJobId)); err != nil {
			return nil, err
		}
		if _, _, err := activeMachine(tx, session.MachineID); err != nil {
			return nil, err
		}
		preparation, err := tx.Get(domain.JobKind, session.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](preparation)
		if err != nil || job.Type != domain.PrepareWorkspaceJob || job.State != domain.JobSucceeded {
			return nil, workspace.ResultUncertain()
		}
		input := workspace.StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: action}
		if domain.Decode(job.Input, &input.Preparation) != nil || domain.Decode(job.Output, &input.Manifest) != nil {
			return nil, workspace.ResultUncertain()
		}
		wasStored := session.Storage != nil && session.Storage.State == domain.WorkspaceStored
		if wasStored {
			input.PreviousState = domain.WorkspaceStored
			input.PreviousSnapshotID = session.Storage.SnapshotID
		}
		if action != workspace.StorageRecover && req.Msg.RecoveryJobId != "" {
			return nil, workspace.ResultUncertain()
		}
		switch action {
		case workspace.StorageRecover:
			if err := storageRecoveryInput(tx, sr.ID, session, domain.ID(req.Msg.RecoveryJobId), &input); err != nil {
				return nil, err
			}
		case workspace.StoragePreview, workspace.StorageCreate, workspace.StorageCleanup:
			if wasStored {
				return nil, domain.Fail(domain.Conflict, "The workspace is already stored.", "Restore its cleanup snapshot before creating another.")
			}
			if req.Msg.SnapshotId != "" {
				return nil, domain.Fail(domain.InvalidArgument, "Creation cannot select an existing snapshot.", "The original accepted request reserves its snapshot identity.")
			}
			if action != workspace.StoragePreview {
				input.SnapshotID = domain.NewID()
			}
			if action == workspace.StorageCleanup {
				pr, err := tx.Get(domain.JobKind, domain.ID(req.Msg.PreviewJobId))
				if err != nil {
					return nil, err
				}
				preview, err := store.Decode[domain.Job](pr)
				if err != nil || preview.Type != domain.WorkspaceStorageJob || preview.State != domain.JobSucceeded || pr.SessionID != sr.ID || preview.MachineID != session.MachineID {
					return nil, workspace.ResultUncertain()
				}
				var output workspace.StorageResult
				if domain.Decode(preview.Output, &output) != nil || output.Action != workspace.StoragePreview || !output.CleanupVerified {
					return nil, workspace.ResultUncertain()
				}
				input.PreviewDigest = output.PreviewDigest
			} else if req.Msg.PreviewJobId != "" {
				return nil, domain.Fail(domain.InvalidArgument, "Only cleanup selects a preview.", "Use the original cleanup preview job.")
			}
		case workspace.StorageInspect, workspace.StorageRestore, workspace.StorageDelete:
			snapshotRecord, err := tx.Get(domain.SnapshotKind, domain.ID(req.Msg.SnapshotId))
			if err != nil {
				return nil, err
			}
			snapshot, err := store.Decode[workspace.SnapshotMetadata](snapshotRecord)
			if err != nil || snapshot.Deleted || snapshot.SessionID != sr.ID || snapshot.MachineID != session.MachineID {
				return nil, workspace.ResultUncertain()
			}
			input.SnapshotMetadata = &snapshot
			input.SnapshotID = snapshot.ID
			input.SnapshotDigest = snapshot.SHA256
			if action == workspace.StorageRestore && (!wasStored || session.Storage.SnapshotID != snapshot.ID) {
				return nil, domain.Fail(domain.Conflict, "Only the current cleanup snapshot can restore this session.", "Preserve newer native history and restore its exact last cleanup snapshot.")
			}
			if action == workspace.StorageDelete && wasStored && session.Storage.SnapshotID == snapshot.ID {
				return nil, domain.Fail(domain.Conflict, "This snapshot is the workspace's only recoverable copy.", "Restore the workspace before permanently deleting its snapshot.")
			}
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		if _, err := tx.PutJob(input.OperationID, 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: domain.ID(req.Msg.RecoveryJobId), Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		snapshotID := domain.ID("")
		if session.Storage != nil {
			snapshotID = session.Storage.SnapshotID
		}
		session.Storage = &domain.WorkspaceStorage{State: domain.WorkspaceStoragePending, JobID: input.OperationID, SnapshotID: snapshotID}
		session.Dispatch = domain.DispatchPaused
		session.NextExecutionIntent = ""
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return storageReceipt{JobID: input.OperationID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt storageReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, err := s.storageOperation(ctx, receipt.JobID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("workspace_storage_accepted", "session_id", meta.Id, "job_id", receipt.JobID, "action", action, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RequestWorkspaceStorageResponse{Job: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) storageOperation(ctx context.Context, id domain.ID) (store.Record, error) {
	var record store.Record
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		record, err = tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		j, err := store.Decode[domain.Job](record)
		if err != nil {
			return err
		}
		if j.Type != domain.WorkspaceStorageJob {
			return domain.Fail(domain.InvalidArgument, "This job is not a storage operation.", "Select the original workspace storage job.")
		}
		return nil
	})
	return record, err
}
func (s *Service) GetWorkspaceStorageOperation(ctx context.Context, req *connect.Request[pb.GetWorkspaceStorageOperationRequest]) (*connect.Response[pb.GetWorkspaceStorageOperationResponse], error) {
	record, err := s.storageOperation(ctx, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetWorkspaceStorageOperationResponse{Job: rpc.Resource(record)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func finishWorkspaceStorage(tx *store.Tx, r store.Record, job domain.Job) error {
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return err
	}
	if session.Storage == nil || session.Storage.JobID != r.ID {
		return workspace.ResultUncertain()
	}
	var input workspace.StorageRequest
	if domain.Decode(job.Input, &input) != nil {
		return workspace.ResultUncertain()
	}
	// An accepted operation retains its original cleanup snapshot through failures.
	prior := input.PreviousState
	session.Storage.State = prior
	if job.State == domain.JobUncertain || (input.Action == workspace.StorageRecover && job.State != domain.JobSucceeded) {
		session.Storage.State = domain.WorkspaceStorageUncertain
	}
	if job.State == domain.JobSucceeded {
		var output workspace.StorageResult
		if domain.Decode(job.Output, &output) != nil {
			return workspace.ResultUncertain()
		}
		session.Storage.State = output.WorkspaceState
		if input.Action == workspace.StorageRecover {
			if err := finishStorageRecovery(tx, input, output); err != nil {
				return err
			}
		}
		if output.Snapshot != nil {
			expected := uint64(0)
			if input.Action == workspace.StorageInspect || input.Action == workspace.StorageRestore || input.Action == workspace.StorageDelete {
				record, err := tx.Get(domain.SnapshotKind, input.SnapshotID)
				if err != nil {
					return err
				}
				expected = record.Revision
			}
			if input.Action == workspace.StorageRecover {
				if record, err := tx.Get(domain.SnapshotKind, input.SnapshotID); err == nil {
					expected = record.Revision
				} else if domain.SafeError(err).Code != domain.NotFound {
					return err
				}
			}
			if _, err := tx.Put(domain.SnapshotKind, input.SnapshotID, expected, sr.ID, sr.ProjectID, output.Snapshot); err != nil {
				return err
			}
		}
		if input.Action == workspace.StorageCleanup {
			session.Storage.State = domain.WorkspaceStored
			session.Storage.SnapshotID = input.SnapshotID
		}
		if input.Action == workspace.StorageRestore {
			session.Storage.State = domain.WorkspacePresent
			session.Storage.SnapshotID = ""
		}
	}
	if session.Storage.State == domain.WorkspaceStored {
		if input.Action == workspace.StorageRecover {
			session.Storage.SnapshotID = input.PreviousSnapshotID
			if input.Recovery != nil && input.Recovery.Original.Action == workspace.StorageCleanup {
				session.Storage.SnapshotID = input.SnapshotID
			}
		}
	} else if session.Storage.State == domain.WorkspacePresent {
		session.Storage.SnapshotID = ""
	}
	session.Dispatch = domain.DispatchPaused
	session.NextExecutionIntent = ""
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}
func validateWorkspaceStorageResult(input workspace.StorageRequest, raw []byte) error {
	var output workspace.StorageResult
	if domain.Decode(raw, &output) != nil || output.Version != 1 || output.OperationID != input.OperationID || output.Action != input.Action || output.SessionID != input.Preparation.SessionID || output.MachineID != input.Preparation.MachineID || !output.CleanupVerified || output.SourceBytes > workspace.MaxSnapshotBytes || output.RemovedSourceBytes > output.SourceBytes {
		return workspace.ResultUncertain()
	}
	if input.Action == workspace.StorageRecover {
		if input.Recovery == nil || output.RecoveredJobID != input.Recovery.Original.OperationID || (output.RecoveredJobState != domain.JobSucceeded && output.RecoveredJobState != domain.JobFailed) || (output.WorkspaceState != domain.WorkspacePresent && output.WorkspaceState != domain.WorkspaceStored) {
			return workspace.ResultUncertain()
		}
		observed := output
		expected := input.Recovery.Original
		observed.Action = expected.Action
		observed.OperationID = expected.OperationID
		observed.RecoveredJobID = ""
		observed.RecoveredJobState = ""
		if output.RecoveredJobState == domain.JobFailed {
			// Failure preserves the original availability and zero removal. The
			// retained artifacts still require the same full metadata validation.
			switch expected.Action {
			case workspace.StoragePreview:
			case workspace.StorageCreate, workspace.StorageCleanup:
				expected.Action = workspace.StoragePreview
				if observed.Snapshot != nil {
					expected.Action = workspace.StorageCreate
				}
			case workspace.StorageInspect, workspace.StorageRestore, workspace.StorageDelete:
				expected.Action = workspace.StorageInspect
			default:
				return workspace.ResultUncertain()
			}
			observed.Action = expected.Action
		}
		encoded, err := json.Marshal(observed)
		if err != nil || validateWorkspaceStorageResult(expected, encoded) != nil {
			return workspace.ResultUncertain()
		}
		return nil
	}
	if output.WorkspaceState != domain.WorkspacePresent && output.WorkspaceState != domain.WorkspaceStored {
		return workspace.ResultUncertain()
	}
	expectedState := input.PreviousState
	if input.Action == workspace.StorageCleanup {
		expectedState = domain.WorkspaceStored
	}
	if input.Action == workspace.StorageRestore {
		expectedState = domain.WorkspacePresent
	}
	if output.WorkspaceState != expectedState || (input.Action != workspace.StorageCleanup && output.RemovedSourceBytes != 0) {
		return workspace.ResultUncertain()
	}
	if input.Action == workspace.StoragePreview {
		if len(output.PreviewDigest) != 64 || output.Snapshot != nil {
			return workspace.ResultUncertain()
		}
	} else {
		snapshot := output.Snapshot
		if snapshot == nil || snapshot.ID != input.SnapshotID || snapshot.SessionID != output.SessionID || snapshot.MachineID != output.MachineID || len(snapshot.SHA256) != 64 || snapshot.SizeBytes > workspace.MaxSnapshotBytes || snapshot.CreatedAt.IsZero() || snapshot.RepositoryCount != uint32(len(input.Manifest.Repositories)) || snapshot.Deleted != (input.Action == workspace.StorageDelete) {
			return workspace.ResultUncertain()
		}
		if input.SnapshotDigest != "" && snapshot.SHA256 != input.SnapshotDigest {
			return workspace.ResultUncertain()
		}
	}
	if input.Action == workspace.StorageCleanup && output.RemovedSourceBytes != output.SourceBytes {
		return workspace.ResultUncertain()
	}
	return nil
}

func storageRecoveryInput(tx *store.Tx, sessionID domain.ID, session domain.Session, id domain.ID, input *workspace.StorageRequest) error {
	if id.Validate() != nil || session.Storage == nil || session.Storage.JobID != id || session.Storage.State != domain.WorkspaceStorageUncertain {
		return workspace.ResultUncertain()
	}
	record, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil || job.Type != domain.WorkspaceStorageJob || job.State != domain.JobUncertain || record.SessionID != sessionID || job.MachineID != session.MachineID {
		return workspace.ResultUncertain()
	}
	assigned, err := tx.JobAssignment(id)
	if err != nil {
		return err
	}
	claim, err := store.Decode[domain.Job](assigned)
	if err != nil || claim.Type != domain.WorkspaceStorageJob || claim.State != domain.JobClaimed || claim.MachineID != job.MachineID || claim.InstanceID != job.InstanceID || assigned.SessionID != sessionID {
		return workspace.ResultUncertain()
	}
	var original workspace.StorageRequest
	if domain.Decode(claim.Input, &original) != nil || original.OperationID != id {
		return workspace.ResultUncertain()
	}
	a, _ := json.Marshal(original.Manifest)
	b, _ := json.Marshal(input.Manifest)
	if string(a) != string(b) {
		return workspace.ResultUncertain()
	}
	sum := sha256.Sum256(assigned.Data)
	claimRef := workspace.StorageJournalClaim{JobID: id, InstanceID: claim.InstanceID, Revision: assigned.Revision, AssignmentDigest: hex.EncodeToString(sum[:])}
	claims := []workspace.StorageJournalClaim{claimRef}
	if original.Action == workspace.StorageRecover {
		if original.Recovery == nil || len(original.Recovery.Claims) >= 8 {
			return workspace.ResultUncertain()
		}
		claims = append(claims, original.Recovery.Claims...)
		original = original.Recovery.Original
	}
	input.Recovery = &workspace.StorageRecovery{Claims: claims, Original: original, InstanceID: claim.InstanceID, Revision: assigned.Revision, AssignmentDigest: hex.EncodeToString(sum[:])}
	input.PreviousState = original.PreviousState
	input.PreviousSnapshotID = original.PreviousSnapshotID
	input.SnapshotID = original.SnapshotID
	return nil
}
func finishStorageRecovery(tx *store.Tx, input workspace.StorageRequest, output workspace.StorageResult) error {
	original, err := tx.Get(domain.JobKind, output.RecoveredJobID)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](original)
	if err != nil || input.Recovery == nil || original.ID != input.Recovery.Original.OperationID || job.Type != domain.WorkspaceStorageJob || job.State != domain.JobUncertain || original.SessionID != input.Preparation.SessionID {
		return workspace.ResultUncertain()
	}
	now := time.Now().UTC()
	job.State = output.RecoveredJobState
	job.FinishedAt = &now
	job.Output = nil
	job.Problem = domain.Fail(domain.Canceled, "The original storage operation was reconciled without repeating its side effects.", "Use its successful explicit recovery job to inspect the retained workspace/snapshot state.")
	if job.State == domain.JobSucceeded {
		originalOutput := output
		originalOutput.Action = input.Recovery.Original.Action
		originalOutput.OperationID = original.ID
		originalOutput.RecoveredJobID = ""
		originalOutput.RecoveredJobState = ""
		raw, err := json.Marshal(originalOutput)
		if err != nil {
			return err
		}
		job.Output = raw
		job.Problem = nil
	}
	_, err = tx.PutJob(original.ID, original.Revision, original.SessionID, original.ProjectID, job)
	if err != nil {
		return err
	}
	for _, claim := range input.Recovery.Claims {
		if claim.JobID == original.ID {
			continue
		}
		prior, err := tx.Get(domain.JobKind, claim.JobID)
		if err != nil {
			return err
		}
		j, err := store.Decode[domain.Job](prior)
		if err != nil || j.Type != domain.WorkspaceStorageJob || j.State != domain.JobUncertain || prior.SessionID != original.SessionID {
			return workspace.ResultUncertain()
		}
		j.State = domain.JobSucceeded
		j.Problem = nil
		j.FinishedAt = &now
		comparison := output
		comparison.OperationID = prior.ID
		j.Output, err = json.Marshal(comparison)
		if err != nil {
			return err
		}
		if _, err := tx.PutJob(prior.ID, prior.Revision, prior.SessionID, prior.ProjectID, j); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) CancelWorkspaceStorageOperation(ctx context.Context, req *connect.Request[pb.CancelWorkspaceStorageOperationRequest]) (*connect.Response[pb.CancelWorkspaceStorageOperationResponse], error) {
	meta := req.Msg.Mutation
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Actor    domain.Principal
		ID       string
		Revision uint64
	}{actor, meta.Id, meta.ExpectedRevision}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "workspace.storage.cancel", identity, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.JobKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil || job.Type != domain.WorkspaceStorageJob {
			return nil, workspace.ResultUncertain()
		}
		if record.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The original storage job revision changed.", "Read the current operation before cancellation.")
		}
		switch job.State {
		case domain.JobQueued:
			now := time.Now().UTC()
			job.State = domain.JobCanceled
			job.FinishedAt = &now
			job.Problem = domain.Fail(domain.Canceled, "Storage work was canceled before native dispatch.", "No native side effects were attempted.")
			if _, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job); err != nil {
				return nil, err
			}
			if err := finishWorkspaceStorage(tx, record, job); err != nil {
				return nil, err
			}
		case domain.JobClaimed:
			if err := tx.RequestJobCancellation(record.ID); err != nil {
				return nil, err
			}
		}
		return storageReceipt{JobID: record.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt storageReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	record, err := s.storageOperation(ctx, receipt.JobID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.CancelWorkspaceStorageOperationResponse{Job: rpc.Resource(record), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
