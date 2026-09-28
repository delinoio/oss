package store

import (
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) preparePRProblemSet(expected uint64, observed domain.RepositoryQueryResult) (Record, domain.PRProblemSet, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, domain.PRProblemSet{}, err
	}
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRProblemSet{}, err
	}
	target, err := domain.PRTargetFromObservation(observed)
	if err != nil {
		return Record{}, domain.PRProblemSet{}, err
	}
	row, value, err := t.FindPRProblemSet(target.Provider, target.RemoteRepositoryID, target.PullRequestID)
	if domain.SafeError(err).Code == domain.NotFound && expected == 0 {
		row.ID = domain.NewID()
		value = domain.PRProblemSet{Version: 1, Type: domain.PRProblemSetRecord, Target: target}
	} else if err != nil {
		return row, value, err
	} else if row.Revision != expected || value.Target.RepositoryNodeID != target.RepositoryNodeID || value.Target.PullRequestNodeID != target.PullRequestNodeID || value.Target.Number != target.Number {
		return row, value, prProblemConflict()
	}
	value.Target = target
	return row, value, nil
}

func (t *Tx) publishPRProblemSet(previous Record, value domain.PRProblemSet) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	row, err := t.Put(domain.ProblemKind, previous.ID, previous.Revision, "", "", value)
	if err != nil {
		return row, err
	}
	if previous.Revision == 0 {
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO pr_problem_sets(id,stable_key) VALUES(?,?)", row.ID, domain.PRProblemKey(value.Target.Provider, value.Target.RemoteRepositoryID, value.Target.PullRequestID))
	}
	return row, storageError(err)
}

func (t *Tx) prProblemCapacity(set domain.ID) error {
	var count int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM pr_problem_records WHERE set_id=?", set).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= domain.MaxRetainedPRProblems {
		return domain.Fail(domain.ResourceExhausted, "This PR has reached its retained problem-version limit.", "Preserve its history; no evidence was evicted or partially collected.")
	}
	return nil
}

func (t *Tx) findPRProblemVersion(set domain.ID, kind domain.PRProblemKind, node, version string) (Record, domain.PRProblem, bool, error) {
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_problem_records WHERE set_id=? AND kind=? AND native_node=? AND content_version=?", set, kind, node, version).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, domain.PRProblem{}, false, nil
	}
	if err != nil {
		return Record{}, domain.PRProblem{}, false, storageError(err)
	}
	row, value, err := t.GetPRProblem(id)
	return row, value, true, err
}

func (t *Tx) reconcilePRProblemMembership(previous []domain.ID, seen map[domain.ID]bool) error {
	for _, id := range previous {
		if seen[id] {
			continue
		}
		row, value, err := t.GetPRProblem(id)
		if err != nil {
			return err
		}
		value.Current = false
		if _, err = t.putPRProblem(id, row.Revision, value); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) ObservePRCI(expected uint64, observed domain.RepositoryQueryResult) (Record, int, error) {
	if observed.Query.Operation != domain.RepositoryCI {
		return Record{}, 0, prProblemConflict()
	}
	original, set, err := t.preparePRProblemSet(expected, observed)
	if err != nil {
		return Record{}, 0, err
	}
	proof, err := domain.NewPRCIObservation(original.ID, observed)
	if err != nil {
		return Record{}, 0, err
	}
	entries, err := proof.FailureContexts()
	if err != nil {
		return Record{}, 0, err
	}
	summary := proof.Summary()
	set.CI = &summary
	row, err := t.publishPRProblemSet(original, set)
	if err != nil {
		return Record{}, 0, err
	}
	previous, err := t.currentPRProblems(row.ID, domain.PRCIProblem, domain.MaxCIContexts)
	if err != nil {
		return Record{}, 0, err
	}
	seen := map[domain.ID]bool{}
	var proofRow Record
	created := 0
	for _, entry := range entries {
		version := entry.Version()
		record, value, found, err := t.findPRProblemVersion(row.ID, domain.PRCIProblem, entry.NodeID, version)
		if err != nil {
			return Record{}, 0, err
		}
		if !found {
			if err = t.prProblemCapacity(row.ID); err != nil {
				return Record{}, 0, err
			}
			// Store the complete original operands once, and only when a new failed
			// version actually needs them. Passing/unknown refreshes retain summaries.
			if proofRow.ID == "" {
				proofRow, err = t.putPRCIObservation(proof)
				if err != nil {
					return Record{}, 0, err
				}
			}
			value = domain.PRProblem{Version: 1, Type: domain.PRProblemEvidenceRecord, SetID: row.ID, Kind: domain.PRCIProblem, Target: set.Target, Observation: proof.Observation, ContentVersion: version, CI: &domain.PRCIProblemEvidence{ObservationID: proofRow.ID, Context: entry, Source: proof.CI.Result.Source, RulesDigest: proof.CI.Rules.Digest}, Current: true, State: domain.PRProblemUnhandled}
			record, err = t.putPRProblem(domain.NewID(), 0, value)
			if err != nil {
				return Record{}, 0, err
			}
			created++
		} else if !value.Current {
			value.Current = true
			record, err = t.putPRProblem(record.ID, record.Revision, value)
			if err != nil {
				return Record{}, 0, err
			}
		}
		seen[record.ID] = true
	}
	if err = t.reconcilePRProblemMembership(previous, seen); err != nil {
		return Record{}, 0, err
	}
	return row, created, nil
}

func (t *Tx) ObservePRConflict(expected uint64, observed domain.RepositoryQueryResult) (Record, int, error) {
	if observed.Query.Operation != domain.RepositoryDetail {
		return Record{}, 0, prProblemConflict()
	}
	original, set, err := t.preparePRProblemSet(expected, observed)
	if err != nil {
		return Record{}, 0, err
	}
	conflict, err := domain.ObservePRConflict(set.Conflict, observed)
	if err != nil {
		return Record{}, 0, err
	}
	set.Conflict = &conflict
	row, err := t.publishPRProblemSet(original, set)
	if err != nil {
		return Record{}, 0, err
	}
	previous, err := t.currentPRProblems(row.ID, domain.PRMergeConflictProblem, 1)
	if err != nil {
		return Record{}, 0, err
	}
	seen := map[domain.ID]bool{}
	created := 0
	if conflict.State == domain.PRConflictPresent {
		snapshot := conflict.Active
		version := snapshot.ContentVersion()
		node := "conflict:" + string(snapshot.TransitionID)
		record, value, found, err := t.findPRProblemVersion(row.ID, domain.PRMergeConflictProblem, node, version)
		if err != nil {
			return Record{}, 0, err
		}
		if !found {
			if err = t.prProblemCapacity(row.ID); err != nil {
				return Record{}, 0, err
			}
			value = domain.PRProblem{Version: 1, Type: domain.PRProblemEvidenceRecord, SetID: row.ID, Kind: domain.PRMergeConflictProblem, Target: set.Target, Observation: snapshot.Observation, ContentVersion: version, Conflict: snapshot, Current: true, State: domain.PRProblemUnhandled}
			record, err = t.putPRProblem(domain.NewID(), 0, value)
			if err != nil {
				return Record{}, 0, err
			}
			created++
		} else if !value.Current {
			value.Current = true
			record, err = t.putPRProblem(record.ID, record.Revision, value)
			if err != nil {
				return Record{}, 0, err
			}
		}
		seen[record.ID] = true
	}
	if err = t.reconcilePRProblemMembership(previous, seen); err != nil {
		return Record{}, 0, err
	}
	return row, created, nil
}
