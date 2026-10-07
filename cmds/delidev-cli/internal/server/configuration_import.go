package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type configurationImportInspection struct {
	ID           domain.ID `json:"id"`
	RepositoryID domain.ID `json:"repository_id"`
	MachineID    domain.ID `json:"machine_id"`
	Path         string    `json:"path"`
}
type configurationImportJob struct {
	Actor       domain.Principal                `json:"actor"`
	Plan        domain.ConfigurationImportPlan  `json:"plan"`
	Inspections []configurationImportInspection `json:"inspections"`
}

func authorizeConfigurationImport(tx *store.Tx, actor domain.Principal) error {
	if !actor.ValidMetadata() {
		return domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential.")
	}
	return tx.Authorize()
}
func writeConfigurationImport(tx *store.Tx, plan domain.ConfigurationImportPlan) ([]domain.ConfigurationImportedResource, error) {
	if err := validateConfigurationPlan(tx, plan); err != nil {
		return nil, err
	}
	result := []domain.ConfigurationImportedResource{}
	for _, change := range plan.Changes {
		revision := change.ExpectedRevision
		if change.Action != domain.ConfigurationReuse {
			value, err := portableValue(change.Kind, change.After, true)
			if err != nil {
				return nil, err
			}
			if err = validateRelationships(tx, change.Kind, change.ID, change.ExpectedRevision, value); err != nil {
				return nil, err
			}
			record, err := tx.Put(change.Kind, change.ID, change.ExpectedRevision, "", "", value)
			if err != nil {
				return nil, err
			}
			revision = record.Revision
		}
		result = append(result, domain.ConfigurationImportedResource{SourceID: change.SourceID, ID: change.ID, Kind: change.Kind, Action: change.Action, Revision: revision})
	}
	return result, nil
}
func (s *Service) ApplyConfigurationImport(ctx context.Context, req *connect.Request[pb.ApplyConfigurationImportRequest]) (*connect.Response[pb.ApplyConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var preview domain.ConfigurationImportPreview
	if err = domain.Decode(req.Msg.PreviewJson, &preview); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input := struct {
		Actor   domain.Principal
		Preview domain.ConfigurationImportPreview
	}{actor, preview}
	// The exact durable receipt is checked before token expiry, so an uncertain
	// accepted request can always be inspected without creating another import.
	id := domain.ID(req.Msg.RequestId)
	result, found, err := s.Store.Replay(ctx, id, "configuration.import", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !found {
		if preview.Token == "" {
			return nil, rpc.Error(transferInvalid(), correlation)
		}
		if _, err = s.Identity.DecodeCursor(preview.Token, configurationPlanScope(actor, preview.Plan)); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		unlock, err := s.lockAccounts(ctx)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		defer unlock()
		result, err = s.Store.Mutate(ctx, id, "configuration.import", input, func(tx *store.Tx) (any, error) {
			if err := authorizeConfigurationImport(tx, actor); err != nil {
				return nil, err
			}
			if err := validateConfigurationPlan(tx, preview.Plan); err != nil {
				return nil, err
			}
			parentID := domain.NewID()
			pending := configurationImportJob{Actor: actor, Plan: preview.Plan, Inspections: []configurationImportInspection{}}
			children := []struct {
				id  domain.ID
				job domain.Job
			}{}
			now := time.Now().UTC()
			for _, change := range preview.Plan.Changes {
				if change.Kind != domain.RepositoryKind || change.Action == domain.ConfigurationReuse {
					continue
				}
				value, err := portableValue(change.Kind, change.After, true)
				if err != nil {
					return nil, err
				}
				repository := value.(*domain.Repository)
				required := []string{}
				for _, ref := range []domain.Reference{repository.Base, repository.Starting} {
					if ref.Type == domain.RemoteBranch {
						required = append(required, ref.Remote)
					}
				}
				for _, checkout := range repository.Checkouts {
					_, machine, err := activeMachine(tx, checkout.MachineID)
					if err != nil {
						return nil, err
					}
					childID := domain.NewID()
					pending.Inspections = append(pending.Inspections, configurationImportInspection{ID: childID, RepositoryID: change.ID, MachineID: checkout.MachineID, Path: checkout.Path})
					identity := ""
					// Preserve the legacy inspection input for older Workers. The
					// source identity is advisory only when the Worker negotiated
					// support for the post-capability field.
					if repository.RemoteURL != "" && slices.Contains(machine.WorkerCapabilities, domain.RepositoryInspectionMetadataV1) {
						identity, err = domain.RepositoryCloneSourceIdentity(repository.RemoteURL)
						if err != nil {
							return nil, err
						}
					}
					raw, err := json.Marshal(domain.RepositoryInspectionInput{Path: checkout.Path, PreferredRemote: repository.PreferredRemote, RequiredRemotes: required, ExpectedRemoteIdentity: identity})
					if err != nil {
						return nil, err
					}
					children = append(children, struct {
						id  domain.ID
						job domain.Job
					}{childID, domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: checkout.MachineID, ParentID: parentID, Input: raw, AcceptedAt: now}})
				}
			}
			raw, err := json.Marshal(pending)
			if err != nil {
				return nil, err
			}
			parent := domain.Job{Type: domain.ImportConfigurationJob, State: domain.JobQueued, Input: raw, AcceptedAt: now}
			if len(children) == 0 {
				resources, err := writeConfigurationImport(tx, preview.Plan)
				if err != nil {
					return nil, err
				}
				parent.Output, err = json.Marshal(resources)
				if err != nil {
					return nil, err
				}
				parent.State, parent.FinishedAt = domain.JobSucceeded, &now
				parent.Input = []byte(`{"version":1}`)
			}
			if _, err = tx.PutJob(parentID, 0, "", "", parent); err != nil {
				return nil, err
			}
			for _, child := range children {
				if _, err = tx.PutJob(child.id, 0, "", "", child.job); err != nil {
					return nil, err
				}
			}
			// A reference-only receipt cannot recreate deleted imported configuration
			// or disclose old instruction contents on a later replay.
			return struct {
				JobID domain.ID `json:"job_id"`
			}{parentID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	var reference struct {
		JobID   domain.ID `json:"job_id"`
		Deleted bool      `json:"deleted,omitempty"`
		ID      domain.ID `json:"id,omitempty"`
	}
	if err = domain.Decode(result.Data, &reference); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if reference.Deleted {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The import was accepted but affected configuration was subsequently deleted.", "The original request cannot recreate it. Inspect the retained import job or generate a new preview for new work."), correlation)
	}
	var report domain.ConfigurationImportResult
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		record, err := tx.Get(domain.JobKind, reference.JobID)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			return err
		}
		if job.Type != domain.ImportConfigurationJob {
			return transferInvalid()
		}
		report = domain.ConfigurationImportResult{JobID: record.ID, State: job.State, Resources: []domain.ConfigurationImportedResource{}, Problem: job.Problem}
		if len(job.Output) > 0 {
			return domain.Decode(job.Output, &report.Resources)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "configuration import observed", "request_id", id, "job_id", report.JobID, "state", report.State, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ApplyConfigurationImportResponse{RequestId: string(id), Replayed: result.Replayed, ResultJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Called by each existing inspection completion/loss/revocation boundary.
// All repository observations must succeed before any configuration is written.
func finishConfigurationImport(tx *store.Tx, record store.Record, parent domain.Job) error {
	if parent.State != domain.JobQueued {
		return nil
	}
	var pending configurationImportJob
	if err := domain.Decode(parent.Input, &pending); err != nil {
		return err
	}
	var problem *domain.Error
	waiting := false
	for _, expected := range pending.Inspections {
		row, err := tx.Get(domain.JobKind, expected.ID)
		if err != nil {
			return err
		}
		child, err := store.Decode[domain.Job](row)
		if err != nil {
			return err
		}
		if child.Type != domain.InspectRepositoryJob || child.ParentID != record.ID ||
			domain.OwnershipBlocks(domain.OwnershipMachine, "", child.MachineID != expected.MachineID) {
			return transferInvalid()
		}
		switch child.State {
		case domain.JobSucceeded:
			var inspection workspace.Inspection
			if err := domain.Decode(child.Output, &inspection); err != nil {
				return err
			}
			if inspection.Root != expected.Path {
				problem = domain.Fail(domain.Conflict, "A checkout resolved to a different canonical path than the preview.", "Inspect the target checkout and explicitly use its canonical path in a new preview.")
			}
			if _, _, err := activeMachine(tx, expected.MachineID); err != nil {
				problem = domain.SafeError(err)
			}
		case domain.JobFailed, domain.JobCanceled, domain.JobUncertain:
			problem = domain.Fail(domain.Conflict, "A Worker did not verify every imported repository.", "Existing configuration is unchanged. Inspect the read-only validation jobs before generating another preview.")
		default:
			waiting = true
		}
	}
	if problem == nil && waiting {
		return nil
	}
	if problem == nil {
		if err := authorizeConfigurationImport(tx, pending.Actor); err != nil {
			problem = domain.SafeError(err)
		}
	}
	if problem == nil {
		if err := validateConfigurationPlan(tx, pending.Plan); err != nil {
			problem = domain.SafeError(err)
		}
	}
	if problem == nil {
		resources, err := writeConfigurationImport(tx, pending.Plan)
		// Unexpected storage failure rolls back the child report and all writes;
		// the Worker's original durable report remains retryable, never partial.
		if err != nil {
			return err
		}
		parent.Output, err = json.Marshal(resources)
		if err != nil {
			return err
		}
	}
	// Once terminal, the job needs only reference outcomes. Drop the staged
	// instruction/configuration copies; retries use the original receipt digest.
	parent.Input = []byte(`{"version":1}`)
	now := time.Now().UTC()
	parent.FinishedAt = &now
	if problem == nil {
		parent.State = domain.JobSucceeded
	} else {
		parent.State, parent.Problem = domain.JobFailed, problem
	}
	_, err := tx.PutJob(record.ID, record.Revision, "", "", parent)
	return err
}
