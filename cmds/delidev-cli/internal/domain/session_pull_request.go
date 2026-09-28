package domain

import "time"

const MaxSessionPullRequests = 100

// SessionPullRequest is retained association provenance, not current GitHub
// state, repository access, problem evidence or permission to execute a fix.
type SessionPullRequest struct {
	Version            uint32              `json:"version"`
	Provider           IntegrationProvider `json:"provider"`
	RepositoryID       ID                  `json:"repository_id"`
	RemoteRepositoryID string              `json:"remote_repository_id"`
	RepositoryNodeID   string              `json:"repository_node_id"`
	Owner              string              `json:"owner"`
	Name               string              `json:"name"`
	PullRequestID      string              `json:"pull_request_id"`
	PullRequestNodeID  string              `json:"pull_request_node_id"`
	Number             string              `json:"number"`
	Title              string              `json:"title"`
	ObservedAt         time.Time           `json:"observed_at"`
}

func (v SessionPullRequest) Validate() error {
	if v.Version != 1 || v.Provider != GitHubCom || v.RepositoryID.Validate() != nil ||
		!PositiveDecimal(v.RemoteRepositoryID) || !PositiveDecimal(v.PullRequestID) || !PositiveDecimal(v.Number) ||
		Text(v.RepositoryNodeID, "repository node identity", 256, true) != nil || Text(v.PullRequestNodeID, "PR node identity", 256, true) != nil ||
		ValidateGitHubRepository(v.Owner, v.Name) != nil || Text(v.Title, "PR title", 4096, true) != nil || v.ObservedAt.IsZero() || v.ObservedAt.Year() < 1970 || v.ObservedAt.Year() > 9999 {
		return Fail(RecoveryRequired, "The retained PR association is inconsistent.", "Preserve its original session and repository identities for inspection.")
	}
	return nil
}

func (v SessionPullRequest) SamePR(other SessionPullRequest) bool {
	return v.Provider == other.Provider && v.RemoteRepositoryID == other.RemoteRepositoryID && v.PullRequestID == other.PullRequestID
}
