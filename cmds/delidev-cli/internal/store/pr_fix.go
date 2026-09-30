// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) BindPRFixTarget(id domain.ID, expected uint64, project domain.ID, target domain.PRGitTarget) (Record, error) {
	if _, err := t.prRemediationActor(); err != nil {
		return Record{}, err
	}
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || v.State != domain.PRRemediationReserved || v.GitTarget != nil || project.Validate() != nil || target.Validate() != nil {
		return r, prRemediationConflict()
	}
	_, set, err := t.GetPRProblemSet(v.SetID)
	if err != nil {
		return r, err
	}
	if !set.Target.SamePR(target.Target) || set.Target.RepositoryNodeID != target.Target.RepositoryNodeID || set.Target.PullRequestNodeID != target.Target.PullRequestNodeID {
		return r, prRemediationConflict()
	}
	v.GitTarget, v.ProjectID = &target, project
	return t.putPRRemediationAttempt(id, expected, v)
}
func (t *Tx) PRFixSelection(id domain.ID) (domain.PRFixExecution, error) {
	_, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return domain.PRFixExecution{}, err
	}
	if v.GitTarget == nil {
		return domain.PRFixExecution{}, prRemediationConflict()
	}
	selection := domain.PRFixExecution{AttemptID: id, Target: *v.GitTarget, Strategy: v.Policy.ConflictStrategy}
	for _, ref := range v.Problems {
		_, problem, err := t.GetPRProblem(ref.ID)
		if err != nil {
			return selection, err
		}
		selection.Conflict = selection.Conflict || problem.Kind == domain.PRMergeConflictProblem
	}
	return selection, selection.Validate()
}

// Only the immutable assigned job, original native completion and independent
// Worker Git proof may handle exact retained versions. Dismissal wins a race.
func (t *Tx) finishPRFixPush(id domain.ID, v domain.PRRemediationAttempt, input domain.ExecutionJobInput, done domain.ExecutionCompletion) (bool, error) {
	if v.GitTarget == nil {
		return true, nil
	}
	selection, err := t.PRFixSelection(id)
	if err != nil {
		return false, err
	}
	if input.Remediation == nil || input.Remediation.Digest() != selection.Digest() || done.PRPush == nil || !done.PRPush.Matches(selection, v.ExecutionID) || done.PRPush.ObservedAt.Before(*v.StartedAt) || done.PRPush.ObservedAt.After(t.now) {
		return false, nil
	}
	if done.PRPush.State == domain.PRPushUncertain {
		return false, nil
	}
	if done.PRPush.State == domain.PRPushVerified && done.Outcome == domain.ExecutionSucceeded {
		for _, ref := range v.Problems {
			row, p, err := t.GetPRProblem(ref.ID)
			if err != nil {
				return false, err
			}
			if p.SetID != v.SetID || p.ContentVersion != ref.ContentVersion {
				return false, prRemediationConflict()
			}
			if p.State == domain.PRProblemUnhandled {
				p.State = domain.PRProblemHandled
				p.Handling = &domain.PRProblemHandling{AttemptID: id, ExecutionID: v.ExecutionID, PushedHead: done.PRPush.ResultHead, At: t.now}
				if _, err := t.putPRProblem(row.ID, row.Revision, p); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}
func (t *Tx) ActivePRFixAttempts(after domain.ID, limit int) ([]Record, bool, error) {
	if _, err := t.prProblemActor(); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 50 {
		return nil, false, prRemediationConflict()
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT a.id FROM pr_remediation_attempts a JOIN entities e ON e.id=a.id WHERE a.id>? AND a.state IN ('running','uncertain') AND json_extract(e.body,'$.git_target') IS NOT NULL ORDER BY a.id LIMIT ?", after, limit+1)
	if err != nil {
		return nil, false, storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, storageError(err)
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	out := []Record{}
	for _, id := range ids {
		row, v, err := t.GetPRRemediationAttempt(id)
		if err != nil {
			return nil, false, err
		}
		if v.GitTarget != nil {
			out = append(out, row)
		}
	}
	return out, more, nil
}
