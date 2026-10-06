// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type storageRemovalIntent struct {
	Version        uint32            `json:"version"`
	OperationID    domain.ID         `json:"operation_id"`
	SessionID      domain.ID         `json:"session_id"`
	SnapshotID     domain.ID         `json:"snapshot_id"`
	Action         StorageAction     `json:"action"`
	SnapshotDigest string            `json:"snapshot_digest,omitempty"`
	Inventory      snapshotInventory `json:"inventory"`
	SourceBytes    *uint64           `json:"source_bytes,omitempty"`
}

// Persist this only after the private namespace and its original inventory have
// been verified, before any unlink. An intent alone precedes native ownership.
type storageRemovalClaim struct {
	RootIdentity string                  `json:"root_identity"`
	Version      uint32                  `json:"version"`
	Reference    StorageRemovalReference `json:"reference"`
	IntentDigest string                  `json:"intent_digest"`
	Pending      []storageRemovalRename  `json:"pending,omitempty"`
}

// A recursive source directory can retain one mapping per open directory while
// its children are being removed. Keep the claim within the private manifest
// bound so recovery can inspect it without trusting an unbounded journal.
const maxStorageRemovalClaim = maxSnapshotManifest

// Journal records repeat two encoded paths for each transition. The immutable
// manifest bounds the original path inventory; the larger journal ceiling holds
// its active prepared/renamed projection plus the bounded header baseline.
// Settled history is compacted atomically before admitting another record.
const maxStorageRemovalJournal = 8 * maxSnapshotManifest

type storageRemovalRename struct {
	Original     string `json:"original"`
	Private      string `json:"private"`
	Renamed      bool   `json:"renamed"`
	ModePrepared bool   `json:"mode_prepared,omitempty"`
}

type storageRemovalRenameRecord struct {
	Original string `json:"original"`
	Private  string `json:"private,omitempty"`
	State    string `json:"state"`
}

const (
	storageRemovalRenamePrepared        = "prepared"
	storageRemovalRenameRenamed         = "renamed"
	storageRemovalRenameCleared         = "cleared"
	storageRemovalRenameRemoved         = "removed"
	storageRemovalRemovedProof          = "removed-proof"
	storageRemovalDirectoryModePrepared = "directory-mode-prepared"
)

func (m *Manager) removalClaimPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-claims", string(id)+".json")
}

func removalReference(r StorageRequest) StorageRemovalReference {
	return StorageRemovalReference{OperationID: r.OperationID, SessionID: r.Preparation.SessionID, SnapshotID: r.SnapshotID, Action: r.Action}
}

func removalClaimMatches(raw []byte, ref StorageRemovalReference, intent []byte) bool {
	var claim storageRemovalClaim
	sum := sha256.Sum256(intent)
	if domain.Decode(raw, &claim) != nil || claim.Version != 2 || claim.RootIdentity == "" || claim.Reference != ref || claim.IntentDigest != hex.EncodeToString(sum[:]) || len(claim.Pending) > maxSnapshotRemovalEntries {
		return false
	}
	seenOriginal := map[string]bool{}
	seenPrivate := map[string]bool{}
	for _, rename := range claim.Pending {
		if !validRemovalRelativePath(rename.Original) || !validRemovalRelativePath(rename.Private) || rename.Original == rename.Private || rename.ModePrepared && !rename.Renamed || seenOriginal[rename.Original] || seenPrivate[rename.Private] {
			return false
		}
		seenOriginal[rename.Original] = true
		seenPrivate[rename.Private] = true
	}
	return true
}

func validRemovalRelativePath(value string) bool {
	return value != "" && value != "." && pathpkg.Clean(value) == value && !pathpkg.IsAbs(value) && !filepath.IsAbs(filepath.FromSlash(value)) && value != ".." && !strings.HasPrefix(value, "../") && len(value) <= 4096
}

func (m *Manager) removalClaimJournalPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-claims", string(id)+".pending")
}

func (m *Manager) appendRemovalClaimRecord(ctx context.Context, r StorageRequest, record storageRemovalRenameRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validRemovalRelativePath(record.Original) || !validRemovalRelativePath(record.Private) || record.Original == record.Private || (record.State != storageRemovalRenamePrepared && record.State != storageRemovalRenameRenamed && record.State != storageRemovalRenameCleared && record.State != storageRemovalRenameRemoved && record.State != storageRemovalDirectoryModePrepared) {
		return ResultUncertain()
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return ResultUncertain()
	}
	raw = append(raw, '\n')
	path := m.removalClaimJournalPath(r.OperationID)
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return ResultUncertain()
	}
	if statErr == nil {
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxStorageRemovalJournal {
			return ResultUncertain()
		}
		if info.Size()+int64(len(raw)) > maxStorageRemovalJournal {
			if err := m.compactRemovalClaimJournal(ctx, r); err != nil {
				return err
			}
			info, err = os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size()+int64(len(raw)) > maxStorageRemovalJournal {
				return ResultUncertain()
			}
		}
	}
	if int64(len(raw)) > maxStorageRemovalJournal {
		return ResultUncertain()
	}
	return security.AppendPrivate(path, raw)
}

// Replace only the append journal, never the immutable claim. Atomic publication
// makes either the full old replay or the equivalent compact replay recoverable.
func (m *Manager) compactRemovalClaimJournal(ctx context.Context, r StorageRequest) error {
	intent, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	if err != nil {
		return ResultUncertain()
	}
	claim, pending, removed, err := m.readRemovalClaimState(r, intent)
	if err != nil {
		return err
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Original < pending[j].Original })
	var raw []byte
	appendRecord := func(record storageRemovalRenameRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		part, err := json.Marshal(record)
		if err != nil || len(raw)+len(part)+1 > maxStorageRemovalJournal {
			return ResultUncertain()
		}
		raw = append(raw, part...)
		raw = append(raw, '\n')
		return nil
	}
	// Retain compatibility with a bounded nonempty original header baseline:
	// clear it in the replay before reconstructing the current active claims.
	for _, prior := range claim.Pending {
		if err := appendRecord(storageRemovalRenameRecord{Original: prior.Original, Private: prior.Private, State: storageRemovalRenameCleared}); err != nil {
			return err
		}
	}
	settled := make([]storageRemovalRename, 0, len(removed))
	for _, rename := range removed {
		settled = append(settled, rename)
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].Original < settled[j].Original })
	for _, rename := range settled {
		// Settled absence needs only the original inventory operand. Generated
		// private prefixes can grow much longer than original paths; repeating
		// them for every removed file would strand an otherwise bounded intent.
		if err := appendRecord(storageRemovalRenameRecord{Original: rename.Original, State: storageRemovalRemovedProof}); err != nil {
			return err
		}
	}
	for _, active := range pending {
		if err := appendRecord(storageRemovalRenameRecord{Original: active.Original, Private: active.Private, State: storageRemovalRenamePrepared}); err != nil {
			return err
		}
		if active.Renamed {
			if err := appendRecord(storageRemovalRenameRecord{Original: active.Original, Private: active.Private, State: storageRemovalRenameRenamed}); err != nil {
				return err
			}
		}
		if active.ModePrepared {
			if err := appendRecord(storageRemovalRenameRecord{Original: active.Original, Private: active.Private, State: storageRemovalDirectoryModePrepared}); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := security.WriteAtomicOwned(m.removalClaimJournalPath(r.OperationID), raw); err != nil {
		return err
	}
	m.Logger.InfoContext(ctx, "workspace_removal_journal_compacted", "operation_id", r.OperationID, "active_claims", len(pending), "journal_bytes", len(raw))
	return nil
}

func (m *Manager) readRemovalClaimPending(r StorageRequest, intent []byte) (storageRemovalClaim, []storageRemovalRename, error) {
	claim, pending, _, err := m.readRemovalClaimState(r, intent)
	return claim, pending, err
}

func (m *Manager) readRemovalClaimState(r StorageRequest, intent []byte) (storageRemovalClaim, []storageRemovalRename, map[string]storageRemovalRename, error) {
	claimRaw, err := security.ReadPrivate(m.removalClaimPath(r.OperationID), maxStorageRemovalClaim)
	var claim storageRemovalClaim
	if err != nil || !removalClaimMatches(claimRaw, removalReference(r), intent) || domain.Decode(claimRaw, &claim) != nil {
		return storageRemovalClaim{}, nil, nil, ResultUncertain()
	}
	var original storageRemovalIntent
	if domain.DecodeBounded(intent, &original, maxSnapshotManifest) != nil || len(original.Inventory.Entries) > maxSnapshotRemovalEntries {
		return storageRemovalClaim{}, nil, nil, ResultUncertain()
	}
	inventory := map[string]bool{}
	for _, entry := range original.Inventory.Entries {
		if !validRemovalRelativePath(entry.Path) || inventory[entry.Path] {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		inventory[entry.Path] = true
	}
	removed := map[string]storageRemovalRename{}
	active := make(map[string]storageRemovalRename, len(claim.Pending))
	for _, rename := range claim.Pending {
		active[rename.Original] = rename
	}
	raw, err := security.ReadPrivate(m.removalClaimJournalPath(r.OperationID), maxStorageRemovalJournal)
	if errors.Is(err, os.ErrNotExist) {
		return claim, append([]storageRemovalRename(nil), claim.Pending...), removed, nil
	}
	if err != nil {
		return storageRemovalClaim{}, nil, nil, ResultUncertain()
	}
	// A durable append is newline-framed. A torn final write grants no effect
	// authority; validate every complete record before atomically repairing it.
	complete := raw
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		complete = raw[:bytes.LastIndexByte(raw, '\n')+1]
	}
	text := strings.TrimSuffix(string(complete), "\n")
	lines := []string{}
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	for _, line := range lines {
		var record storageRemovalRenameRecord
		if domain.Decode([]byte(line), &record) != nil || !validRemovalRelativePath(record.Original) || (record.State != storageRemovalRemovedProof && (!validRemovalRelativePath(record.Private) || record.Original == record.Private)) {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		current, exists := active[record.Original]
		switch record.State {
		case storageRemovalRemovedProof:
			if exists || removed[record.Original].Original != "" || record.Private != "" || !inventory[record.Original] {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			removed[record.Original] = storageRemovalRename{Original: record.Original, Renamed: true}
		case storageRemovalRenamePrepared:
			if exists || removed[record.Original].Original != "" || record.Private == record.Original {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			active[record.Original] = storageRemovalRename{Original: record.Original, Private: record.Private}
		case storageRemovalRenameRenamed:
			if !exists || current.Private != record.Private || current.Renamed {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			current.Renamed = true
			active[record.Original] = current
		case storageRemovalDirectoryModePrepared:
			if !exists || current.Private != record.Private || !current.Renamed || current.ModePrepared {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			current.ModePrepared = true
			active[record.Original] = current
		case storageRemovalRenameRemoved:
			if !exists || current.Private != record.Private || !current.Renamed {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			removed[record.Original] = current
			delete(active, record.Original)
		case storageRemovalRenameCleared:
			if !exists || current.Private != record.Private {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			delete(active, record.Original)
		default:
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		if len(active)+len(removed) > maxSnapshotRemovalEntries {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
	}
	if len(complete) != len(raw) {
		if err := security.WriteAtomicOwned(m.removalClaimJournalPath(r.OperationID), complete); err != nil {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		m.Logger.Info("workspace_removal_journal_tail_repaired", "operation_id", r.OperationID, "discarded_bytes", len(raw)-len(complete))
	}
	pending := make([]storageRemovalRename, 0, len(active))
	for _, rename := range active {
		pending = append(pending, rename)
	}
	return claim, pending, removed, nil
}

func (m *Manager) retainRemovalClaim(ctx context.Context, r StorageRequest, intent []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := m.removalClaimPath(r.OperationID)
	if raw, err := security.ReadPrivate(path, maxStorageRemovalClaim); err == nil {
		if !removalClaimMatches(raw, removalReference(r), intent) {
			return ResultUncertain()
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	identity, err := directoryPathIdentity(filepath.Join(m.Root, "workspace-removals", string(r.OperationID)))
	if err != nil {
		return ResultUncertain()
	}
	sum := sha256.Sum256(intent)
	raw, err := json.Marshal(storageRemovalClaim{Version: 2, RootIdentity: identity, Reference: removalReference(r), IntentDigest: hex.EncodeToString(sum[:])})
	if err != nil || len(raw) > maxStorageRemovalClaim {
		return ResultUncertain()
	}
	return security.WriteAtomicOwned(path, raw)
}

func (m *Manager) removalIntentPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-intents", string(id)+".json")
}
func (m *Manager) retainRemovalIntent(ctx context.Context, r StorageRequest, path string, expectedDigest ...string) error {
	var inventory snapshotInventory
	var snapshotDigest string
	var sourceBytes uint64
	if r.Action == StorageCleanup {
		pinned, digest, err := m.cleanupRemovalInventory(r)
		if err != nil {
			return err
		}
		if len(expectedDigest) > 0 && digest != expectedDigest[0] {
			return ResultUncertain()
		}
		inventory, snapshotDigest, sourceBytes = pinned, digest, pinned.Bytes
	} else if r.Action == StorageDelete {
		var err error
		inventory, sourceBytes, err = snapshotRemovalInventory(ctx, r, path)
		if err != nil {
			return err
		}
		snapshotDigest = r.SnapshotDigest
	} else {
		return ResultUncertain()
	}
	intent := storageRemovalIntent{Version: 1, OperationID: r.OperationID, SessionID: r.Preparation.SessionID, SnapshotID: r.SnapshotID, Action: r.Action, Inventory: inventory, SnapshotDigest: snapshotDigest, SourceBytes: &sourceBytes}
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > maxSnapshotManifest {
		return ResultUncertain()
	}
	if old, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest); err == nil {
		if string(old) != string(raw) {
			return ResultUncertain()
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	return security.WriteAtomicOwned(m.removalIntentPath(r.OperationID), raw)
}
func (m *Manager) confirmRemoval(ctx context.Context, r StorageRequest, path string, partial bool) error {
	if path != filepath.Join(m.Root, "workspace-removals", string(r.OperationID)) {
		return ResultUncertain()
	}
	raw, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	if err != nil {
		return ResultUncertain()
	}
	var intent storageRemovalIntent
	if domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != r.OperationID || intent.SessionID != r.Preparation.SessionID || intent.SnapshotID != r.SnapshotID || intent.Action != r.Action {
		return ResultUncertain()
	}
	if r.Action == StorageCleanup {
		pinned, digest, err := m.cleanupRemovalInventory(r)
		if err != nil || digest != intent.SnapshotDigest || inventoryDigest(pinned) != inventoryDigest(intent.Inventory) {
			return ResultUncertain()
		}
	}
	if r.Action == StorageDelete && intent.SnapshotDigest != r.SnapshotDigest {
		return ResultUncertain()
	}
	var pending []storageRemovalRename
	var removed map[string]storageRemovalRename
	if partial {
		_, pending, removed, err = m.readRemovalClaimState(r, raw)
		if err != nil {
			// A crash may occur after the top-level rename but before its first
			// claim publication. Only an intact namespace matching the complete
			// synchronized intent can establish that original claim. Missing
			// names or any existing malformed claim/journal retain uncertainty.
			_, claimErr := security.ReadPrivate(m.removalClaimPath(r.OperationID), maxStorageRemovalClaim)
			_, journalErr := security.ReadPrivate(m.removalClaimJournalPath(r.OperationID), maxStorageRemovalJournal)
			if !errors.Is(claimErr, os.ErrNotExist) || !errors.Is(journalErr, os.ErrNotExist) {
				return ResultUncertain()
			}
			exists, err := storageExists(path)
			if err != nil || !exists {
				return ResultUncertain()
			}
			partial = false
		}
		exists, err := storageExists(path)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
	if err := m.verifyRemovalInventory(ctx, r, path, partial, intent, pending, removed); err != nil {
		return err
	}
	return m.retainRemovalClaim(ctx, r, raw)
}

// Compare remaining entries and replay only original journal transitions. This
// check grants no new claim, so permanent deletion can require preexisting proof.
func (m *Manager) verifyRemovalInventory(ctx context.Context, r StorageRequest, path string, partial bool, intent storageRemovalIntent, pending []storageRemovalRename, removed map[string]storageRemovalRename) error {
	entryLimit := MaxSnapshotEntries
	if r.Action == StorageDelete {
		entryLimit = maxSnapshotRemovalEntries
	}
	current, err := walkSnapshotEntries(ctx, path, "", nil, entryLimit)
	if err != nil {
		return err
	}
	if !partial && inventoryDigest(current) != inventoryDigest(intent.Inventory) {
		return ResultUncertain()
	}
	if partial {
		expected := map[string]snapshotEntry{}
		for _, entry := range intent.Inventory.Entries {
			expected[entry.Path] = entry
		}
		mappings := append([]storageRemovalRename(nil), pending...)
		// Prefer the deepest private prefix so nested claimed directories map
		// back to their original logical path before the inventory comparison.
		mappingFor := func(physical string) (string, bool) {
			best := ""
			logical := ""
			for _, rename := range mappings {
				if physical != rename.Private && !strings.HasPrefix(physical, rename.Private+"/") {
					continue
				}
				if len(rename.Private) > len(best) {
					best = rename.Private
					logical = rename.Original + strings.TrimPrefix(physical, rename.Private)
				}
			}
			return logical, best != ""
		}
		seen := map[string]bool{}
		for _, entry := range current.Entries {
			logicalPath, mapped := mappingFor(entry.Path)
			if !mapped {
				logicalPath = entry.Path
			}
			entry.Path = logicalPath
			old, ok := expected[logicalPath]
			if !ok {
				return ResultUncertain()
			}
			if seen[logicalPath] {
				return ResultUncertain()
			}
			if _, settled := removed[logicalPath]; settled {
				return ResultUncertain()
			}
			seen[logicalPath] = true
			if os.FileMode(old.Mode).IsDir() && os.FileMode(entry.Mode).IsDir() {
				modePrepared := false
				for _, rename := range mappings {
					modePrepared = modePrepared || rename.Original == logicalPath && rename.ModePrepared
				}
				if !removalDirectoryModeMatches(os.FileMode(entry.Mode), old.Mode, modePrepared) {
					return ResultUncertain()
				}
				continue
			}
			if !reflect.DeepEqual(old, entry) {
				return ResultUncertain()
			}
		}
		for logical := range expected {
			if seen[logical] {
				continue
			}
			_, proven := removed[logical]
			for _, rename := range mappings {
				proven = proven || rename.Original == logical && rename.Renamed
			}
			if !proven {
				return ResultUncertain()
			}
		}
		for i, rename := range mappings {
			originalPresent, privatePresent := false, false
			for _, entry := range current.Entries {
				logical, mapped := mappingFor(entry.Path)
				if !mapped {
					logical = entry.Path
				}
				if logical == rename.Original && entry.Path != rename.Private {
					originalPresent = true
				}
				if entry.Path == rename.Private {
					privatePresent = true
				}
			}
			switch {
			case privatePresent:
				if !rename.Renamed {
					if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: rename.Original, Private: rename.Private, State: storageRemovalRenameRenamed}); err != nil {
						return err
					}
					mappings[i].Renamed = true
				}
			case originalPresent:
				// The claim was durable before the rename, but the rename did not
				// publish. Drop this stale mapping so recovery can retry the same
				// pinned entry without treating its original name as foreign.
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: rename.Original, Private: rename.Private, State: storageRemovalRenameCleared}); err != nil {
					return err
				}
				mappings[i] = storageRemovalRename{}
			case rename.Renamed:
				// The private entry was verified and may have been removed just
				// before the mapping-clear write. Keep that proof for retry.
			default:
				// Neither name proves whether the pre-rename claim completed.
				return ResultUncertain()
			}
		}
	}
	return nil
}
func storageExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, ResultUncertain()
	}
	return true, nil
}

// Explicit recovery inspects the original immutable operation and journal. It
// never repeats snapshot creation, source rename or restoration publication.
// Only already claimed, independently inventoried removals may be continued.
func (m *Manager) recoverStorage(ctx context.Context, r StorageRequest, result StorageResult) (StorageResult, error) {
	original := r.Recovery.Original
	result.RecoveredJobID = original.OperationID
	result.RecoveredJobState = domain.JobFailed
	result.WorkspaceState = original.PreviousState
	if result.WorkspaceState == "" {
		result.WorkspaceState = domain.WorkspacePresent
	}
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	removal := filepath.Join(m.Root, "workspace-removals", string(original.OperationID))
	staging := filepath.Join(m.Root, "snapshot-staging", string(original.OperationID))
	live, err := storageExists(root)
	if err != nil {
		return result, err
	}
	removed, err := storageExists(removal)
	if err != nil {
		return result, err
	}
	staged, err := storageExists(staging)
	if err != nil {
		return result, err
	}
	if original.Action == StorageCleanup && live && removed {
		return result, ResultUncertain()
	}
	snapshotExists := false
	var snapshot snapshotManifest
	var metadata SnapshotMetadata
	if original.SnapshotID != "" {
		snapshotExists, err = storageExists(m.snapshotPath(original.SnapshotID))
		if err != nil {
			return result, err
		}
		if snapshotExists {
			snapshot, metadata, err = m.inspectSnapshot(ctx, original.SnapshotID)
			if err != nil {
				return result, err
			}
			if metadata.SessionID != r.Preparation.SessionID || metadata.MachineID != r.Preparation.MachineID || manifestDigest(snapshot.Workspace) != manifestDigest(r.Manifest) || (original.SnapshotDigest != "" && metadata.SHA256 != original.SnapshotDigest) {
				return result, ResultUncertain()
			}
			if original.Action == StorageCreate || original.Action == StorageCleanup {
				claim, err := m.verifySnapshotPublication(snapshot, metadata.SHA256)
				if err != nil || snapshot.OperationID != original.OperationID || claim.Reference != removalReference(original) || claim.RequestDigest != storageRequestDigest(original) {
					return result, ResultUncertain()
				}
			}
			if original.Action == StorageCleanup && (snapshot.SourceDigest != original.PreviewDigest || !digestValid(snapshot.SourceDigest)) {
				return result, ResultUncertain()
			}
			result.Snapshot = &metadata
			result.SourceBytes = snapshot.SourceBytes
		}
	}
	switch original.Action {
	case StoragePreview, StorageCreate, StorageCleanup:
		if live {
			manifest, err := m.Read(r.Preparation.SessionID)
			if err != nil || manifestDigest(manifest) != manifestDigest(r.Manifest) {
				return result, ResultUncertain()
			}
			if snapshotExists && (original.Action == StorageCreate || original.Action == StorageCleanup) && snapshot.SourceDirectoryIdentity != "" {
				identity, err := sourceWorkspaceDirectoryIdentity(root, manifest)
				if err != nil || identity != snapshot.SourceDirectoryIdentity {
					return result, ResultUncertain()
				}
				// The original publication claim proves the completed copy. Later
				// mutable source eligibility cannot revoke that proof or authorize
				// another cleanup. A live cleanup source settles as preserved/failed.
				result.SourceBytes, result.PreviewDigest = snapshot.SourceBytes, snapshot.SourceDigest
			} else {
				// Pre-amendment snapshots retain their prior identity checks.
				if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, manifest, continuationIdentity); err != nil {
					return result, err
				}
				observation, err := m.storageObservation(ctx, original)
				if err != nil {
					return result, err
				}
				result.SourceBytes, result.PreviewDigest = observation.Whole.Bytes, observation.Digest
			}
			result.WorkspaceState = domain.WorkspacePresent
			if original.Action == StorageCreate && snapshotExists {
				result.RecoveredJobState = domain.JobSucceeded
			}
		} else {
			if original.Action != StorageCleanup || !snapshotExists {
				return result, ResultUncertain()
			}
			// Absence alone cannot prove this operation removed the source. The
			// original synchronized intent and verified claim remain mandatory after
			// the last unlink, including interruption before the namespace transition.
			if err := m.confirmRemoval(ctx, original, removal, true); err != nil {
				return result, err
			}
			if removed {
				if err := m.removeClaimedSnapshotTree(ctx, original, removal, true); err != nil {
					return result, ResultUncertain()
				}
			}
			if err := security.SyncParent(root); err != nil {
				return result, ResultUncertain()
			}
			if err := security.SyncParent(removal); err != nil {
				return result, ResultUncertain()
			}
			result.WorkspaceState = domain.WorkspaceStored
			result.RecoveredJobState = domain.JobSucceeded
			if snapshot.SourceDigest != original.PreviewDigest || !digestValid(snapshot.SourceDigest) {
				return result, ResultUncertain()
			}
			result.PreviewDigest = snapshot.SourceDigest
			result.SourceBytes = snapshot.SourceBytes
			result.RemovedSourceBytes = snapshot.SourceBytes
		}
		if staged {
			if err := m.cleanupStorageStaging(ctx, original); err != nil {
				return result, ResultUncertain()
			}
		}
	case StorageRestore:
		if !snapshotExists || removed {
			return result, ResultUncertain()
		}
		if live {
			if staged {
				return result, ResultUncertain()
			}
			raw, err := security.ReadPrivate(m.restoreBindingPath(r.Preparation.SessionID), 4096)
			var binding restoreBinding
			if err != nil || domain.Decode(raw, &binding) != nil || binding.Version != 2 || !binding.Published || !digestValid(binding.DirectoryIdentity) || binding.OperationID != original.OperationID || binding.SessionID != r.Preparation.SessionID || binding.SnapshotID != original.SnapshotID || binding.SnapshotDigest != metadata.SHA256 || binding.ManifestDigest != manifestDigest(snapshot.Workspace) || binding.OriginalIdentity != snapshot.OriginalIdentity {
				return result, ResultUncertain()
			}
			// The published binding and workspace identity prove ownership after
			// publication. Ordinary user edits and commits are valid recovery state;
			// the exact snapshot inventory was required only before publication.
			if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, r.Manifest, continuationIdentity); err != nil {
				return result, err
			}
			result.WorkspaceState = domain.WorkspacePresent
			result.RecoveredJobState = domain.JobSucceeded
		} else {
			if staged {
				// The external operation claim and native root identity own all
				// scratch, including an interrupted partial copy. Completed snapshot
				// equality is required for publication, not unpublished cleanup.
				if err := m.cleanupStorageStaging(ctx, original); err != nil {
					return result, ResultUncertain()
				}
			}
			result.WorkspaceState = domain.WorkspaceStored
		}
	case StorageInspect:
		if !snapshotExists {
			return result, ResultUncertain()
		}
		result.RecoveredJobState = domain.JobSucceeded
	case StorageDelete:
		if snapshotExists && removed {
			return result, ResultUncertain()
		}
		if !snapshotExists {
			if original.SnapshotMetadata == nil || original.SnapshotMetadata.ID != original.SnapshotID || original.SnapshotMetadata.SHA256 != original.SnapshotDigest {
				return result, ResultUncertain()
			}
			// Absence alone cannot prove this operation removed the source. The
			// original synchronized intent and verified claim remain mandatory after
			// the last unlink, including interruption before the namespace transition.
			if err := m.confirmRemoval(ctx, original, removal, true); err != nil {
				return result, err
			}
			if removed {
				if err := m.removeClaimedSnapshotTree(ctx, original, removal, true); err != nil {
					return result, ResultUncertain()
				}
			}
			if err := security.SyncParent(removal); err != nil {
				return result, ResultUncertain()
			}
			if err := security.SyncParent(m.snapshotPath(original.SnapshotID)); err != nil {
				return result, ResultUncertain()
			}
			rawIntent, err := security.ReadPrivate(m.removalIntentPath(original.OperationID), maxSnapshotManifest)
			var intent storageRemovalIntent
			if err != nil || domain.DecodeBounded(rawIntent, &intent, maxSnapshotManifest) != nil || intent.SourceBytes == nil || *intent.SourceBytes > MaxSnapshotBytes {
				return result, ResultUncertain()
			}
			// confirmRemoval bound this original intent to the verified claim;
			// snapshot metadata size cannot reconstruct its logical source count.
			result.SourceBytes = *intent.SourceBytes
			metadata = *original.SnapshotMetadata
			metadata.Deleted = true
			result.Snapshot = &metadata
			result.RecoveredJobState = domain.JobSucceeded
		}
	default:
		return result, ResultUncertain()
	}
	retained, err := m.snapshotBytesLocked(ctx, r.Preparation.SessionID)
	if err != nil {
		return result, err
	}
	result.RetainedSnapshotBytes = retained
	result.CapacityBytes, result.FreeBytesAfter = storageCapacity(m.Root)
	result.CleanupVerified = true
	return result, nil
}

// StorageRemovalReference names only the immutable intent to retire after its
// matching server report and Worker reported journal are durable.
type StorageRemovalReference struct {
	OperationID domain.ID     `json:"operation_id"`
	SessionID   domain.ID     `json:"session_id"`
	SnapshotID  domain.ID     `json:"snapshot_id"`
	Action      StorageAction `json:"action"`
}

func (m *Manager) RetireStorageRemoval(ctx context.Context, ref StorageRemovalReference) error {
	if ref.OperationID.Validate() != nil || ref.SessionID.Validate() != nil || ref.SnapshotID.Validate() != nil || (ref.Action != StorageCleanup && ref.Action != StorageDelete) {
		return ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, claimPath, journalPath := m.removalIntentPath(ref.OperationID), m.removalClaimPath(ref.OperationID), m.removalClaimJournalPath(ref.OperationID)
	for _, artifact := range []string{path, claimPath, journalPath} {
		if err := security.PrivateDir(filepath.Dir(artifact)); err != nil {
			return err
		}
	}
	raw, err := security.ReadPrivate(path, maxSnapshotManifest)
	intentExists := err == nil
	if err == nil {
		var intent storageRemovalIntent
		if domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != ref.OperationID || intent.SessionID != ref.SessionID || intent.SnapshotID != ref.SnapshotID || intent.Action != ref.Action {
			return ResultUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	claim, err := security.ReadPrivate(claimPath, maxStorageRemovalClaim)
	if err == nil {
		if !intentExists || !removalClaimMatches(claim, ref, raw) {
			return ResultUncertain()
		}
		// Retire and synchronize the smaller proof first. Interrupted retirement
		// can safely retry with the acknowledged original intent still present.
		if err := os.Remove(claimPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	if err := security.SyncParent(claimPath); err != nil {
		return err
	}
	if err := os.Remove(journalPath); err == nil {
		if err := security.SyncParent(journalPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if intentExists {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return security.SyncParent(path)
}

// Source removal authority comes from the already verified published snapshot,
// never from a fresh inventory that could adopt uncaptured concurrent writes.
func (m *Manager) cleanupRemovalInventory(r StorageRequest) (snapshotInventory, string, error) {
	raw, err := security.ReadPrivate(filepath.Join(m.snapshotPath(r.SnapshotID), "snapshot.json"), maxSnapshotManifest)
	var pinned snapshotManifest
	if err != nil || domain.DecodeBounded(raw, &pinned, maxSnapshotManifest) != nil || pinned.Version != 1 || pinned.ID != r.SnapshotID || pinned.OperationID != r.OperationID || pinned.SourceDigest != r.PreviewDigest || manifestDigest(pinned.Workspace) != manifestDigest(r.Manifest) || pinned.SourceInventory.Bytes != pinned.SourceBytes || len(pinned.SourceInventory.Entries) == 0 || len(pinned.SourceInventory.Entries) > MaxSnapshotEntries || pinned.SourceBytes > MaxSnapshotBytes {
		return snapshotInventory{}, "", ResultUncertain()
	}
	sum := sha256.Sum256(raw)
	return pinned.SourceInventory, hex.EncodeToString(sum[:]), nil
}

// Wrapper entries have a separate bound. They do not enlarge the valid workspace
// inventory, and unexpected snapshot-root content never enters removal authority.
func snapshotRemovalInventory(ctx context.Context, r StorageRequest, path string) (snapshotInventory, uint64, error) {
	raw, err := security.ReadPrivate(filepath.Join(path, "snapshot.json"), maxSnapshotManifest)
	var pinned snapshotManifest
	sum := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != r.SnapshotDigest || domain.DecodeBounded(raw, &pinned, maxSnapshotManifest) != nil || pinned.ID != r.SnapshotID || manifestDigest(pinned.Workspace) != manifestDigest(r.Manifest) {
		return snapshotInventory{}, 0, ResultUncertain()
	}
	inventory, err := walkSnapshotEntries(ctx, path, "", nil, maxSnapshotRemovalEntries)
	if err != nil {
		return snapshotInventory{}, 0, err
	}
	var contents snapshotInventory
	wrappers := 0
	for _, entry := range inventory.Entries {
		switch {
		case entry.Path == "snapshot.json":
			if !os.FileMode(entry.Mode).IsRegular() || entry.SHA256 != r.SnapshotDigest {
				return snapshotInventory{}, 0, ResultUncertain()
			}
			wrappers++
		case entry.Path == "workspace":
			contents.RootMode = entry.Mode
			if !os.FileMode(entry.Mode).IsDir() {
				return snapshotInventory{}, 0, ResultUncertain()
			}
			wrappers++
		case strings.HasPrefix(entry.Path, "workspace/"):
			entry.Path = strings.TrimPrefix(entry.Path, "workspace/")
			contents.Entries = append(contents.Entries, entry)
			contents.Bytes += entry.Size
		default:
			return snapshotInventory{}, 0, ResultUncertain()
		}
	}
	if wrappers != 2 || len(contents.Entries) > MaxSnapshotEntries || inventoryDigest(contents) != inventoryDigest(pinned.Inventory) {
		return snapshotInventory{}, 0, ResultUncertain()
	}
	return inventory, pinned.SourceBytes, nil
}
