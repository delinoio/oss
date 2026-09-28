package store

import (
	"database/sql"
	"errors"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const prProblemSchema = `
CREATE TABLE pr_problem_sets (
 id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 stable_key TEXT PRIMARY KEY CHECK(length(stable_key)=64)
);
CREATE TABLE pr_problem_records (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 kind TEXT NOT NULL CHECK(kind='review-feedback'),
 native_node TEXT NOT NULL,
 content_version TEXT NOT NULL CHECK(length(content_version)=64),
 current INTEGER NOT NULL CHECK(current IN (0,1)),
 UNIQUE(set_id,kind,native_node,content_version)
);
CREATE INDEX pr_problem_current ON pr_problem_records(set_id,kind,current,id);
CREATE INDEX pr_problem_history ON pr_problem_records(set_id,id);
PRAGMA user_version=18;
`

func (t *Tx) prProblemActor() (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(t.ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "PR problem history requires an owner or paired client.", "Use an authorized product client.")
	}
	return actor, t.Authorize()
}

func prProblemConflict() error {
	return domain.Fail(domain.Conflict, "The PR problem inventory or content revision changed.", "Refresh the original PR and problem versions before retrying.")
}
func prProblemMissing() error {
	return domain.Fail(domain.NotFound, "No retained problem inventory exists for this PR.", "Collect the current PR's published feedback first.")
}

func (t *Tx) FindPRProblemSet(provider domain.IntegrationProvider, repository, pullRequest string) (Record, domain.PRProblemSet, error) {
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRProblemSet{}, err
	}
	key := domain.PRProblemKey(provider, repository, pullRequest)
	if key == "" {
		return Record{}, domain.PRProblemSet{}, domain.Fail(domain.InvalidArgument, "Invalid stable PR identity.", "Use the original provider, repository and PR numeric identities.")
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_problem_sets WHERE stable_key=?", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, domain.PRProblemSet{}, prProblemMissing()
	}
	if err != nil {
		return Record{}, domain.PRProblemSet{}, storageError(err)
	}
	r, v, err := t.GetPRProblemSet(id)
	if err == nil && domain.PRProblemKey(v.Target.Provider, v.Target.RemoteRepositoryID, v.Target.PullRequestID) != key {
		err = prProblemConflict()
	}
	return r, v, err
}

func (t *Tx) GetPRProblemSet(id domain.ID) (Record, domain.PRProblemSet, error) {
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRProblemSet{}, err
	}
	r, err := t.Get(domain.ProblemKind, id)
	if err != nil {
		return r, domain.PRProblemSet{}, err
	}
	v, err := Decode[domain.PRProblemSet](r)
	if err != nil {
		return r, v, err
	}
	if v.Validate() != nil || r.SessionID != "" || r.ProjectID != "" {
		return r, v, prProblemConflict()
	}
	var key string
	if err = t.tx.QueryRowContext(t.ctx, "SELECT stable_key FROM pr_problem_sets WHERE id=?", id).Scan(&key); err != nil {
		return r, v, storageError(err)
	}
	if key != domain.PRProblemKey(v.Target.Provider, v.Target.RemoteRepositoryID, v.Target.PullRequestID) {
		return r, v, prProblemConflict()
	}
	return r, v, nil
}

func (t *Tx) GetPRProblem(id domain.ID) (Record, domain.PRProblem, error) {
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRProblem{}, err
	}
	r, err := t.Get(domain.ProblemKind, id)
	if err != nil {
		return r, domain.PRProblem{}, err
	}
	v, err := Decode[domain.PRProblem](r)
	if err != nil {
		return r, v, err
	}
	if v.Validate() != nil || r.SessionID != "" || r.ProjectID != "" {
		return r, v, prProblemConflict()
	}
	var set domain.ID
	var kind domain.PRProblemKind
	var node, version string
	var current bool
	var proofID sql.NullString
	if err = t.tx.QueryRowContext(t.ctx, "SELECT set_id,kind,native_node,content_version,current,ci_observation_id FROM pr_problem_records WHERE id=?", id).Scan(&set, &kind, &node, &version, &current, &proofID); err != nil {
		return r, v, storageError(err)
	}
	if set != v.SetID || kind != v.Kind || node != v.NativeNode() || version != v.ContentVersion || current != v.Current {
		return r, v, prProblemConflict()
	}
	if v.Kind == domain.PRCIProblem {
		if !proofID.Valid || domain.ID(proofID.String) != v.CI.ObservationID {
			return r, v, prProblemConflict()
		}
		_, proof, err := t.GetPRCIObservation(v.CI.ObservationID)
		if err != nil {
			return r, v, err
		}
		if proof.SetID != v.SetID || !proof.Target.SamePR(v.Target) || !v.CI.Matches(proof) || proof.Observation.BaseSHA != v.Observation.BaseSHA || proof.Observation.HeadSHA != v.Observation.HeadSHA || !proof.Observation.ObservedAt.Equal(v.Observation.ObservedAt) {
			return r, v, prProblemConflict()
		}
	} else if proofID.Valid {
		return r, v, prProblemConflict()
	}
	_, parent, err := t.GetPRProblemSet(set)
	if err != nil {
		return r, v, err
	}
	if !parent.Target.SamePR(v.Target) || parent.Target.PullRequestNodeID != v.Target.PullRequestNodeID || parent.Target.RepositoryNodeID != v.Target.RepositoryNodeID {
		return r, v, prProblemConflict()
	}
	return r, v, nil
}

func (t *Tx) putPRProblem(id domain.ID, expected uint64, value domain.PRProblem) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	r, err := t.Put(domain.ProblemKind, id, expected, "", "", value)
	if err != nil {
		return r, err
	}
	if expected == 0 {
		var proofID any
		if value.CI != nil {
			proofID = value.CI.ObservationID
		}
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO pr_problem_records(id,set_id,kind,native_node,content_version,current,ci_observation_id) VALUES(?,?,?,?,?,?,?)", id, value.SetID, value.Kind, value.NativeNode(), value.ContentVersion, value.Current, proofID)
	} else {
		_, err = t.tx.ExecContext(t.ctx, "UPDATE pr_problem_records SET current=? WHERE id=?", value.Current, id)
	}
	return r, storageError(err)
}

// Collection publishes only a complete, independently validated inventory.
// The caller must recheck the repository/profile generation in this transaction
// after its outside-lock GitHub read. expectedSetRevision is captured before
// that read, so a delayed collector cannot replace a newer committed inventory.
func (t *Tx) ObservePRFeedback(expectedSetRevision uint64, observed domain.RepositoryQueryResult) (Record, int, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, 0, err
	}
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, 0, err
	}
	if observed.Validate() != nil || (observed.Query.Operation != domain.RepositoryFeedback && observed.Query.Operation != domain.RepositoryReviewers) {
		return Record{}, 0, prProblemConflict()
	}
	feedback := observed.Feedback
	if observed.Reviewers != nil {
		feedback = &observed.Reviewers.Feedback
	}
	if feedback == nil {
		return Record{}, 0, prProblemConflict()
	}
	item := observed.Items[0]
	target := domain.SessionPullRequest{Version: 1, Provider: observed.Repository.Provider, RepositoryID: observed.RepositoryID, RemoteRepositoryID: observed.Repository.ID, RepositoryNodeID: observed.Repository.NodeID, Owner: observed.Repository.Owner, Name: observed.Repository.Name, PullRequestID: item.ID, PullRequestNodeID: item.NodeID, Number: item.Number, Title: item.Title, ObservedAt: observed.ObservedAt}
	setRecord, set, err := t.FindPRProblemSet(target.Provider, target.RemoteRepositoryID, target.PullRequestID)
	if domain.SafeError(err).Code == domain.NotFound && expectedSetRevision == 0 {
		setRecord.ID = domain.NewID()
		set = domain.PRProblemSet{Version: 1, Type: domain.PRProblemSetRecord, Target: target}
	} else if err != nil {
		return Record{}, 0, err
	} else if setRecord.Revision != expectedSetRevision || set.Target.RepositoryNodeID != target.RepositoryNodeID || set.Target.PullRequestNodeID != target.PullRequestNodeID || set.Target.Number != target.Number {
		return Record{}, 0, prProblemConflict()
	}
	set.Target = target
	set.Feedback = &domain.PRProblemObservation{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, ObservedAt: observed.ObservedAt}
	if err = set.Validate(); err != nil {
		return Record{}, 0, err
	}
	setRecord, err = t.Put(domain.ProblemKind, setRecord.ID, expectedSetRevision, "", "", set)
	if err != nil {
		return Record{}, 0, err
	}
	if expectedSetRevision == 0 {
		if _, err = t.tx.ExecContext(t.ctx, "INSERT INTO pr_problem_sets(id,stable_key) VALUES(?,?)", setRecord.ID, domain.PRProblemKey(target.Provider, target.RemoteRepositoryID, target.PullRequestID)); err != nil {
			return Record{}, 0, storageError(err)
		}
	}
	var count int
	if err = t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM pr_problem_records WHERE set_id=?", setRecord.ID).Scan(&count); err != nil {
		return Record{}, 0, storageError(err)
	}
	previous, err := t.currentPRProblems(setRecord.ID, domain.PRFeedbackProblem, domain.MaxPRFeedback)
	if err != nil {
		return Record{}, 0, err
	}
	seen := map[domain.ID]bool{}
	created := 0
	for _, entry := range feedback.Entries {
		latest, err := domain.FeedbackProviderState(entry, feedback.Threads)
		if err != nil {
			return Record{}, 0, err
		}
		var id domain.ID
		err = t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_problem_records WHERE set_id=? AND kind=? AND native_node=? AND content_version=?", setRecord.ID, domain.PRFeedbackProblem, entry.NodeID, entry.ContentVersion).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			// A content edit cannot change a provider node's original kind or
			// numeric identity. Check retained history, including absent versions.
			var priorID domain.ID
			priorErr := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_problem_records WHERE set_id=? AND kind=? AND native_node=? LIMIT 1", setRecord.ID, domain.PRFeedbackProblem, entry.NodeID).Scan(&priorID)
			if priorErr == nil {
				_, prior, e := t.GetPRProblem(priorID)
				if e != nil {
					return Record{}, 0, e
				}
				if prior.Feedback.Kind != entry.Kind || prior.Feedback.ID != entry.ID {
					return Record{}, 0, prProblemConflict()
				}
			} else if !errors.Is(priorErr, sql.ErrNoRows) {
				return Record{}, 0, storageError(priorErr)
			}
			if count+created >= domain.MaxRetainedPRProblems {
				return Record{}, 0, domain.Fail(domain.ResourceExhausted, "This PR has reached its retained problem-version limit.", "Preserve its history; no feedback was evicted or partially collected.")
			}
			id = domain.NewID()
			value := domain.PRProblem{Version: 1, Type: domain.PRProblemEvidenceRecord, SetID: setRecord.ID, Kind: domain.PRFeedbackProblem, Target: target, Observation: *set.Feedback, ContentVersion: entry.ContentVersion, Feedback: &entry, OriginalProvider: &latest, LatestProvider: &latest, Current: true, State: domain.PRProblemUnhandled}
			if _, err = t.putPRProblem(id, 0, value); err != nil {
				return Record{}, 0, err
			}
			created++
		} else if err != nil {
			return Record{}, 0, storageError(err)
		} else {
			r, value, err := t.GetPRProblem(id)
			if err != nil {
				return Record{}, 0, err
			}
			if value.Feedback.ID != entry.ID || value.Feedback.Kind != entry.Kind {
				return Record{}, 0, prProblemConflict()
			}
			if !value.Current || !reflect.DeepEqual(value.LatestProvider, &latest) {
				value.Current, value.LatestProvider = true, &latest
				if _, err = t.putPRProblem(id, r.Revision, value); err != nil {
					return Record{}, 0, err
				}
			}
		}
		seen[id] = true
	}
	for _, id := range previous {
		if seen[id] {
			continue
		}
		r, value, err := t.GetPRProblem(id)
		if err != nil {
			return Record{}, 0, err
		}
		value.Current = false
		if _, err = t.putPRProblem(id, r.Revision, value); err != nil {
			return Record{}, 0, err
		}
	}
	return setRecord, created, nil
}

func (t *Tx) currentPRProblems(set domain.ID, kind domain.PRProblemKind, limit int) ([]domain.ID, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM pr_problem_records WHERE set_id=? AND kind=? AND current=1 ORDER BY id LIMIT ?", set, kind, limit+1)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return nil, storageError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(err)
	}
	if len(ids) > limit {
		return nil, prProblemConflict()
	}
	return ids, nil
}

func (t *Tx) DismissPRProblem(id domain.ID, expected uint64, version string) (Record, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, err
	}
	actor, err := t.prProblemActor()
	if err != nil {
		return Record{}, err
	}
	r, value, err := t.GetPRProblem(id)
	if err != nil {
		return r, err
	}
	if r.Revision != expected || value.ContentVersion != version {
		return r, prProblemConflict()
	}
	if value.State == domain.PRProblemDismissed {
		return r, nil
	}
	value.State = domain.PRProblemDismissed
	value.Dismissal = &domain.PRProblemDismissal{RequestID: t.requestID, ActorType: actor.Type, DeviceID: actor.DeviceID, At: t.now}
	if actor.Type == domain.OwnerDevice {
		value.Dismissal.DeviceID = ""
	}
	r, err = t.putPRProblem(id, expected, value)
	if err != nil {
		return r, err
	}
	setRecord, set, err := t.GetPRProblemSet(value.SetID)
	if err != nil {
		return r, err
	}
	// The shared epoch also invalidates a concurrent collector and paged reads.
	_, err = t.Put(domain.ProblemKind, setRecord.ID, setRecord.Revision, "", "", set)
	return r, err
}

func (t *Tx) ListPRProblems(set domain.ID, after domain.ID, limit int) ([]Record, bool, error) {
	if _, _, err := t.GetPRProblemSet(set); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 50 || after != "" && after.Validate() != nil {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid problem-history page.", "Use 1 through 50 entries and the original page cursor.")
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM pr_problem_records WHERE set_id=? AND id>? ORDER BY id LIMIT ?", set, after, limit+1)
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
	result := []Record{}
	size := 0
	for _, id := range ids {
		r, _, err := t.GetPRProblem(id)
		if err != nil {
			return nil, false, err
		}
		if len(result) == limit || size+len(r.Data) > 1<<20 {
			return result, true, nil
		}
		size += len(r.Data)
		result = append(result, r)
	}
	return result, false, nil
}
