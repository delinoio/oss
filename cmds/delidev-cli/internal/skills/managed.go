// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type managedReceipt struct {
	ActorID domain.ID                 `json:"actor_id"`
	Digest  string                    `json:"digest"`
	Result  domain.ManagedSkillResult `json:"result"`
}
type managedCatalog struct {
	Version        uint32                                   `json:"version"`
	ServerID       domain.ID                                `json:"server_id"`
	WorkerDeviceID domain.ID                                `json:"worker_device_id"`
	Packages       map[domain.ID]domain.ManagedSkillPackage `json:"packages"`
	Receipts       map[domain.ID]managedReceipt             `json:"receipts"`
}

func managedRoot(root string, server, worker domain.ID) string {
	return filepath.Join(root, "managed-skills", string(server), string(worker))
}
func (c managedCatalog) valid(server, worker domain.ID) bool {
	if c.Version != 1 || c.ServerID != server || c.WorkerDeviceID != worker || c.Packages == nil || c.Receipts == nil || len(c.Packages) > MaxInventory || len(c.Receipts) > 4096 {
		return false
	}
	for id, p := range c.Packages {
		if id != p.ID || p.WorkerDeviceID != worker || p.Validate() != nil {
			return false
		}
	}
	for id, r := range c.Receipts {
		if r.ActorID.Validate() != nil || r.Result.RequestID != id || r.Result.WorkerDeviceID != worker || r.Result.Validate() != nil {
			return false
		}
	}
	return true
}
func writePackage(ctx context.Context, files map[string]packageFile, digest, target string) error {
	if err := os.Mkdir(target, 0700); err != nil {
		return unavailable()
	}
	for name, resource := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(target, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return unavailable()
		}
		if err := security.WriteAtomicOwnedMode(path, resource.Bytes, resource.Executable); err != nil {
			return unavailable()
		}
	}
	_, actual, err := packageFiles(ctx, target)
	if err != nil || actual != digest {
		return unavailable()
	}
	return nil
}

// Managed keeps mutations under one owned lock. Pending receipts are durable
// before copying or cleanup; an interrupted operation requires inspection and
// never repeats a filesystem mutation under a replacement request identity.
func (m Manager) Managed(ctx context.Context, q domain.ManagedSkillRequest) (domain.ManagedSkillResult, error) {
	var result domain.ManagedSkillResult
	for _, id := range []domain.ID{q.ServerID, q.ActorID, q.MachineID, q.WorkerDeviceID, q.WorkerInstanceID, q.RequestID} {
		if id.Validate() != nil {
			return result, domain.ManagedSkillUnavailable()
		}
	}
	if q.Mutation.Validate() != nil {
		return result, domain.ManagedSkillUnavailable()
	}
	root := managedRoot(m.Root, q.ServerID, q.WorkerDeviceID)
	if err := security.PrivateDir(root); err != nil {
		return result, err
	}
	lock, err := security.TryLock(filepath.Join(root, "catalog.lock"))
	if err != nil {
		return result, domain.ManagedSkillUnavailable()
	}
	defer lock.Close()
	path := filepath.Join(root, "catalog.json")
	catalog := managedCatalog{Version: 1, ServerID: q.ServerID, WorkerDeviceID: q.WorkerDeviceID, Packages: map[domain.ID]domain.ManagedSkillPackage{}, Receipts: map[domain.ID]managedReceipt{}}
	raw, err := security.ReadPrivate(path, 32<<20)
	if err == nil {
		if domain.DecodeBounded(raw, &catalog, 32<<20) != nil || !catalog.valid(q.ServerID, q.WorkerDeviceID) {
			return result, domain.ManagedSkillUnavailable()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	save := func() error {
		raw, err := json.Marshal(catalog)
		if err != nil || len(raw) > 32<<20 {
			return bound()
		}
		return security.WriteAtomic(path, raw)
	}
	inventory := func() []domain.ManagedSkillPackage {
		out := make([]domain.ManagedSkillPackage, 0, len(catalog.Packages))
		for _, p := range catalog.Packages {
			out = append(out, p)
		}
		slices.SortFunc(out, func(a, b domain.ManagedSkillPackage) int { return strings.Compare(string(a.ID), string(b.ID)) })
		return out
	}
	result = domain.ManagedSkillResult{WorkerDeviceID: q.WorkerDeviceID, RequestID: q.RequestID, State: domain.MCPSucceeded, Packages: inventory()}
	if q.Mutation.Action == domain.ManagedSkillList {
		return result, nil
	}
	if q.Mutation.Action == domain.ManagedSkillInspect {
		prior, exists := catalog.Receipts[q.RequestID]
		if !exists {
			if len(catalog.Receipts) >= 4096 {
				return result, bound()
			}
			result.State = domain.MCPFailed
			result.Problem = domain.Fail(domain.NotFound, "The original skill request was not delivered.", "Its identity is now reserved; do not replay it.")
			retained := result
			retained.Packages = nil
			catalog.Receipts[q.RequestID] = managedReceipt{ActorID: q.ActorID, Result: retained}
			if err := save(); err != nil {
				return result, err
			}
			return result, nil
		}
		if prior.ActorID != q.ActorID {
			return result, domain.Fail(domain.PermissionDenied, "The skill request belongs to another client.", "Use its original authorized client.")
		}
		result = prior.Result
		result.Packages = inventory()
		if result.State == domain.MCPPending {
			result.State = domain.MCPRecoveryRequired
		}
		return result, nil
	}
	current, exists := catalog.Packages[q.Mutation.PackageID]
	if q.Mutation.Action == domain.ManagedSkillView {
		if !exists || current.Revision != q.Mutation.ExpectedRevision {
			return result, domain.Fail(domain.Conflict, "The selected package revision changed.", "Refresh the owning Runner catalog.")
		}
		files, digest, err := packageFiles(ctx, filepath.Join(root, "packages", string(current.ID)))
		if err != nil || digest != current.ContentRevision {
			return result, unavailable()
		}
		result.Package = &current
		result.SkillMarkdown = string(files["SKILL.md"].Bytes)
		for name, f := range files {
			result.Manifest = append(result.Manifest, domain.ManagedSkillResource{Path: filepath.ToSlash(name), Bytes: len(f.Bytes), Digest: hex.EncodeToString(hash(f.Bytes)), Executable: f.Executable})
		}
		slices.SortFunc(result.Manifest, func(a, b domain.ManagedSkillResource) int { return strings.Compare(a.Path, b.Path) })
		manifestJSON, _ := json.Marshal(result.Manifest)
		if len(manifestJSON) > 1<<20 {
			return result, bound()
		}
		return result, nil
	}
	raw, _ = json.Marshal(q.Mutation)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if prior, ok := catalog.Receipts[q.RequestID]; ok {
		if prior.ActorID != q.ActorID || prior.Digest != digest {
			return result, domain.Fail(domain.Conflict, "The skill request identity has different input.", "Inspect the original request.")
		}
		result = prior.Result
		result.Packages = inventory()
		if result.State == domain.MCPPending {
			result.State = domain.MCPRecoveryRequired
		}
		return result, nil
	}
	if len(catalog.Receipts) >= 4096 {
		return result, bound()
	}
	if exists && current.Revision != q.Mutation.ExpectedRevision || !exists && q.Mutation.Action != domain.ManagedSkillImport || exists && q.Mutation.Action == domain.ManagedSkillImport {
		return result, domain.Fail(domain.Conflict, "The skill package revision changed.", "Read the owning catalog before a new explicit mutation.")
	}
	result.State = domain.MCPPending
	receipt := managedReceipt{ActorID: q.ActorID, Digest: digest, Result: result}
	receipt.Result.Packages = nil
	catalog.Receipts[q.RequestID] = receipt
	if err := save(); err != nil {
		return result, err
	}
	finish := func(failure error) (domain.ManagedSkillResult, error) {
		result.State = domain.MCPSucceeded
		if failure != nil {
			result.State = domain.MCPFailed
			result.Problem = domain.SafeError(failure)
		}
		result.Packages = inventory()
		receipt.Result = result
		receipt.Result.Packages = nil
		catalog.Receipts[q.RequestID] = receipt
		if err := save(); err != nil {
			return result, domain.Fail(domain.RecoveryRequired, "The skill request outcome is unconfirmed.", "Inspect its original identity.")
		}
		return result, nil
	}
	switch q.Mutation.Action {
	case domain.ManagedSkillImport:
		if len(catalog.Packages) >= MaxInventory || !filepath.IsAbs(q.Mutation.SourceDirectory) {
			return finish(bound())
		}
		sourceInfo, err := os.Lstat(q.Mutation.SourceDirectory)
		if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
			return finish(unavailable())
		}
		files, content, err := packageFiles(ctx, q.Mutation.SourceDirectory)
		if err != nil {
			return finish(err)
		}
		if !utf8.Valid(files["SKILL.md"].Bytes) {
			return finish(unavailable())
		}
		name, description, err := metadata(files["SKILL.md"].Bytes)
		if err != nil {
			return finish(err)
		}
		next := domain.ManagedSkillPackage{ID: q.Mutation.PackageID, WorkerDeviceID: q.WorkerDeviceID, Revision: 1, ContentRevision: content, Name: name, Description: description, Provenance: "worker-import", Enabled: true, Support: []domain.MCPHarnessSupport{{Harness: domain.Codex, Profile: "codex-extra-roots-v1", Verified: false}}}
		if next.Validate() != nil || slices.ContainsFunc(inventory(), func(p domain.ManagedSkillPackage) bool { return strings.EqualFold(p.Name, name) }) {
			return finish(domain.Fail(domain.Conflict, "The native skill name is invalid or ambiguous.", "Choose a package with a unique native name."))
		}
		projected, _ := json.Marshal(append(inventory(), next))
		if len(projected) > 900<<10 {
			return finish(bound())
		}
		packages := filepath.Join(root, "packages")
		if err := security.PrivateDir(packages); err != nil {
			return finish(err)
		}
		target := filepath.Join(packages, string(next.ID))
		temporary := filepath.Join(packages, "pending-"+string(q.RequestID))
		if err := writePackage(ctx, files, content, temporary); err != nil {
			cleanup := os.RemoveAll(temporary)
			if cleanup != nil {
				return result, domain.Fail(domain.RecoveryRequired, "The incomplete package requires owned cleanup.", "Preserve the original request.")
			}
			return finish(err)
		}
		_, after, err := packageFiles(ctx, q.Mutation.SourceDirectory)
		afterInfo, statErr := os.Lstat(q.Mutation.SourceDirectory)
		if err != nil || after != content || statErr != nil || !os.SameFile(sourceInfo, afterInfo) {
			if err := os.RemoveAll(temporary); err != nil {
				return result, domain.ManagedSkillUnavailable()
			}
			return finish(domain.Fail(domain.Conflict, "The source package changed while copying.", "Select the source again after it becomes stable."))
		}
		// The retained content copy is an independent immutable generation owner.
		// Catalog deletion removes only packages/<id>; accepted configurations still
		// resolve their exact digest under retained/<id>, never the source directory.
		retained := filepath.Join(root, "retained")
		if err := security.PrivateDir(retained); err != nil {
			return finish(err)
		}
		if err := writePackage(ctx, files, content, filepath.Join(retained, string(next.ID))); err != nil {
			return result, domain.Fail(domain.RecoveryRequired, "The retained package copy is incomplete.", "Inspect the original request before cleanup.")
		}
		if err := os.Rename(temporary, target); err != nil {
			return result, domain.ManagedSkillUnavailable()
		}
		catalog.Packages[next.ID] = next
	case domain.ManagedSkillEnable:
		current.Enabled = *q.Mutation.Enabled
		current.Revision++
		catalog.Packages[current.ID] = current
	case domain.ManagedSkillDelete:
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := os.RemoveAll(filepath.Join(root, "packages", string(current.ID))); err != nil {
			return result, domain.Fail(domain.RecoveryRequired, "Catalog package cleanup is unconfirmed.", "Inspect the original deletion request.")
		}
		delete(catalog.Packages, current.ID)
	default:
		return finish(domain.ManagedSkillUnavailable())
	}
	return finish(nil)
}

// ManagedRuntime creates an independent package copy for an immutable execution.
// It resolves the retained package digest, not the current enabled/catalog state.
func (m Manager) ManagedRuntime(ctx context.Context, server, worker domain.ID, packages []domain.ManagedSkillPackage, runtime string) ([]NativeSelection, error) {
	if server.Validate() != nil || worker.Validate() != nil || len(packages) > domain.MaxSelectedSkills {
		return nil, unavailable()
	}
	out := []NativeSelection{}
	names := map[string]bool{}
	total := 0
	for _, p := range packages {
		if p.Validate() != nil || p.WorkerDeviceID != worker || names[strings.ToLower(p.Name)] {
			return nil, unavailable()
		}
		names[strings.ToLower(p.Name)] = true
		files, digest, err := packageFiles(ctx, filepath.Join(managedRoot(m.Root, server, worker), "retained", string(p.ID)))
		if err != nil || digest != p.ContentRevision {
			return nil, unavailable()
		}
		for _, resource := range files {
			total += len(resource.Bytes)
			if total > MaxTotalBytes {
				return nil, bound()
			}
		}
		target := filepath.Join(runtime, "managed-skills", string(p.ID))
		if err := security.PrivateDir(filepath.Dir(target)); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			if err := writePackage(ctx, files, digest, target); err != nil {
				return nil, err
			}
		} else {
			_, actual, err := packageFiles(ctx, target)
			if err != nil || actual != digest {
				return nil, unavailable()
			}
		}
		out = append(out, NativeSelection{Name: p.Name, Path: filepath.Join(target, "SKILL.md"), ContentRevision: p.ContentRevision})
	}
	return out, nil
}

func VerifyManagedRuntime(ctx context.Context, home string, selected []NativeSelection) error {
	names := map[string]bool{}
	for _, v := range selected {
		relative, err := filepath.Rel(filepath.Join(home, "managed-skills"), v.Path)
		parts := strings.Split(relative, string(filepath.Separator))
		if err != nil || len(parts) != 2 || domain.ID(parts[0]).Validate() != nil || parts[1] != "SKILL.md" || names[strings.ToLower(v.Name)] {
			return unavailable()
		}
		files, digest, err := packageFiles(ctx, filepath.Dir(v.Path))
		if err != nil || digest != v.ContentRevision {
			return unavailable()
		}
		name, _, err := metadata(files["SKILL.md"].Bytes)
		if err != nil || name != v.Name {
			return unavailable()
		}
		names[strings.ToLower(name)] = true
	}
	return nil
}
