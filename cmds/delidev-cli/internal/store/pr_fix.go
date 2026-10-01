// SPDX-License-Identifier: Apache-2.0
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Preserve the original discovery association independently of the selected
// execution session. Stop/Archive/unlink on that source must still win when a
// dedicated workspace has been queued but no native execution was claimed.
func (t *Tx) BindAutomaticPRSource(id domain.ID, expected uint64, link Record) (Record, error) {
	r, v, err := t.GetPRRemediationAttempt(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || v.State != domain.PRRemediationReserved || v.AutomaticLinkID != "" || link.Kind != domain.PullRequestKind || link.Revision == 0 {
		return r, prRemediationConflict()
	}
	v.AutomaticLinkID, v.AutomaticLinkRevision = link.ID, link.Revision
	if err := t.RequireAutomaticPRSource(v); err != nil {
		return r, err
	}
	return t.putPRRemediationAttempt(id, expected, v)
}

func (t *Tx) RequireAutomaticPRSource(v domain.PRRemediationAttempt) error {
	if v.Mode != domain.PRRemediationAutomatic || v.GitTarget == nil || v.AutomaticLinkID == "" || v.AutomaticLinkRevision == 0 {
		return prRemediationConflict()
	}
	r, err := t.Get(domain.PullRequestKind, v.AutomaticLinkID)
	if err != nil {
		return err
	}
	link, err := Decode[domain.SessionPullRequest](r)
	if err != nil {
		return err
	}
	if r.Revision != v.AutomaticLinkRevision || r.ProjectID != v.ProjectID || link.Validate() != nil || !link.SamePR(v.GitTarget.Target) || link.RepositoryID != v.GitTarget.Target.RepositoryID || link.RepositoryNodeID != v.GitTarget.Target.RepositoryNodeID || link.PullRequestNodeID != v.GitTarget.Target.PullRequestNodeID {
		return prRemediationConflict()
	}
	sr, err := t.Get(domain.SessionKind, r.SessionID)
	if err != nil {
		return err
	}
	session, err := Decode[domain.Session](sr)
	if err != nil {
		return err
	}
	if sr.ProjectID != r.ProjectID || session.ProjectID != r.ProjectID {
		return prRemediationConflict()
	}
	return t.RequireAutomaticPRSourceSession(sr, session, link)
}

// A settled automatic failure can preserve discovery authority while a fresh
// session executes the next attempt. This never resumes its old paused queue:
// the exact retained failed attempt and positive cleanup must still match.
func (t *Tx) RequireAutomaticPRSourceSession(sr Record, session domain.Session, link domain.SessionPullRequest) error {
	if session.AutomaticRemediationStopped || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery {
		return prRemediationConflict()
	}
	if session.Dispatch != domain.DispatchPaused {
		return nil
	}
	if session.Outcome != domain.ExecutionFailed || session.Execution == nil || session.Execution.Outcome != domain.ExecutionFailed || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" {
		return prRemediationConflict()
	}
	_, attempt, found, err := t.PRRemediationForInput(session.Execution.InputID)
	if err != nil {
		return err
	}
	if !found || attempt.Mode != domain.PRRemediationAutomatic || attempt.AutomaticLinkID == "" || attempt.GitTarget == nil || !attempt.GitTarget.Target.SamePR(link) || attempt.ProjectID != sr.ProjectID || attempt.SessionID != sr.ID || attempt.ExecutionID != session.Execution.ExecutionID || attempt.State != domain.PRRemediationFinished || attempt.Outcome != domain.ExecutionFailed {
		return prRemediationConflict()
	}
	return nil
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
	// FinishPRRemediation already proves server-observed original assignment,
	// report ordering and native cleanup. Worker wall time is audit metadata,
	// not a cross-machine ordering gate; clock skew cannot strand a valid push.
	if input.Remediation == nil || input.Remediation.Digest() != selection.Digest() || done.PRPush == nil || !done.PRPush.Matches(selection, v.ExecutionID) {
		return false, nil
	}
	if done.PRPush.State == domain.PRPushUncertain {
		return false, nil
	}
	if done.PRPush.State == domain.PRPushVerified && done.Outcome == domain.ExecutionSucceeded {
		handled := []domain.PRRemediationProblemRef{}
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
				handled = append(handled, ref)
			}
		}
		if len(handled) != 0 {
			// Publish immutable Activity proof only after the original native,
			// cleanup and push checks, atomically with these exact handled versions.
			raw, err := json.Marshal(done.PRPush)
			if err != nil {
				return false, storageError(err)
			}
			digest := sha256.Sum256(raw)
			if _, err := t.RetainPRHandlingVerification(v.SetID, handled, hex.EncodeToString(digest[:])); err != nil {
				return false, err
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
