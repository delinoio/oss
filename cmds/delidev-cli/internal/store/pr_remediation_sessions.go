package store

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// PRRemediationSessionPosition is internal pagination within one transaction.
// It must not be retained as a public cursor across different database snapshots.
type PRRemediationSessionPosition struct {
	UpdatedAt time.Time
	ID        domain.ID
}

// PRRemediationSessions lists linked, potentially reusable sessions by most
// recent persisted session activity, with a deterministic UUID tie-breaker.
// A link is historical provenance only. The caller must verify current project,
// account, harness and Worker workspace compatibility before choosing a session.
func (t *Tx) PRRemediationSessions(project domain.ID, target domain.SessionPullRequest, after PRRemediationSessionPosition, limit int) ([]Record, bool, error) {
	if _, err := t.prProblemActor(); err != nil {
		return nil, false, err
	}
	if project.Validate() != nil || target.Validate() != nil || limit < 1 || limit > 50 ||
		(after.ID == "") != after.UpdatedAt.IsZero() || (after.ID != "" && (after.ID.Validate() != nil || after.UpdatedAt.UnixMilli() < 0)) {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid linked PR session selection.", "Use the original project and stable PR identities within one database snapshot.")
	}
	query := `SELECT ` + recordColumns + ` FROM entities AS s WHERE s.kind='session' AND s.project_id=?
 AND json_extract(s.body,'$.archive')='active' AND json_extract(s.body,'$.recovery')='none'
 AND json_extract(s.body,'$.dispatch') IN ('blocked','ready','claimed')
 AND json_extract(s.body,'$.workspace') IN ('worktree','local') AND json_extract(s.body,'$.preparation.state')='ready'
 AND EXISTS(SELECT 1 FROM entities AS p WHERE p.kind='pull_request' AND p.session_id=s.id AND p.project_id=s.project_id
  AND json_extract(p.body,'$.provider')=? AND json_extract(p.body,'$.repository_id')=?
  AND json_extract(p.body,'$.remote_repository_id')=? AND json_extract(p.body,'$.repository_node_id')=?
  AND json_extract(p.body,'$.pull_request_id')=? AND json_extract(p.body,'$.pull_request_node_id')=?)`
	args := []any{project, target.Provider, target.RepositoryID, target.RemoteRepositoryID, target.RepositoryNodeID, target.PullRequestID, target.PullRequestNodeID}
	if after.ID != "" {
		query += " AND (s.updated_at<? OR (s.updated_at=? AND s.id<?))"
		args = append(args, after.UpdatedAt.UnixMilli(), after.UpdatedAt.UnixMilli(), after.ID)
	}
	query += " ORDER BY s.updated_at DESC,s.id DESC LIMIT ?"
	args = append(args, limit+1)
	return t.sessionPage(limit, query, args...)
}
