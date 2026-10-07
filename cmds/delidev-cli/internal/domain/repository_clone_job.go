// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"path"
	"strings"
	"unicode"
)

// Selection is metadata from an explicit profile. It grants no Git credential.
type RepositoryCloneSelection struct {
	ProfileID    ID     `json:"profile_id"`
	Revision     uint64 `json:"revision"`
	GenerationID ID     `json:"generation_id"`
	RepositoryID string `json:"repository_id"`
	NodeID       string `json:"node_id"`
	Owner        string `json:"owner"`
	Name         string `json:"name"`
}
type RepositoryCloneInput struct {
	RepositoryID  ID                        `json:"repository_id"`
	MachineID     ID                        `json:"machine_id"`
	LocalOrigin   LocalOrigin               `json:"local_origin"`
	ParentPath    string                    `json:"parent_path"`
	URL           string                    `json:"url"`
	DirectoryName string                    `json:"directory_name"`
	GitHub        *RepositoryCloneSelection `json:"github,omitempty"`
}

func (s RepositoryCloneSelection) Validate() error {
	if s.ProfileID.Validate() != nil || s.Revision == 0 || s.GenerationID.Validate() != nil || !PositiveDecimal(s.RepositoryID) || Text(s.NodeID, "repository node identity", 256, true) != nil || ValidateGitHubRepository(s.Owner, s.Name) != nil {
		return Fail(InvalidArgument, "The GitHub repository selection is invalid.", "Select a repository from the current profile again.")
	}
	return nil
}
func ValidateRepositoryCloneParent(value string) error {
	if strings.ContainsFunc(value, unicode.IsControl) || Text(value, "clone parent path", 4096, true) != nil || strings.ContainsAny(value, "\x00\r\n") || (!path.IsAbs(value) && !windowsAbsolute(value)) {
		return Fail(InvalidArgument, "Choose an absolute parent folder on this computer.", "The parent folder must already exist.")
	}
	return nil
}
func (i RepositoryCloneInput) Validate() error {
	if UniqueIDs([]ID{i.RepositoryID, i.MachineID}) != nil || OwnershipBlocks(OwnershipMachine, i.RepositoryID, i.LocalOrigin.MachineID != i.MachineID) || i.LocalOrigin.DeviceID.Validate() != nil {
		return localCloneInputFailure()
	}
	if err := ValidateRepositoryCloneParent(i.ParentPath); err != nil {
		return err
	}
	if err := ValidateRepositoryCloneDirectory(i.DirectoryName); err != nil {
		return err
	}
	parsed, err := ParseRepositoryCloneURL(i.URL)
	if err != nil {
		return err
	}
	if i.GitHub != nil {
		if err := i.GitHub.Validate(); err != nil {
			return err
		}
		if !strings.EqualFold(parsed.GitHubOwner, i.GitHub.Owner) || !strings.EqualFold(parsed.GitHubName, i.GitHub.Name) {
			return Fail(InvalidArgument, "The Git URL no longer matches the selected GitHub repository.", "Select the repository again or remove the profile association.")
		}
	}
	return nil
}
func localCloneInputFailure() error {
	return Fail(InvalidArgument, "The clone assignment has invalid ownership.", "Verify this computer's Worker and inspect the original request.")
}
