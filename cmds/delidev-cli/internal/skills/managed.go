// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type managedRecord struct {
	Package      domain.ManagedSkillPackage `json:"package"`
	ServerID     domain.ID                  `json:"server_id"`
	RootIdentity string                     `json:"root_identity"`
}
type managedReceipt struct {
	Request       domain.ManagedSkillRequest `json:"request"`
	Digest        string                     `json:"digest"`
	Record        managedRecord              `json:"record"`
	Complete      bool                       `json:"complete"`
	Removing      bool                       `json:"removing"`
	RemovalClaims []string                   `json:"removal_claims,omitempty"`
}

func (m Manager) managedCatalog() string { return filepath.Join(m.Root, "managed-skill-catalog") }
func (m Manager) managedPath(id domain.ID) string {
	return filepath.Join(m.Root, "managed-skill-packages", string(id))
}
func (m Manager) managedRecord(id domain.ID) (managedRecord, error) {
	var value managedRecord
	raw, e := readBounded(filepath.Join(m.managedCatalog(), string(id)+".json"), 256<<10)
	if e != nil {
		return value, e
	}
	if domain.Decode(raw, &value) != nil || value.Package.PackageID != id || value.ServerID.Validate() != nil {
		return value, unavailable()
	}
	return value, nil
}

// Canonical private modes are included in the complete owned-copy digest. Source
// modes are separately rechecked before publication; legacy explicit digests stay
// unchanged. This detects every permission change to an owned resource.
func managedFiles(ctx context.Context, path string) (map[string]packageFile, []domain.ManagedSkillResource, string, string, error) {
	return managedResources(ctx, path, true)
}
func managedResources(ctx context.Context, path string, requireSkill bool) (map[string]packageFile, []domain.ManagedSkillResource, string, string, error) {
	files, legacy, e := packageResources(ctx, path, requireSkill)
	if e != nil {
		return nil, nil, "", "", e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, nil, "", "", e
	}
	defer root.Close()
	manifest := make([]domain.ManagedSkillResource, 0, len(files))
	for name, v := range files {
		if !utf8.ValidString(name) || !fs.ValidPath(filepath.ToSlash(name)) {
			return nil, nil, "", "", unavailable()
		}
		info, e := root.Lstat(name)
		if e != nil || !info.Mode().IsRegular() {
			return nil, nil, "", "", unavailable()
		}
		manifest = append(manifest, domain.ManagedSkillResource{Path: filepath.ToSlash(name), Size: int64(len(v.Bytes)), Mode: uint32(info.Mode().Perm()), Digest: hex.EncodeToString(hash(v.Bytes))})
	}
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	raw, _ := json.Marshal(manifest)
	digest := sha256.Sum256(raw)
	return files, manifest, hex.EncodeToString(digest[:]), legacy, nil
}
func managedConflict() error {
	return domain.Fail(domain.Conflict, "The managed skill changed.", "Refresh the package and retry with its current revision.")
}
func managedUncertain() error {
	return domain.Fail(domain.RecoveryRequired, "The original skill operation requires recovery.", "Retain its original request; do not import or delete another package as recovery.")
}
func managedRequestDigest(v domain.ManagedSkillRequest) string {
	v.WorkerInstanceID = ""
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (m Manager) verifyManaged(ctx context.Context, r managedRecord) error {
	identity, e := directoryIdentity(m.managedPath(r.Package.PackageID))
	if e != nil || identity != r.RootIdentity {
		return managedUncertain()
	}
	_, _, digest, legacy, e := managedFiles(ctx, m.managedPath(r.Package.PackageID))
	if e != nil || digest != r.Package.ContentRevision || legacy != r.Package.LegacyRevision {
		return managedUncertain()
	}
	return nil
}
func (m Manager) Managed(ctx context.Context, request domain.ManagedSkillRequest) (domain.ManagedSkillResult, error) {
	out := domain.ManagedSkillResult{Packages: []domain.ManagedSkillPackage{}}
	if request.ServerID.Validate() != nil || request.ActorID.Validate() != nil || request.WorkerDeviceID.Validate() != nil || request.WorkerInstanceID.Validate() != nil || request.MachineID.Validate() != nil || request.RequestID.Validate() != nil {
		return out, unavailable()
	}
	if e := security.PrivateDir(m.managedCatalog()); e != nil {
		return out, e
	}
	lock, e := security.TryLock(filepath.Join(m.managedCatalog(), "catalog.lock"))
	if e != nil || lock == nil {
		return out, domain.Fail(domain.Unavailable, "Skill management is busy.", "Retry the original request.")
	}
	defer lock.Close()
	if request.Action == domain.ManagedSkillCancelImport {
		return m.cancelManagedImport(ctx, request)
	}
	if request.Action == domain.ManagedSkillList {
		entries, e := boundedDirectory(m.managedCatalog(), MaxInventory+1)
		if e != nil {
			return out, e
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			id := domain.ID(entry.Name()[:len(entry.Name())-5])
			r, e := m.managedRecord(id)
			if e != nil {
				return out, e
			}
			if r.ServerID == request.ServerID && r.Package.WorkerDeviceID == request.WorkerDeviceID && r.Package.MachineID == request.MachineID && !r.Package.Deleted {
				p := r.Package
				p.Resources = nil
				out.Packages = append(out.Packages, p)
			}
		}
		return out, nil
	}
	if request.PackageID.Validate() != nil {
		return out, unavailable()
	}
	if request.Action == domain.ManagedSkillView {
		r, e := m.managedRecord(request.PackageID)
		if e != nil || r.ServerID != request.ServerID || r.Package.WorkerDeviceID != request.WorkerDeviceID || r.Package.MachineID != request.MachineID || r.Package.Deleted {
			return out, unavailable()
		}
		if e = m.verifyManaged(ctx, r); e != nil {
			return out, e
		}
		raw, e := readBounded(filepath.Join(m.managedPath(request.PackageID), "SKILL.md"), 1<<20)
		if e != nil || !utf8.Valid(raw) {
			return out, unavailable()
		}
		out.Packages = append(out.Packages, r.Package)
		out.SkillMarkdown = string(raw)
		return out, nil
	}
	if request.Action != domain.ManagedSkillImport && request.Action != domain.ManagedSkillEnable && request.Action != domain.ManagedSkillDelete {
		return out, unavailable()
	}
	receiptPath := filepath.Join(m.Root, "managed-skill-receipts", string(request.RequestID)+".json")
	var receipt managedReceipt
	raw, err := readBounded(receiptPath, 512<<10)
	digest := managedRequestDigest(request)
	if err == nil {
		if domain.Decode(raw, &receipt) != nil || receipt.Removing && request.Action == domain.ManagedSkillImport || receipt.Digest != digest {
			return out, managedConflict()
		}
		if receipt.Complete {
			if receipt.Record.Package.Deleted {
				if _, e := os.Lstat(m.managedPath(request.PackageID)); !errors.Is(e, os.ErrNotExist) {
					return out, managedUncertain()
				}
				if e = m.retireManagedCatalog(receipt.Record); e != nil {
					return out, e
				}
			}
			if !receipt.Record.Package.Deleted {
				out.Packages = append(out.Packages, receipt.Record.Package)
			}
			return out, nil
		}
		// An interrupted import never repeats source reads or copies. Only a complete
		// original root with its full recorded manifest can finish publication.
		if request.Action == domain.ManagedSkillImport && receipt.Record.RootIdentity != "" && m.verifyManaged(ctx, receipt.Record) == nil {
			if e = writeJSON(filepath.Join(m.managedCatalog(), string(request.PackageID)+".json"), receipt.Record); e != nil {
				return out, e
			}
			receipt.Complete = true
			if e = writeJSON(receiptPath, receipt); e != nil {
				return out, e
			}
			out.Packages = append(out.Packages, receipt.Record.Package)
			return out, nil
		}
		if request.Action != domain.ManagedSkillDelete || !receipt.Removing {
			return out, managedUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, managedUncertain()
	} else {
		receipt = managedReceipt{Request: request, Digest: digest}
		if request.Action == domain.ManagedSkillImport {
			if request.ExpectedRevision != 0 || !filepath.IsAbs(request.SourceDirectory) {
				return out, unavailable()
			}
			if _, e = m.managedRecord(request.PackageID); !errors.Is(e, os.ErrNotExist) {
				return out, managedConflict()
			}
			sourceIdentity, e := directoryIdentity(request.SourceDirectory)
			if e != nil {
				return out, e
			}
			sources, sourceManifest, sourceDigest, sourceLegacy, e := managedFiles(ctx, request.SourceDirectory)
			if e != nil {
				return out, e
			}
			name, description, e := metadata(sources["SKILL.md"].Bytes)
			if e != nil {
				return out, e
			}
			current, e := boundedDirectory(m.managedCatalog(), MaxInventory+1)
			if e != nil || len(current) > MaxInventory {
				return out, bound()
			}
			for _, entry := range current {
				if filepath.Ext(entry.Name()) != ".json" {
					continue
				}
				r, e := m.managedRecord(domain.ID(entry.Name()[:len(entry.Name())-5]))
				if e != nil {
					return out, e
				}
				if !r.Package.Deleted && r.ServerID == request.ServerID && r.Package.Name == name {
					return out, managedConflict()
				}
			}
			receipt.Record = managedRecord{ServerID: request.ServerID, Package: domain.ManagedSkillPackage{PackageID: request.PackageID, WorkerDeviceID: request.WorkerDeviceID, MachineID: request.MachineID, Revision: 1, Name: name, Description: description, Enabled: true, LegacyRevision: sourceLegacy}}
			// Claim the immutable complete intended manifest before any owned bytes.
			for _, resource := range sourceManifest {
				if resource.Mode&0111 != 0 {
					resource.Mode = 0700
				} else {
					resource.Mode = 0600
				}
				receipt.Record.Package.Resources = append(receipt.Record.Package.Resources, resource)
			}
			expectedRaw, _ := json.Marshal(receipt.Record.Package.Resources)
			h := sha256.Sum256(expectedRaw)
			receipt.Record.Package.ContentRevision = hex.EncodeToString(h[:])
			if e = writeJSON(receiptPath, receipt); e != nil {
				return out, e
			}
			if e = security.PrivateDir(filepath.Dir(m.managedPath(request.PackageID))); e != nil {
				return out, e
			}
			if e = os.Mkdir(m.managedPath(request.PackageID), 0700); e != nil {
				return out, managedUncertain()
			}
			receipt.Record.RootIdentity, e = directoryIdentity(m.managedPath(request.PackageID))
			if e != nil {
				return out, e
			}
			if e = writeJSON(receiptPath, receipt); e != nil {
				return out, e
			}
			for path, data := range sources {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				target := filepath.Join(m.managedPath(request.PackageID), path)
				if e = security.PrivateDir(filepath.Dir(target)); e != nil {
					return out, e
				}
				if e = security.WriteAtomicOwnedMode(target, data.Bytes, data.Executable); e != nil {
					return out, e
				}
			}
			_, _, afterSource, _, e := managedFiles(ctx, request.SourceDirectory)
			afterIdentity, identityError := directoryIdentity(request.SourceDirectory)
			if e != nil || identityError != nil || afterIdentity != sourceIdentity || afterSource != sourceDigest {
				return out, managedUncertain()
			}
			_, manifest, full, legacy, e := managedFiles(ctx, m.managedPath(request.PackageID))
			if e != nil || full != receipt.Record.Package.ContentRevision {
				return out, managedUncertain()
			}
			receipt.Record.Package.Resources = manifest
			receipt.Record.Package.LegacyRevision = legacy
			if e = writeJSON(receiptPath, receipt); e != nil {
				return out, e
			}
		} else {
			r, e := m.managedRecord(request.PackageID)
			if e != nil || r.ServerID != request.ServerID || r.Package.WorkerDeviceID != request.WorkerDeviceID || r.Package.MachineID != request.MachineID || r.Package.Deleted || r.Package.Revision != request.ExpectedRevision {
				return out, managedConflict()
			}
			if e = m.verifyManaged(ctx, r); e != nil {
				return out, e
			}
			receipt.Record = r
			if request.Action == domain.ManagedSkillEnable {
				receipt.Record.Package.Enabled = request.Enabled
				receipt.Record.Package.Revision++
			} else {
				receipt.Removing = true
			}
			if e = writeJSON(receiptPath, receipt); e != nil {
				return out, e
			}
		}
	}
	if request.Action == domain.ManagedSkillDelete {
		if e = m.removeManaged(ctx, &receipt, receiptPath); e != nil {
			return out, e
		}
		receipt.Record.Package.Deleted = true
		receipt.Record.Package.Enabled = false
		receipt.Record.Package.Revision++
	}
	if e = writeJSON(filepath.Join(m.managedCatalog(), string(request.PackageID)+".json"), receipt.Record); e != nil {
		return out, e
	}
	receipt.Complete = true
	if e = writeJSON(receiptPath, receipt); e != nil {
		return out, e
	}
	if receipt.Record.Package.Deleted {
		if e = m.retireManagedCatalog(receipt.Record); e != nil {
			return out, e
		}
	}
	if !receipt.Record.Package.Deleted {
		out.Packages = append(out.Packages, receipt.Record.Package)
	}
	return out, nil
}

// Persist each original unlink claim before removal. Reconnection can prove a
// claimed missing resource, but cannot adopt a changed root or unlisted entry.
func (m Manager) removeManaged(ctx context.Context, receipt *managedReceipt, journal string) error {
	target := m.managedPath(receipt.Record.Package.PackageID)
	claimed := map[string]bool{}
	for _, name := range receipt.RemovalClaims {
		claimed[name] = true
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		if len(claimed) != len(receipt.Record.Package.Resources) {
			return managedUncertain()
		}
		return nil
	}
	identity, e := directoryIdentity(target)
	if e != nil || identity != receipt.Record.RootIdentity {
		return managedUncertain()
	}
	_, actual, _, _, e := managedResources(ctx, target, false)
	if e != nil {
		return managedUncertain()
	}
	expected := map[string]domain.ManagedSkillResource{}
	dirs := map[string]bool{}
	for _, r := range receipt.Record.Package.Resources {
		expected[r.Path] = r
		for d := filepath.Dir(filepath.FromSlash(r.Path)); d != "."; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	present := map[string]bool{}
	for _, r := range actual {
		original, ok := expected[r.Path]
		if !ok || original != r {
			return managedUncertain()
		}
		present[r.Path] = true
	}
	for name := range expected {
		if !present[name] && !claimed[name] {
			return managedUncertain()
		}
	}
	root, e := os.OpenRoot(target)
	if e != nil {
		return managedUncertain()
	}
	defer root.Close()
	// Enumerate directories too: extra empty directories are not deletion authority.
	var walk func(string) error
	walk = func(d string) error {
		f, e := root.Open(d)
		if e != nil {
			return e
		}
		entries, e := f.ReadDir(MaxFiles + 1)
		f.Close()
		if e != nil && !errors.Is(e, io.EOF) || len(entries) > MaxFiles {
			return managedUncertain()
		}
		for _, entry := range entries {
			if entry.IsDir() {
				name := filepath.Join(d, entry.Name())
				if !dirs[name] {
					return managedUncertain()
				}
				if e := walk(name); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e = walk("."); e != nil {
		return managedUncertain()
	}
	for _, r := range receipt.Record.Package.Resources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !claimed[r.Path] {
			receipt.RemovalClaims = append(receipt.RemovalClaims, r.Path)
			if e = writeJSON(journal, receipt); e != nil {
				return e
			}
			claimed[r.Path] = true
		}
		if present[r.Path] {
			if e = root.Remove(filepath.FromSlash(r.Path)); e != nil {
				return managedUncertain()
			}
		}
	}
	ordered := []string{}
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i], string(filepath.Separator)) > strings.Count(ordered[j], string(filepath.Separator))
	})
	for _, d := range ordered {
		if e = root.Remove(d); e != nil && !errors.Is(e, os.ErrNotExist) {
			return managedUncertain()
		}
	}
	identity, e = directoryIdentity(target)
	if e != nil || identity != receipt.Record.RootIdentity {
		return managedUncertain()
	}
	if e = os.Remove(target); e != nil {
		return managedUncertain()
	}
	return security.SyncParent(target)
}

type ManagedRuntimeRecord struct {
	Version  uint32                `json:"version"`
	Bindings []domain.SkillBinding `json:"bindings"`
}

func ManagedRuntimeBindings(home string) ([]domain.SkillBinding, error) {
	raw, e := security.ReadPrivate(filepath.Join(home, "managed-skill-bindings.json"), 64<<10)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	var saved ManagedRuntimeRecord
	if e != nil || domain.Decode(raw, &saved) != nil || saved.Version != 1 || domain.ValidateSkills(saved.Bindings) != nil {
		return nil, unavailable()
	}
	for _, b := range saved.Bindings {
		if b.ManagedRevision == "" {
			return nil, unavailable()
		}
	}
	return saved.Bindings, nil
}

// Each native runtime and independent Fork owns a complete package. A retained
// original proof lets that runtime continue after the catalog/parent is deleted.
func (m Manager) ManagedRuntime(ctx context.Context, bindings []domain.SkillBinding, home string) ([]NativeSelection, error) {
	if domain.ValidateSkills(bindings) != nil {
		return nil, unavailable()
	}
	out := []NativeSelection{}
	for _, binding := range bindings {
		path := filepath.Join(home, "selected-skills", string(binding.SkillID), "SKILL.md")
		raw, e := security.ReadPrivate(filepath.Join(home, "selected-skill-proofs", string(binding.SkillID)+".json"), 4096)
		if errors.Is(e, os.ErrNotExist) {
			if _, e = m.CopyToRuntime(ctx, []domain.SkillBinding{binding}, home); e != nil {
				return nil, e
			}
		} else {
			var saved domain.SkillBinding
			if e != nil || domain.Decode(raw, &saved) != nil || saved != binding {
				return nil, unavailable()
			}
		}
		if e = VerifyRuntimePackage(ctx, home, path); e != nil {
			return nil, e
		}
		bytes, e := readBounded(path, 1<<20)
		if e != nil {
			return nil, e
		}
		name, _, e := metadata(bytes)
		if e != nil {
			return nil, e
		}
		out = append(out, NativeSelection{Name: name, Path: path})
	}
	if e := writeJSON(filepath.Join(home, "managed-skill-bindings.json"), ManagedRuntimeRecord{Version: 1, Bindings: bindings}); e != nil {
		return nil, e
	}
	return out, nil
}
func CloneManagedRuntime(ctx context.Context, sourceHome, childHome string) ([]NativeSelection, error) {
	bindings, e := ManagedRuntimeBindings(sourceHome)
	if e != nil {
		return nil, e
	}
	result := []NativeSelection{}
	total := 0
	for _, b := range bindings {
		source := filepath.Join(sourceHome, "selected-skills", string(b.SkillID), "SKILL.md")
		target, size, e := CloneRuntimePackage(ctx, sourceHome, source, childHome)
		if e != nil {
			return nil, e
		}
		total += size
		if total > MaxTotalBytes {
			return nil, bound()
		}
		raw, e := readBounded(target, 1<<20)
		if e != nil {
			return nil, e
		}
		name, _, e := metadata(raw)
		if e != nil {
			return nil, e
		}
		result = append(result, NativeSelection{Name: name, Path: target})
	}
	if len(bindings) > 0 {
		if e = writeJSON(filepath.Join(childHome, "managed-skill-bindings.json"), ManagedRuntimeRecord{Version: 1, Bindings: bindings}); e != nil {
			return nil, e
		}
	}
	return result, nil
}

// Explicit cancellation fences the original import before joined original-byte
// cleanup. A successful import cannot be canceled as a substitute for deletion.
func (m Manager) cancelManagedImport(ctx context.Context, request domain.ManagedSkillRequest) (domain.ManagedSkillResult, error) {
	out := domain.ManagedSkillResult{Packages: []domain.ManagedSkillPackage{}}
	if request.PackageID.Validate() != nil {
		return out, unavailable()
	}
	journal := filepath.Join(m.Root, "managed-skill-receipts", string(request.PackageID)+".json")
	raw, e := readBounded(journal, 512<<10)
	var receipt managedReceipt
	if errors.Is(e, os.ErrNotExist) {
		if _, e = os.Lstat(m.managedPath(request.PackageID)); !errors.Is(e, os.ErrNotExist) {
			return out, managedUncertain()
		}
		// No original bytes were claimed. Retain a cancellation fence against a
		// delayed import with this original package/request identity.
		receipt = managedReceipt{Request: request, Record: managedRecord{ServerID: request.ServerID, Package: domain.ManagedSkillPackage{PackageID: request.PackageID, MachineID: request.MachineID, WorkerDeviceID: request.WorkerDeviceID, Deleted: true}}, Removing: true, Complete: true}
		return out, writeJSON(journal, receipt)
	}
	if e != nil || domain.Decode(raw, &receipt) != nil || receipt.Request.ServerID != request.ServerID || receipt.Request.ActorID != request.ActorID || receipt.Request.MachineID != request.MachineID || receipt.Request.WorkerDeviceID != request.WorkerDeviceID {
		return out, managedUncertain()
	}
	if receipt.Complete {
		if receipt.Record.Package.Deleted {
			if _, e = os.Lstat(m.managedPath(request.PackageID)); errors.Is(e, os.ErrNotExist) {
				return out, m.retireManagedCatalog(receipt.Record)
			}
			return out, managedUncertain()
		}
		out.Packages = append(out.Packages, receipt.Record.Package)
		return out, nil
	}
	receipt.Removing = true
	// Import publication has not succeeded. The original root and complete
	// intended manifest authorize removing only original matching partial bytes.
	for _, r := range receipt.Record.Package.Resources {
		if !slices.Contains(receipt.RemovalClaims, r.Path) {
			receipt.RemovalClaims = append(receipt.RemovalClaims, r.Path)
		}
	}
	if e = writeJSON(journal, receipt); e != nil {
		return out, e
	}
	if receipt.Record.RootIdentity == "" {
		if _, e = os.Lstat(m.managedPath(request.PackageID)); !errors.Is(e, os.ErrNotExist) {
			return out, managedUncertain()
		}
	} else if e = m.removeManaged(ctx, &receipt, journal); e != nil {
		return out, e
	}
	receipt.Complete = true
	receipt.Record.Package.Deleted = true
	receipt.Record.Package.Enabled = false
	if e = m.retireManagedCatalog(receipt.Record); e != nil {
		return out, e
	}
	if e = writeJSON(journal, receipt); e != nil {
		return out, e
	}
	return out, nil
}

// Catalog capacity counts live packages only. Durable exact-request receipts
// remain separate; retiring metadata cannot adopt or remove replacement bytes.
func (m Manager) retireManagedCatalog(original managedRecord) error {
	record, e := m.managedRecord(original.Package.PackageID)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !original.Package.Deleted || record.RootIdentity != original.RootIdentity || record.ServerID != original.ServerID || record.Package.ContentRevision != original.Package.ContentRevision || record.Package.WorkerDeviceID != original.Package.WorkerDeviceID {
		return managedUncertain()
	}
	path := filepath.Join(m.managedCatalog(), string(original.Package.PackageID)+".json")
	if e = os.Remove(path); e != nil {
		return e
	}
	return security.SyncParent(path)
}
