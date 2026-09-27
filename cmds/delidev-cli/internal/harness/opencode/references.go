package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// WorkspaceReference is an additional repository from the independently owned
// Worker manifest. Native local references expose it without cloning a remote,
// changing the primary cwd or replacing native tool permissions.
type WorkspaceReference struct {
	RepositoryID domain.ID `json:"repository_id"`
	Path         string    `json:"path"`
}

const referenceDescription = "Additional repository in the selected DeliDev workspace."
const referenceConfigSchema = "https://opencode.ai/config.json"

func referenceName(index int, reference WorkspaceReference) string {
	// Native configuration is a map. Keep its sorted key order identical to
	// the immutable prepared repository order, independently of UUID order.
	return fmt.Sprintf("repository-%03d-%s", index, reference.RepositoryID)
}

func validWorkspaceReferences(references []WorkspaceReference, workspace, home string) bool {
	if len(references) > 99 {
		return false
	}
	seen := map[domain.ID]bool{}
	roots := []string{workspace, home}
	for _, reference := range references {
		// The native default permission rule appends a wildcard to this path.
		// Reject unverified pattern/interpolation spellings rather than silently
		// widening scope. A future native escaping profile may relax this bound.
		if reference.RepositoryID.Validate() != nil || seen[reference.RepositoryID] || !checkpointPath(reference.Path) || strings.ContainsAny(filepath.ToSlash(reference.Path), "*?[]{}\\") {
			return false
		}
		seen[reference.RepositoryID] = true
		for _, root := range roots {
			if directoryContains(root, reference.Path) || directoryContains(reference.Path, root) {
				return false
			}
		}
		roots = append(roots, reference.Path)
	}
	return true
}

func (p *nativeAPIProfile) referenceConfig() map[string]any {
	references := make(map[string]any, len(p.References))
	for i, reference := range p.References {
		references[referenceName(i, reference)] = map[string]any{"path": reference.Path, "description": referenceDescription}
	}
	return references
}

func validCheckpointReferences(value nativeCheckpoint) bool {
	return validWorkspaceReferences(value.References, value.Workspace, value.RuntimeHome) && (len(value.References) == 0 || value.Snapshot != nil && value.NativeRoot == value.Workspace)
}

func (p *nativeAPIProfile) referenceDocument() map[string]any {
	// Without its pinned schema field the legacy loader rewrites this file.
	return map[string]any{"$schema": referenceConfigSchema, "references": p.referenceConfig()}
}

func (p *nativeAPIProfile) inspectReferences() error {
	for _, reference := range p.References {
		if !canonicalDirectory(reference.Path) {
			return sessionUncertain()
		}
	}
	if len(p.References) > 0 && p.ReferencePath != "" {
		parent := filepath.Dir(p.ReferencePath)
		if security.CheckPrivateDir(parent) != nil || !canonicalDirectory(parent) {
			return sessionUncertain()
		}
		want, err := json.Marshal(p.referenceDocument())
		raw, readErr := security.ReadPrivate(p.ReferencePath, maxHTTPBody)
		if err != nil || readErr != nil || !bytes.Equal(raw, want) || !referenceSourcesEmpty(p.ReferenceWorkspace) {
			return sessionUncertain()
		}
	}
	return nil
}

// The pinned v2 loader ignores OPENCODE_CONFIG_CONTENT and project-disable
// flags. Supply only local references in our explicit private config directory;
// refuse project sources that v2 could merge. Remove this dual-loader bridge
// only after a verified native version consumes the isolated inline profile.
func (p *nativeAPIProfile) writeReferences(home, workspace string) error {
	if len(p.References) == 0 {
		return nil
	}
	if !referenceSourcesEmpty(workspace) {
		return incompatible()
	}
	path := filepath.Join(home, "opencode")
	if err := security.PrivateDir(path); err != nil {
		return sessionUncertain()
	}
	path = filepath.Join(path, "opencode.json")
	raw, err := json.Marshal(p.referenceDocument())
	if err != nil {
		return sessionInvalid()
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return sessionUncertain()
	}
	n, written := file.Write(raw)
	synced, closed := file.Sync(), file.Close()
	if n != len(raw) || written != nil || synced != nil || closed != nil || security.SyncParent(path) != nil {
		return sessionUncertain()
	}
	p.ReferencePath, p.ReferenceWorkspace = path, workspace
	return p.inspectReferences()
}

func referenceSourcesEmpty(workspace string) bool {
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		if _, err := os.Lstat(filepath.Join(workspace, name)); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// Diagnostic counts never grant authority or expose native paths/payloads.
func observedReferencePolicyCounts(raw []byte, selected PrimaryAgent, references []WorkspaceReference) (int, int) {
	var agents []struct {
		Name       PrimaryAgent     `json:"name"`
		Permission []PermissionRule `json:"permission"`
	}
	if json.Unmarshal(raw, &agents) != nil {
		return 0, 0
	}
	for _, agent := range agents {
		if agent.Name != selected {
			continue
		}
		matches := 0
		for _, rule := range agent.Permission {
			for _, reference := range references {
				if rule == (PermissionRule{"external_directory", filepath.Join(reference.Path, "*"), PermissionAllow}) {
					matches++
				}
			}
		}
		return len(agent.Permission), matches
	}
	return 0, 0
}
