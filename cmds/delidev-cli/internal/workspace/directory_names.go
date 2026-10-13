// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// PortableRepositoryDirectory freezes display metadata into one portable path
// component. Accepted history never repeats this conversion after a rename.
func PortableRepositoryDirectory(name string) (string, error) {
	name = norm.NFC.String(strings.TrimSpace(name))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || strings.ContainsRune(`<>:"/\|?*`, c) {
			return '_'
		}
		return c
	}, name)
	name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	name = truncateDirectory(name, 255)
	name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	if name == "" {
		name = "repository"
	}
	if domain.IsWindowsReservedDeviceName(name) {
		name = "_" + truncateDirectory(name, 254)
		name = strings.TrimRightFunc(name, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
	}
	if !utf8.ValidString(name) {
		return "", ResultUncertain()
	}
	return name, domain.ValidateRepositoryCloneDirectory(name)
}

func truncateDirectory(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	name = name[:limit]
	for !utf8.ValidString(name) && len(name) > 0 {
		name = name[:len(name)-1]
	}
	return name
}

// RepositoryDirectory retains the historical UUID component for omitted names.
// Callers validate the frozen field before deriving filesystem authority.
func RepositoryDirectory(id domain.ID, frozen string) string {
	if frozen != "" {
		return frozen
	}
	return string(id)
}

func ownedRepositoryPath(root string, id domain.ID, frozen string) string {
	return filepath.Join(root, RepositoryDirectory(id, frozen))
}

func directoryCollisionKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}

// directoryAdmission selects the reserved-name set for frozen directory names.
type directoryAdmission int

const (
	// newDirectoryAdmission applies the complete Windows reserved-device set to
	// names frozen by a fresh Worktree acceptance.
	newDirectoryAdmission directoryAdmission = iota
	// historicalDirectoryAdmission replays names that earlier releases accepted.
	historicalDirectoryAdmission
)

func (a directoryAdmission) validate(name string) error {
	if a == historicalDirectoryAdmission {
		return domain.ValidateHistoricalRepositoryDirectory(name)
	}
	return domain.ValidateRepositoryCloneDirectory(name)
}

func validateRepositoryDirectories(r PrepareRequest, admission directoryAdmission) error {
	seen := map[string]bool{directoryCollisionKey("manifest.json"): true}
	for _, repo := range r.Repositories {
		if repo.DirectoryName != "" && (r.Type != domain.Worktree || norm.NFC.String(repo.DirectoryName) != repo.DirectoryName || admission.validate(repo.DirectoryName) != nil || len(repo.DirectoryName) > 255) {
			return ResultUncertain()
		}
		if r.Type != domain.Worktree {
			continue
		}
		key := directoryCollisionKey(RepositoryDirectory(repo.ID, repo.DirectoryName))
		if seen[key] {
			return domain.Fail(domain.Conflict, "Repository checkout directory names collide.", "Rename the registered repositories before accepting a new workspace.")
		}
		seen[key] = true
	}
	return nil
}

func (r PrepareRequest) RequiresNamedDirectories() bool {
	for _, repo := range r.Repositories {
		if repo.DirectoryName != "" {
			return true
		}
	}
	return false
}

func NamedDirectoriesUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "Named workspace directories were not negotiated.", "Update and reconnect the original Worker before using this workspace.")
}

func (m *Manager) requireNamedDirectories(r PrepareRequest) error {
	if r.RequiresNamedDirectories() && !m.NamedDirectories {
		return NamedDirectoriesUnsupported()
	}
	return nil
}

// Check every destination before the first repository Git operation. Existing
// scopes are handled by immutable replay, never adopted by this fresh admission.
func checkRepositoryDestinations(root string, r PrepareRequest) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ResultUncertain()
	}
	occupied := map[string]bool{}
	for _, entry := range entries {
		occupied[directoryCollisionKey(entry.Name())] = true
	}
	for _, repo := range r.Repositories {
		if occupied[directoryCollisionKey(RepositoryDirectory(repo.ID, repo.DirectoryName))] {
			return domain.Fail(domain.Conflict, "A repository checkout destination is occupied.", "Preserve existing content and choose a fresh workspace.")
		}
	}
	return nil
}

func validateFrozenDirectory(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 255 || !utf8.ValidString(name) || norm.NFC.String(name) != name {
		return ResultUncertain()
	}
	return domain.ValidateHistoricalRepositoryDirectory(name)
}

func manifestRepositorySpecs(manifest Manifest) []RepositorySpec {
	specs := make([]RepositorySpec, 0, len(manifest.Repositories))
	for _, repo := range manifest.Repositories {
		specs = append(specs, RepositorySpec{ID: repo.ID, DirectoryName: repo.DirectoryName})
	}
	return specs
}

// ForkRequiresNamedDirectories reports whether the frozen source preparation
// uses named repository directories. Independent Forks and Sidechat share this
// predicate: a Sidechat child owns only reference metadata, but its source
// inspection and reference publication still resolve the parent's frozen
// directory names and therefore need the negotiated named-directory layout.
func ForkRequiresNamedDirectories(input domain.ForkJobInput) (bool, error) {
	var preparation PrepareRequest
	if domain.Decode(input.SourceAssignment.Preparation, &preparation) != nil {
		return false, ResultUncertain()
	}
	return preparation.RequiresNamedDirectories(), nil
}

// ValidateDirectories checks the frozen names of a freshly accepted request.
// Replay paths use the historical validator and never call this method.
func (r PrepareRequest) ValidateDirectories() error {
	return validateRepositoryDirectories(r, newDirectoryAdmission)
}
