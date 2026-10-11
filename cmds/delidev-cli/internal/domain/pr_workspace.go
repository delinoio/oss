// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strconv"
	"time"
)

// Workspace observations are disposable reads, never persisted authorization.
type PRWorkspaceState string

const (
	PRWorkspaceComplete   PRWorkspaceState = "complete"
	PRWorkspaceIncomplete PRWorkspaceState = "incomplete"
	PRWorkspaceAmbiguous  PRWorkspaceState = "ambiguous"
)

type PRChangeCounts struct {
	Additions string `json:"additions"`
	Deletions string `json:"deletions"`
}

func (c PRChangeCounts) Validate() bool {
	for _, value := range []string{c.Additions, c.Deletions} {
		n, e := strconv.ParseUint(value, 10, 64)
		if e != nil || strconv.FormatUint(n, 10) != value {
			return false
		}
	}
	return true
}

type PRWorkspaceNode struct {
	Item            RepositoryItem  `json:"item"`
	Counts          *PRChangeCounts `json:"counts,omitempty"`
	AvatarReference string          `json:"avatar_reference,omitempty"`
	Seed            bool            `json:"seed"`
}
type PRWorkspaceEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}
type PRReadScope struct {
	RepositoryID       ID               `json:"repository_id"`
	RepositoryRevision string           `json:"repository_revision"`
	ProfileID          ID               `json:"profile_id"`
	GenerationID       ID               `json:"generation_id"`
	ObservedAt         time.Time        `json:"observed_at"`
	Repository         RemoteRepository `json:"repository"`
}

func (s PRReadScope) Validate() bool {
	return s.RepositoryID.Validate() == nil && PositiveDecimal(s.RepositoryRevision) && s.ProfileID.Validate() == nil && s.GenerationID.Validate() == nil && !s.ObservedAt.IsZero() && PositiveDecimal(s.Repository.ID) && Text(s.Repository.NodeID, "repository node", 256, true) == nil && ValidateGitHubRepository(s.Repository.Owner, s.Repository.Name) == nil && s.Repository.Provider == GitHubCom
}

type PRWorkspace struct {
	PRReadScope
	State   PRWorkspaceState  `json:"state"`
	Reasons []string          `json:"reasons"`
	Seeds   []string          `json:"seeds"`
	Nodes   []PRWorkspaceNode `json:"nodes"`
	Edges   []PRWorkspaceEdge `json:"edges"`
}

func (w PRWorkspace) Validate() bool {
	if !w.PRReadScope.Validate() || len(w.Seeds) > 20 || len(w.Seeds) == 0 || len(w.Nodes) > 100 || w.Nodes == nil || w.Edges == nil || w.Reasons == nil || (w.State != PRWorkspaceComplete && w.State != PRWorkspaceIncomplete && w.State != PRWorkspaceAmbiguous) {
		return false
	}
	seen := map[string]RepositoryItem{}
	seeds := map[string]bool{}
	for _, n := range w.Seeds {
		if !PositiveDecimal(n) || seeds[n] {
			return false
		}
		seeds[n] = true
	}
	for _, n := range w.Nodes {
		q := RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryDetail, Number: n.Item.Number}
		if n.Item.Validate(w.Repository, q) != nil || (n.Counts != nil && !n.Counts.Validate()) || (n.AvatarReference != "" && ID(n.AvatarReference).Validate() != nil) || n.Seed != seeds[n.Item.Number] {
			return false
		}
		if _, ok := seen[n.Item.Number]; ok {
			return false
		}
		seen[n.Item.Number] = n.Item
	}
	for _, edge := range w.Edges {
		a, aok := seen[edge.Parent]
		b, bok := seen[edge.Child]
		if !aok || !bok || a.Number == b.Number || a.HeadRef != b.BaseRef || a.HeadRepository == nil || a.HeadRepository.Repository == nil || a.HeadRepository.Repository.ID != w.Repository.ID || b.HeadRepository == nil || b.HeadRepository.Repository == nil || b.HeadRepository.Repository.ID != w.Repository.ID {
			return false
		}
	}
	for _, reason := range w.Reasons {
		if Text(reason, "workspace reason", 64, true) != nil {
			return false
		}
	}
	return true
}

type PRCommitPerson struct {
	Name            string           `json:"name"`
	Date            time.Time        `json:"date"`
	Actor           *RepositoryActor `json:"actor,omitempty"`
	AvatarReference string           `json:"avatar_reference,omitempty"`
}
type PRCommit struct {
	SHA       string          `json:"sha"`
	Message   string          `json:"message"`
	Author    PRCommitPerson  `json:"author"`
	Committer PRCommitPerson  `json:"committer"`
	Parents   []string        `json:"parents"`
	Counts    *PRChangeCounts `json:"counts,omitempty"`
}
type PRCommits struct {
	PRReadScope
	PullRequestID string     `json:"pull_request_id"`
	Number        string     `json:"number"`
	BaseSHA       string     `json:"base_sha"`
	HeadSHA       string     `json:"head_sha"`
	PageToken     string     `json:"page_token"`
	NextPageToken string     `json:"next_page_token"`
	Commits       []PRCommit `json:"commits"`
}

func (v PRCommits) Validate() bool {
	if !v.PRReadScope.Validate() || !PositiveDecimal(v.PullRequestID) || !PositiveDecimal(v.Number) || !repositorySHA(v.BaseSHA) || !repositorySHA(v.HeadSHA) || v.Commits == nil || len(v.Commits) > 20 || Text(v.PageToken, "page token", 2048, false) != nil || Text(v.NextPageToken, "next page token", 2048, false) != nil || v.PageToken != "" && v.PageToken == v.NextPageToken {
		return false
	}
	seen := map[string]bool{}
	for _, c := range v.Commits {
		if !repositorySHA(c.SHA) || seen[c.SHA] || Text(c.Message, "commit message", 128<<10, false) != nil || c.Parents == nil || len(c.Parents) > 100 || c.Counts != nil && !c.Counts.Validate() {
			return false
		}
		seen[c.SHA] = true
		for _, p := range []PRCommitPerson{c.Author, c.Committer} {
			if Text(p.Name, "commit name", 4096, false) != nil || p.Date.IsZero() || p.Actor != nil && p.Actor.Validate() != nil || p.AvatarReference != "" && ID(p.AvatarReference).Validate() != nil {
				return false
			}
		}
		for _, p := range c.Parents {
			if !repositorySHA(p) {
				return false
			}
		}
	}
	return true
}
