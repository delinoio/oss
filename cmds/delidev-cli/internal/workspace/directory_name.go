package workspace

import (
	"path/filepath"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Directory returns the immutable live checkout directory name. A manifest
// without DirectoryName keeps the historical repository-UUID layout.
func (r PreparedRepository) Directory() string {
	if r.DirectoryName != "" {
		return r.DirectoryName
	}
	return string(r.ID)
}

// ownedRepositoryPath is the only owned checkout path derivation.
func ownedRepositoryPath(root string, id domain.ID, directoryName string) string {
	if directoryName != "" {
		return filepath.Join(root, directoryName)
	}
	return filepath.Join(root, string(id))
}

func (r PreparedRepository) ownedPath(root string) string {
	return ownedRepositoryPath(root, r.ID, r.DirectoryName)
}

// validManagedDirectoryName accepts only a name that is already in canonical
// portable form, so a tampered or hand-written value cannot become authority.
func validManagedDirectoryName(name string) bool {
	return name == "" || domain.ValidateRepositoryCloneDirectory(name) == nil && domain.PortableDirectoryName(name) == name
}

func managedDirectoryKey(name string) string {
	return cases.Fold().String(norm.NFC.String(name))
}

// validateDirectoryNames rejects invalid and colliding names before any Git or
// directory side effect. Collisions use NFC plus Unicode default case folding
// and include workspace metadata names and legacy UUID-named siblings.
func validateDirectoryNames(workspaceType domain.WorkspaceType, repositories []RepositorySpec) error {
	seen := map[string]bool{managedDirectoryKey("manifest.json"): true, managedDirectoryKey("chat"): true, managedDirectoryKey(".git"): true}
	for _, repo := range repositories {
		if repo.DirectoryName != "" && (workspaceType != domain.Worktree || !repo.SourceKind.managed() || !validManagedDirectoryName(repo.DirectoryName)) {
			return domain.Fail(domain.InvalidArgument, "A managed repository directory name is invalid.", "Use the registered repository name conversion.")
		}
	}
	for _, repo := range repositories {
		name := repo.DirectoryName
		if name == "" {
			name = string(repo.ID)
		}
		key := managedDirectoryKey(name)
		if seen[key] {
			return domain.Fail(domain.Conflict, "Two repositories would use the same workspace directory name.", "Rename one registered repository so the converted folder names differ.")
		}
		seen[key] = true
	}
	return nil
}

// ManifestHasNamedDirectories reports whether a serialized manifest carries a
// frozen named directory, which requires the named-directory Worker capability.
func ManifestHasNamedDirectories(raw []byte) bool {
	var manifest Manifest
	if domain.Decode(raw, &manifest) != nil {
		return false
	}
	for _, repository := range manifest.Repositories {
		if repository.DirectoryName != "" {
			return true
		}
	}
	return false
}

// ForkRequiresNamedDirectories reports whether the source manifest carries any
// frozen named directory, which a child preparation must preserve.
func ForkRequiresNamedDirectories(input domain.ForkJobInput) bool {
	var source Manifest
	if domain.Decode(input.SourceAssignment.Manifest, &source) != nil {
		return false
	}
	for _, repository := range source.Repositories {
		if repository.DirectoryName != "" {
			return true
		}
	}
	return false
}
