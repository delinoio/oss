// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"time"
)

type GitHubRepositoryListEntry struct {
	Repository RemoteRepository `json:"repository"`
	Archived   bool             `json:"archived"`
	HTTPSURL   string           `json:"https_url"`
	SSHURL     string           `json:"ssh_url"`
}
type GitHubRepositoryPage struct {
	ProfileID       ID                          `json:"profile_id"`
	ProfileRevision string                      `json:"profile_revision"`
	GenerationID    ID                          `json:"generation_id"`
	ObservedAt      time.Time                   `json:"observed_at"`
	Page            uint32                      `json:"page"`
	PageSize        uint32                      `json:"page_size"`
	NextPage        uint32                      `json:"next_page"`
	Repositories    []GitHubRepositoryListEntry `json:"repositories"`
}

func ValidateGitHubRepositoryPageInput(page, size uint32) error {
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return Fail(InvalidArgument, "Invalid GitHub repository page.", "Select a page of at most 100 repositories.")
	}
	return nil
}
func (e GitHubRepositoryListEntry) Validate() error {
	r := e.Repository
	if r.Provider != GitHubCom || !PositiveDecimal(r.ID) || Text(r.NodeID, "repository node identity", 256, true) != nil || ValidateGitHubRepository(r.Owner, r.Name) != nil || Text(r.DefaultBranch, "default branch", 1024, false) != nil || strings.ContainsAny(r.DefaultBranch, "\r\n") || r.HeadCommit != "" || e.HTTPSURL != "https://github.com/"+r.Owner+"/"+r.Name+".git" || e.SSHURL != "git@github.com:"+r.Owner+"/"+r.Name+".git" {
		return Fail(Unavailable, "The GitHub repository page is malformed.", "Refresh the original profile and page; no repository selection was accepted.")
	}
	return nil
}
func (p GitHubRepositoryPage) Validate() error {
	invalid := func() error {
		return Fail(Unavailable, "The GitHub repository page could not be verified.", "Refresh the original profile and page.")
	}
	if p.ProfileID.Validate() != nil || !PositiveDecimal(p.ProfileRevision) || p.GenerationID.Validate() != nil || p.ObservedAt.IsZero() || ValidateGitHubRepositoryPageInput(p.Page, p.PageSize) != nil || p.Repositories == nil || len(p.Repositories) > int(p.PageSize) || (p.NextPage != 0 && (p.NextPage != p.Page+1 || p.NextPage > 1000000)) {
		return invalid()
	}
	ids, nodes, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range p.Repositories {
		r := e.Repository
		name := strings.ToLower(r.Owner + "/" + r.Name)
		if e.Validate() != nil || ids[r.ID] || nodes[r.NodeID] || names[name] {
			return invalid()
		}
		ids[r.ID], nodes[r.NodeID], names[name] = true, true, true
	}
	return nil
}
