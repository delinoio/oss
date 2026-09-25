package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type repositorySaveInput struct {
	ID               domain.ID         `json:"id"`
	ExpectedRevision uint64            `json:"expected_revision"`
	Repository       domain.Repository `json:"repository"`
}
type repositorySaveOutput struct {
	ID       domain.ID `json:"id"`
	Revision uint64    `json:"revision"`
}

func repositoryRevision(tx *store.Tx, id domain.ID, expected uint64) error {
	record, err := tx.Get(domain.RepositoryKind, id)
	if expected == 0 && domain.SafeError(err).Code == domain.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if expected == 0 || record.Revision != expected {
		return domain.Fail(domain.Conflict, "The repository revision changed.", "Read its current revision and submit a new save request.")
	}
	return nil
}
func saveRepository(ctx context.Context, s *store.Store, input ConfigurationMutation, repository domain.Repository) (store.Result, error) {
	return s.Mutate(ctx, input.RequestID, "repository.save", input, func(tx *store.Tx) (any, error) {
		id := input.ID
		if id == "" {
			id = domain.NewID()
		}
		if err := repositoryRevision(tx, id, input.ExpectedRevision); err != nil {
			return nil, err
		}
		if err := validateRelationships(tx, domain.RepositoryKind, id, input.ExpectedRevision, &repository); err != nil {
			return nil, err
		}
		for _, checkout := range repository.Checkouts {
			if _, _, err := activeMachine(tx, checkout.MachineID); err != nil {
				return nil, err
			}
		}
		document, err := json.Marshal(repositorySaveInput{ID: id, ExpectedRevision: input.ExpectedRevision, Repository: repository})
		if err != nil {
			return nil, err
		}
		parent, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.SaveRepositoryJob, State: domain.JobQueued, Input: document, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		required := []string{}
		for _, ref := range []domain.Reference{repository.Base, repository.Starting} {
			if ref.Type == domain.RemoteBranch {
				required = append(required, ref.Remote)
			}
		}
		for _, checkout := range repository.Checkouts {
			raw, err := json.Marshal(domain.RepositoryInspectionInput{Path: checkout.Path, PreferredRemote: repository.PreferredRemote, RequiredRemotes: required})
			if err != nil {
				return nil, err
			}
			_, err = tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: checkout.MachineID, ParentID: parent.ID, Input: raw, AcceptedAt: time.Now().UTC()})
			if err != nil {
				return nil, err
			}
		}
		return parent, nil
	})
}

// finishRepositorySave runs in the same transaction as a child result. The
// repository and parent outcome become visible together only after every
// selected Worker has revalidated its own checkout. No Git runs in this txn.
func finishRepositorySave(tx *store.Tx, parentID domain.ID) error {
	if parentID == "" {
		return nil
	}
	record, err := tx.Get(domain.JobKind, parentID)
	if err != nil {
		return err
	}
	parent, err := store.Decode[domain.Job](record)
	if err != nil {
		return err
	}
	if parent.Type != domain.SaveRepositoryJob || parent.State != domain.JobQueued {
		return nil
	}
	var input repositorySaveInput
	if err := domain.Decode(parent.Input, &input); err != nil {
		return err
	}
	inspections := map[domain.ID]workspace.Inspection{}
	var after domain.ID
	pending := false
	var problem *domain.Error
	for {
		children, err := tx.Jobs("", parentID, "", after, store.MaxPage)
		if err != nil {
			return err
		}
		for _, childRecord := range children {
			child, err := store.Decode[domain.Job](childRecord)
			if err != nil {
				return err
			}
			switch child.State {
			case domain.JobSucceeded:
				var result workspace.Inspection
				if err := domain.Decode(child.Output, &result); err != nil {
					return err
				}
				inspections[child.MachineID] = result
			case domain.JobFailed, domain.JobCanceled, domain.JobUncertain:
				problem = child.Problem
				if problem == nil {
					problem = domain.Fail(domain.Unavailable, "A Worker did not complete repository validation.", "Inspect the accepted operation before retrying.")
				}
			default:
				pending = true
			}
			after = childRecord.ID
		}
		if len(children) < store.MaxPage {
			break
		}
	}
	if problem == nil && pending {
		return nil
	}
	if problem == nil && len(inspections) != len(input.Repository.Checkouts) {
		return domain.Fail(domain.RecoveryRequired, "Repository validation records are incomplete.", "Preserve the accepted operation and inspect server state.")
	}
	if problem == nil {
		for i, checkout := range input.Repository.Checkouts {
			input.Repository.Checkouts[i].Path = inspections[checkout.MachineID].Root
		}
		err := input.Repository.Validate()
		if err == nil {
			err = repositoryRevision(tx, input.ID, input.ExpectedRevision)
		}
		if err == nil {
			err = validateRelationships(tx, domain.RepositoryKind, input.ID, input.ExpectedRevision, &input.Repository)
		}
		if err == nil {
			for _, checkout := range input.Repository.Checkouts {
				if _, _, e := activeMachine(tx, checkout.MachineID); e != nil {
					err = e
					break
				}
			}
		}
		if err != nil {
			problem = domain.SafeError(err)
		} else {
			saved, e := tx.Put(domain.RepositoryKind, input.ID, input.ExpectedRevision, "", "", input.Repository)
			if e != nil {
				return e
			}
			parent.Output, err = json.Marshal(repositorySaveOutput{ID: saved.ID, Revision: saved.Revision})
			if err != nil {
				return err
			}
		}
	}
	now := time.Now().UTC()
	parent.FinishedAt = &now
	if problem == nil {
		parent.State = domain.JobSucceeded
	} else {
		parent.State = domain.JobFailed
		parent.Problem = problem
		if problem.Code == domain.RecoveryRequired {
			parent.State = domain.JobUncertain
		}
	}
	_, err = tx.PutJob(record.ID, record.Revision, "", "", parent)
	return err
}
