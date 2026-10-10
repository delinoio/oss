// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// PortableRepositoryDirectory freezes a display name into a portable single
// component. Names remain presentation; the repository UUID retains identity.
func PortableRepositoryDirectory(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", directoryNameInvalid()
	}
	name = norm.NFC.String(strings.TrimSpace(name))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || strings.ContainsRune("<>:\"/\\|?*", c) {
			return '_'
		}
		return c
	}, name)
	if len(name) > 255 {
		name = name[:255]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	if name == "" {
		name = "repository"
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		name = "_" + name
		if len(name) > 255 {
			name = name[:255]
			for !utf8.ValidString(name) {
				name = name[:len(name)-1]
			}
			name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
		}
	}
	if err := domain.ValidateRepositoryCloneDirectory(name); err != nil {
		return "", err
	}
	return name, nil
}
func directoryNameInvalid() error {
	return domain.Fail(domain.InvalidArgument, "Invalid managed repository directory name.", "Use a portable single folder component.")
}
func directoryNameKey(name string) string { return cases.Fold().String(norm.NFC.String(name)) }
func repositoryDirectory(id domain.ID, name string) string {
	if name != "" {
		return name
	}
	return string(id)
}

// ValidateDirectoryNames admits the complete layout before Git or any managed
// repository directory is created, including legacy UUID components in mixed inputs.
func ValidateDirectoryNames(request PrepareRequest) error { return validateDirectoryNames(request) }
func validateDirectoryNames(request PrepareRequest) error {
	seen := map[string]bool{directoryNameKey("manifest.json"): true}
	for _, repo := range request.Repositories {
		if repo.DirectoryName != "" {
			if request.Type != domain.Worktree || len(repo.DirectoryName) > 255 || !utf8.ValidString(repo.DirectoryName) || !norm.NFC.IsNormalString(repo.DirectoryName) || domain.ValidateRepositoryCloneDirectory(repo.DirectoryName) != nil {
				return directoryNameInvalid()
			}
		}
		if request.Type != domain.Worktree {
			continue
		}
		key := directoryNameKey(repositoryDirectory(repo.ID, repo.DirectoryName))
		if seen[key] {
			return domain.Fail(domain.Conflict, "Managed repository directory names collide.", "Choose distinct repository names before creating the session.")
		}
		seen[key] = true
	}
	return nil
}
func (request PrepareRequest) HasNamedDirectories() bool {
	if request.SidechatSource != nil {
		for _, repo := range request.SidechatSource.Preparation.Repositories {
			if repo.DirectoryName != "" {
				return true
			}
		}
	}
	for _, repo := range request.Repositories {
		if repo.DirectoryName != "" {
			return true
		}
	}
	return false
}
func NamedDirectoriesUnsupported() error {
	return domain.Fail(domain.Unsupported, "Named managed repository directories were not negotiated.", "Update and reconnect the selected Worker.")
}

func ValidateManifestDirectoryNames(manifest Manifest) error {
	request := PrepareRequest{Type: manifest.Type}
	for _, repo := range manifest.Repositories {
		request.Repositories = append(request.Repositories, RepositorySpec{ID: repo.ID, DirectoryName: repo.DirectoryName})
	}
	return validateDirectoryNames(request)
}
func ForkHasNamedDirectories(input domain.ForkJobInput) (bool, error) {
	var preparation PrepareRequest
	if domain.Decode(input.SourceAssignment.Preparation, &preparation) != nil {
		return false, ResultUncertain()
	}
	return input.Workspace == domain.Worktree && preparation.HasNamedDirectories(), nil
}

// JobHasNamedDirectories reads only immutable private assignments. It never
// derives layout from current registration or reported Worker paths.
func JobHasNamedDirectories(job domain.Job) (bool, error) {
	switch job.Type {
	case domain.PrepareWorkspaceJob:
		var input PrepareRequest
		if domain.Decode(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		return input.HasNamedDirectories(), nil
	case domain.RecoverWorkspaceJob:
		var input RecoveryRequest
		if domain.Decode(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		return input.Preparation.HasNamedDirectories(), nil
	case domain.WorkspaceStorageJob:
		var input StorageRequest
		if DecodeStorageRequest(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		return input.Preparation.HasNamedDirectories(), nil
	case domain.RecoverExecutionJob:
		var input domain.ExecutionRecoveryRequest
		if domain.Decode(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		var preparation PrepareRequest
		if domain.Decode(input.Preparation, &preparation) != nil {
			return false, ResultUncertain()
		}
		return preparation.HasNamedDirectories(), nil
	case domain.ExecuteSessionJob:
		var input domain.ExecutionJobInput
		if domain.Decode(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		var preparation PrepareRequest
		if domain.Decode(input.Preparation, &preparation) != nil {
			return false, ResultUncertain()
		}
		return preparation.HasNamedDirectories(), nil
	case domain.ForkSessionJob:
		var input domain.ForkJobInput
		if domain.Decode(job.Input, &input) != nil {
			return false, ResultUncertain()
		}
		return ForkHasNamedDirectories(input)
	}
	return false, nil
}
