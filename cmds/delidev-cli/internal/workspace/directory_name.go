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

// RepositoryDirectoryName converts a registration name once at acceptance.
// Replay, recovery and Fork use the retained component, never current names.
func RepositoryDirectoryName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", domain.Fail(domain.InvalidArgument, "The repository name is not valid UTF-8.", "Use a valid registered repository name.")
	}
	name = norm.NFC.String(strings.TrimSpace(name))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || strings.ContainsRune("<>:\"/\\|?*", c) {
			return '_'
		}
		return c
	}, name)
	name = trimDirectoryEnd(truncateDirectoryName(name, 255))
	if name == "" {
		name = "repository"
	}
	if reservedDirectoryName(name) {
		name = "_" + trimDirectoryEnd(truncateDirectoryName(name, 254))
	}
	if err := domain.ValidateRepositoryCloneDirectory(name); err != nil {
		return "", err
	}
	return name, nil
}

func truncateDirectoryName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	for !utf8.RuneStart(name[limit]) {
		limit--
	}
	return name[:limit]
}

func trimDirectoryEnd(name string) string {
	return strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
}

func reservedDirectoryName(name string) bool {
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	return stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}

func directoryCollisionKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}

// Invalid metadata produces no path. Callers must compare it with the retained
// path before opening or removing anything. Historical omission keeps the UUID.
func repositoryDirectoryComponent(id domain.ID, name string) string {
	if id.Validate() != nil {
		return ""
	}
	if name == "" {
		return string(id)
	}
	if !norm.NFC.IsNormalString(name) || domain.ValidateRepositoryCloneDirectory(name) != nil {
		return ""
	}
	return name
}

func ownedRepositoryPath(root string, id domain.ID, name string) string {
	component := repositoryDirectoryComponent(id, name)
	if component == "" {
		return ""
	}
	return filepath.Join(root, component)
}

// ValidateDirectoryNames is portable and has no filesystem or Git side effects.
// Include omitted UUID components in the same collision namespace.
func (r PrepareRequest) ValidateDirectoryNames() error {
	seen := map[string]bool{directoryCollisionKey("manifest.json"): true}
	for _, repo := range r.Repositories {
		if r.Type != domain.Worktree {
			if repo.DirectoryName != "" {
				return domain.Fail(domain.InvalidArgument, "Only managed Worktree directories can have an accepted name.", "Preserve original Local checkout paths.")
			}
			continue
		}
		component := repositoryDirectoryComponent(repo.ID, repo.DirectoryName)
		if component == "" {
			return domain.Fail(domain.InvalidArgument, "The accepted repository directory name is invalid.", "Use a validated portable component without changing accepted preparation metadata.")
		}
		key := directoryCollisionKey(component)
		if seen[key] {
			return domain.Fail(domain.Conflict, "Repository directory names collide in this workspace.", "Choose distinct registered names before accepting a new workspace.")
		}
		seen[key] = true
	}
	return nil
}

func (r PrepareRequest) UsesNamedManagedDirectories() bool {
	for _, repo := range r.Repositories {
		if repo.DirectoryName != "" {
			return true
		}
	}
	return r.SidechatSource != nil && r.SidechatSource.Preparation.UsesNamedManagedDirectories()
}

func NamedManagedDirectoriesUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "Named managed directories were not negotiated with this Worker.", "Update and reconnect the original Worker before using this workspace.")
}

func requireRepositoryDestinationsAbsent(root string, request PrepareRequest) error {
	if request.Type != domain.Worktree {
		return nil
	}
	for _, repo := range request.Repositories {
		path := ownedRepositoryPath(root, repo.ID, repo.DirectoryName)
		if path == "" {
			return ResultUncertain()
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return domain.Fail(domain.Conflict, "An accepted repository destination is occupied.", "Preserve existing files and reconcile the original workspace before retrying.")
		}
	}
	return nil
}
