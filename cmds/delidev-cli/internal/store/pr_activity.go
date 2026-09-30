package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
)

// RetainPRHandlingVerification is the private publication boundary for an
// independent verifier. Manual fixes call it only after original assignment,
// native completion, cleanup and push verification. An attempt outcome, public
// write request or provider thread state cannot supply this proof.
func (t *Tx) RetainPRHandlingVerification(set domain.ID, problems []domain.PRRemediationProblemRef, proofDigest string) (Record, error) {
	actor, err := t.prRemediationActor()
	if err != nil {
		return Record{}, err
	}
	problems = slices.Clone(problems)
	slices.SortFunc(problems, func(a, b domain.PRRemediationProblemRef) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	v := domain.PRHandlingVerification{Version: 1, Type: domain.PRHandlingVerificationRecord, SetID: set, Problems: problems, ProofDigest: proofDigest, Actor: actor}
	if err := v.Validate(); err != nil {
		return Record{}, err
	}
	if _, _, err := t.GetPRProblemSet(set); err != nil {
		return Record{}, err
	}
	for _, ref := range problems {
		_, p, err := t.GetPRProblem(ref.ID)
		if err != nil {
			return Record{}, err
		}
		if p.SetID != set || p.ContentVersion != ref.ContentVersion {
			return Record{}, prProblemConflict()
		}
	}
	rows, more, err := t.sessionPage(1, "SELECT "+recordColumns+" FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-handling-verification' AND json_extract(body,'$.set_id')=? AND json_extract(body,'$.proof_digest')=? LIMIT 2", set, proofDigest)
	if err != nil {
		return Record{}, err
	}
	if more {
		return Record{}, prProblemConflict()
	}
	if len(rows) != 0 {
		prior, err := Decode[domain.PRHandlingVerification](rows[0])
		if err != nil || prior.Validate() != nil || !slices.Equal(prior.Problems, problems) {
			return Record{}, prProblemConflict()
		}
		return rows[0], nil
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-handling-verification' AND json_extract(body,'$.set_id')=?`, set).Scan(&count); err != nil {
		return Record{}, storageError(err)
	}
	if count >= domain.MaxRetainedPRProblems {
		return Record{}, domain.Fail(domain.ResourceExhausted, "The original PR verification history is full.", "Preserve the original proofs; no handling evidence was evicted.")
	}
	return t.Put(domain.ProblemKind, domain.NewID(), 0, "", "", v)
}

func (t *Tx) recordPRActivity(source Record, set domain.ID, target domain.SessionPullRequest, action domain.PRActivityAction, problems []domain.PRRemediationProblemRef, attempt *domain.PRRemediationAttempt) error {
	actor, err := t.prRemediationActor()
	if err != nil {
		return err
	}
	v := domain.PRActivity{Version: 1, Type: domain.PRActivityRecord, Action: action, SourceID: source.ID, SourceRevision: source.Revision, SetID: set, RemoteRepositoryID: target.RemoteRepositoryID, PullRequestID: target.PullRequestID, Number: target.Number, Owner: target.Owner, Name: target.Name, Problems: problems, Actor: actor}
	var session, project domain.ID
	if attempt != nil {
		v.AttemptState, v.Mode, v.Outcome, v.ExecutionID = attempt.State, attempt.Mode, attempt.Outcome, attempt.ExecutionID
		session = attempt.SessionID
		if session != "" {
			r, err := t.Get(domain.SessionKind, session)
			if err != nil {
				return err
			}
			project = r.ProjectID
		}
	}
	if err = v.Validate(); err != nil {
		return err
	}
	// Fresh IDs identify retained transitions, not aliases or mutable sources.
	// Source writers call this only for original creation or semantic change;
	// ordinary receipts bypass the callback and cannot append duplicate activity.
	_, err = t.Put(domain.ProblemKind, domain.NewID(), 0, session, project, v)
	return err
}

// Remove all activity owned by a deleted session, including the reservation
// recorded before its original attempt was bound. Shared PR evidence itself is
// session-independent and remains retained for other associations.
func (t *Tx) deleteSessionPRActivity(session domain.ID) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id,revision FROM entities WHERE kind='problem' AND json_extract(body,'$.type') IN ('pull-request-activity','pull-request-handling-verification') AND (session_id=? OR json_extract(body,'$.source_id') IN (SELECT id FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-remediation-attempt' AND json_extract(body,'$.session_id')=?))`, session, session)
	if err != nil {
		return storageError(err)
	}
	type ref struct {
		id       domain.ID
		revision uint64
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err = rows.Scan(&r.id, &r.revision); err != nil {
			break
		}
		refs = append(refs, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	for _, r := range refs {
		if err := t.Delete(domain.ProblemKind, r.id, r.revision); err != nil {
			return err
		}
	}
	return nil
}
