package domain

import "time"

type PRWorkspaceState string

const (
	PRWorkspaceComplete   PRWorkspaceState = "complete"
	PRWorkspaceIncomplete PRWorkspaceState = "incomplete"
	PRWorkspaceAmbiguous  PRWorkspaceState = "ambiguous"
)

type PRChangeCounts struct {
	Additions uint64 `json:"additions"`
	Deletions uint64 `json:"deletions"`
}
type PRWorkspaceRow struct {
	Item            RepositoryItem  `json:"item"`
	Counts          *PRChangeCounts `json:"counts"`
	AvatarReference string          `json:"avatar_reference,omitempty"`
	// The locator is private adapter data. It never crosses the product boundary.
	AvatarLocator string `json:"-"`
	Seed          bool   `json:"seed"`
}
type PRWorkspaceEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}
type PRWorkspace struct {
	RepositoryID       ID                `json:"repository_id"`
	RepositoryRevision string            `json:"repository_revision"`
	ProfileID          ID                `json:"profile_id"`
	GenerationID       ID                `json:"generation_id"`
	Repository         RemoteRepository  `json:"repository"`
	State              PRWorkspaceState  `json:"state"`
	Rows               []PRWorkspaceRow  `json:"rows"`
	Edges              []PRWorkspaceEdge `json:"edges"`
	ObservedAt         time.Time         `json:"observed_at"`
}
type PRCommit struct {
	SHA             string          `json:"sha"`
	Message         string          `json:"message"`
	Author          string          `json:"author"`
	AuthoredAt      time.Time       `json:"authored_at"`
	Committer       string          `json:"committer"`
	CommittedAt     time.Time       `json:"committed_at"`
	Parents         []string        `json:"parents"`
	Counts          *PRChangeCounts `json:"counts"`
	AvatarReference string          `json:"avatar_reference,omitempty"`
	AvatarActor     string          `json:"-"`
	AvatarLocator   string          `json:"-"`
}
type PRCommitPage struct {
	RepositoryID       ID               `json:"repository_id"`
	RepositoryRevision string           `json:"repository_revision"`
	ProfileID          ID               `json:"profile_id"`
	GenerationID       ID               `json:"generation_id"`
	Repository         RemoteRepository `json:"repository"`
	Item               RepositoryItem   `json:"item"`
	Commits            []PRCommit       `json:"commits"`
	NextPageToken      string           `json:"next_page_token"`
}

func PRWorkspaceSHA(value string) bool { return repositorySHA(value) }
