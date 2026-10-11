// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// PortableRepositoryDirectory freezes registered spelling once at acceptance.
// It never chooses a suffix or reads a later registration during replay.
func PortableRepositoryDirectory(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", domain.Fail(domain.InvalidArgument, "Invalid repository name.", "Use valid Unicode text.")
	}
	name = norm.NFC.String(strings.TrimSpace(name))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || strings.ContainsRune("<>:\"/\\|?*", c) {
			return '_'
		}
		return c
	}, name)
	name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
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
	// A managed child named .git is interpreted as Git administration metadata by snapshot storage.
	// Keep the conversion scoped to that exact component; remove it only if storage stops reserving it.
	if strings.EqualFold(name, ".git") {
		name = "_" + name[1:]
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		name = "_" + name
	}
	// Prefixing a device stem can add one byte to a 255-byte component.
	if len(name) > 255 {
		name = name[:255]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	return name, domain.ValidateRepositoryCloneDirectory(name)
}

func directoryCollisionKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}
func validDirectoryName(name string) bool {
	return name == "" || norm.NFC.String(name) == name && !strings.EqualFold(name, ".git") && domain.ValidateRepositoryCloneDirectory(name) == nil
}

// An omitted component is the immutable version-1 UUID layout, including Fork.
func repositoryDirectoryComponent(id domain.ID, name string) string {
	if name != "" {
		return name
	}
	return string(id)
}
func ownedRepositoryPath(root string, id domain.ID, name string) string {
	if id.Validate() != nil || !validDirectoryName(name) {
		return ""
	}
	return filepath.Join(root, repositoryDirectoryComponent(id, name))
}

func directoryNamesConflict() error {
	return domain.Fail(domain.Conflict, "Repository checkout directory names collide.", "Choose distinct registered names that remain distinct as portable folder names.")
}

// ValidateDirectoryNames admits the complete frozen set before any Git operation.
func ValidateDirectoryNames(request PrepareRequest) error { return validateDirectoryNames(request) }
func validateDirectoryNames(request PrepareRequest) error {
	seen := map[string]bool{directoryCollisionKey("manifest.json"): true}
	for _, repo := range request.Repositories {
		if !validDirectoryName(repo.DirectoryName) || repo.DirectoryName != "" && (request.Type != domain.Worktree || !repo.SourceKind.managed()) {
			return domain.Fail(domain.InvalidArgument, "Invalid managed checkout directory name.", "Preserve the original accepted portable directory name.")
		}
		if request.Type != domain.Worktree {
			continue
		}
		key := directoryCollisionKey(repositoryDirectoryComponent(repo.ID, repo.DirectoryName))
		if seen[key] {
			return directoryNamesConflict()
		}
		seen[key] = true
	}
	return nil
}
func validateManifestDirectoryNames(manifest Manifest) error {
	request := PrepareRequest{Type: manifest.Type}
	for _, repo := range manifest.Repositories {
		request.Repositories = append(request.Repositories, RepositorySpec{ID: repo.ID, SourceKind: repo.SourceKind, DirectoryName: repo.DirectoryName})
	}
	// Sidechat references preserve named parent paths without owning those files.
	if manifest.Reference != nil {
		for _, repo := range manifest.Repositories {
			if !validDirectoryName(repo.DirectoryName) {
				return ResultUncertain()
			}
		}
		return nil
	}
	return validateDirectoryNames(request)
}
func RequiresNamedDirectories(request PrepareRequest) bool {
	for _, repo := range request.Repositories {
		if repo.DirectoryName != "" {
			return true
		}
	}
	return false
}
func NamedDirectoriesUnsupported() error {
	return domain.Fail(domain.Unsupported, "Named managed checkout directories were not negotiated.", "Update and reconnect the original Worker before using this workspace.")
}

// Existing session roots are never adopted. This preflight also catches occupied
// destinations on case-sensitive hosts before any repository directory or Git child.
func ensureRepositoryDestinationsAbsent(root string, request PrepareRequest) error {
	if request.Type != domain.Worktree {
		return nil
	}
	for _, repo := range request.Repositories {
		if _, err := os.Lstat(ownedRepositoryPath(root, repo.ID, repo.DirectoryName)); err == nil {
			return domain.Fail(domain.Conflict, "The checkout destination already exists.", "Preserve existing content and choose another session.")
		} else if !errors.Is(err, os.ErrNotExist) {
			return ResultUncertain()
		}
	}
	return nil
}
