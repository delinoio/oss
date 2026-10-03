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
	"slices"
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

type storageRemovalRename struct {
	Original string `json:"original"`
	Private  string `json:"private"`
	Renamed  bool   `json:"renamed"`
}

type storageRemovalRenameRecord struct {
	Original string `json:"original"`
	Private  string `json:"private"`
	State    string `json:"state"`
	Digest   string `json:"digest,omitempty"`
}

const (
	storageRemovalRenamePrepared     = "prepared"
	storageRemovalRenameRenamed      = "renamed"
	storageRemovalRenameCleared      = "cleared"
	storageRemovalRenameClearedProof = "cleared-proof"
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
		if !validRemovalRelativePath(rename.Original) || !validRemovalRelativePath(rename.Private) || rename.Original == rename.Private || seenOriginal[rename.Original] || seenPrivate[rename.Private] {
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

func removalRootPrivatePath(root string, operationID domain.ID) string {
	return filepath.Join(root, "workspace-removals", ".removing-root-"+string(operationID))
}

func (m *Manager) appendRemovalClaimRecord(ctx context.Context, r StorageRequest, record storageRemovalRenameRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validRemovalClaimRecord(record) {
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
	if statErr == nil && (!info.Mode().IsRegular() || info.Size() < 0 || info.Size()+int64(len(raw)) > maxStorageRemovalClaim) {
		return ResultUncertain()
	}
	if statErr != nil && int64(len(raw)) > maxStorageRemovalClaim {
		return ResultUncertain()
	}
	return security.AppendPrivate(path, raw)
}

func validRemovalClaimRecord(record storageRemovalRenameRecord) bool {
	if record.State == storageRemovalRenameClearedProof {
		if record.Original != "" || record.Private != "" || len(record.Digest) != sha256.Size*2 {
			return false
		}
		_, err := hex.DecodeString(record.Digest)
		return err == nil
	}
	return validRemovalRelativePath(record.Original) && validRemovalRelativePath(record.Private) && record.Original != record.Private && record.Digest == "" && (record.State == storageRemovalRenamePrepared || record.State == storageRemovalRenameRenamed || record.State == storageRemovalRenameCleared)
}

func removalClearedDigest(original string) string {
	digest := sha256.Sum256([]byte(original))
	return hex.EncodeToString(digest[:])
}

// Rewrite the journal from the still-active mappings after a successful clear.
// This bounds durable progress by the current recovery frontier instead of the
// total number of entries already removed. WriteAtomic leaves the previous
// journal intact if publication fails, so the just-appended clear remains
// recoverable through the old complete history.
func (m *Manager) compactRemovalClaim(ctx context.Context, r StorageRequest, pending []storageRemovalRename) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	journalPath := m.removalClaimJournalPath(r.OperationID)
	oldJournal, err := security.ReadPrivate(journalPath, maxStorageRemovalClaim)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ResultUncertain()
	}
	if len(oldJournal) < maxStorageRemovalClaim/2 {
		return nil
	}
	cleared := map[string]struct{}{}
	text := strings.TrimSuffix(string(oldJournal), "\n")
	if text != "" {
		for _, line := range strings.Split(text, "\n") {
			var record storageRemovalRenameRecord
			if domain.Decode([]byte(line), &record) != nil || !validRemovalClaimRecord(record) {
				return ResultUncertain()
			}
			switch record.State {
			case storageRemovalRenameCleared:
				cleared[removalClearedDigest(record.Original)] = struct{}{}
			case storageRemovalRenameClearedProof:
				cleared[record.Digest] = struct{}{}
			}
		}
	}
	active := make([]storageRemovalRename, 0, len(pending))
	seenOriginal := map[string]bool{}
	seenPrivate := map[string]bool{}
	for _, rename := range pending {
		if rename.Original == "" && rename.Private == "" && !rename.Renamed {
			continue
		}
		if !validRemovalRelativePath(rename.Original) || !validRemovalRelativePath(rename.Private) || rename.Original == rename.Private || seenOriginal[rename.Original] || seenPrivate[rename.Private] {
			return ResultUncertain()
		}
		seenOriginal[rename.Original] = true
		seenPrivate[rename.Private] = true
		active = append(active, rename)
	}
	slices.SortFunc(active, func(a, b storageRemovalRename) int {
		if a.Original < b.Original {
			return -1
		}
		if a.Original > b.Original {
			return 1
		}
		return strings.Compare(a.Private, b.Private)
	})
	journal := make([]byte, 0, len(active)*128)
	for _, rename := range active {
		for _, state := range []string{storageRemovalRenamePrepared, storageRemovalRenameRenamed} {
			if state == storageRemovalRenameRenamed && !rename.Renamed {
				break
			}
			raw, err := json.Marshal(storageRemovalRenameRecord{Original: rename.Original, Private: rename.Private, State: state})
			if err != nil {
				return ResultUncertain()
			}
			journal = append(journal, raw...)
			journal = append(journal, '\n')
			if len(journal) > maxStorageRemovalClaim {
				return ResultUncertain()
			}
		}
	}
	digests := make([]string, 0, len(cleared))
	for digest := range cleared {
		digests = append(digests, digest)
	}
	slices.Sort(digests)
	for _, digest := range digests {
		raw, err := json.Marshal(storageRemovalRenameRecord{State: storageRemovalRenameClearedProof, Digest: digest})
		if err != nil {
			return ResultUncertain()
		}
		journal = append(journal, raw...)
		journal = append(journal, '\n')
		if len(journal) > maxStorageRemovalClaim {
			return ResultUncertain()
		}
	}
	return security.WriteAtomic(journalPath, journal)
}

func (m *Manager) readRemovalClaimPending(r StorageRequest, intent []byte) (storageRemovalClaim, []storageRemovalRename, map[string]struct{}, error) {
	claimRaw, err := security.ReadPrivate(m.removalClaimPath(r.OperationID), maxStorageRemovalClaim)
	var claim storageRemovalClaim
	if err != nil || !removalClaimMatches(claimRaw, removalReference(r), intent) || domain.Decode(claimRaw, &claim) != nil {
		return storageRemovalClaim{}, nil, nil, ResultUncertain()
	}
	active := make(map[string]storageRemovalRename, len(claim.Pending))
	for _, rename := range claim.Pending {
		active[rename.Original] = rename
	}
	raw, err := security.ReadPrivate(m.removalClaimJournalPath(r.OperationID), maxStorageRemovalClaim)
	if errors.Is(err, os.ErrNotExist) {
		return claim, append([]storageRemovalRename(nil), claim.Pending...), map[string]struct{}{}, nil
	}
	if err != nil {
		return storageRemovalClaim{}, nil, nil, ResultUncertain()
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lastNewline := bytes.LastIndexByte(raw, '\n')
		prefix := raw[:lastNewline+1]
		tail := raw[lastNewline+1:]
		var record storageRemovalRenameRecord
		switch {
		case !json.Valid(tail):
			// AppendPrivate may leave a short final JSON fragment after a
			// filesystem write error. Preserve every complete prior record and
			// discard only that incomplete suffix before retrying recovery.
			if err := security.WriteAtomic(m.removalClaimJournalPath(r.OperationID), prefix); err != nil {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			raw = prefix
		case json.Unmarshal(tail, &record) != nil:
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		case !validRemovalClaimRecord(record):
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		default:
			repaired := append(append([]byte(nil), raw...), '\n')
			if err := security.WriteAtomic(m.removalClaimJournalPath(r.OperationID), repaired); err != nil {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			raw = repaired
		}
	}
	text := strings.TrimSuffix(string(raw), "\n")
	cleared := map[string]struct{}{}
	if text == "" {
		return claim, append([]storageRemovalRename(nil), claim.Pending...), cleared, nil
	}
	for _, line := range strings.Split(text, "\n") {
		var record storageRemovalRenameRecord
		if domain.Decode([]byte(line), &record) != nil || !validRemovalClaimRecord(record) {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		current, exists := active[record.Original]
		switch record.State {
		case storageRemovalRenamePrepared:
			if exists || record.Private == record.Original {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			active[record.Original] = storageRemovalRename{Original: record.Original, Private: record.Private}
		case storageRemovalRenameRenamed:
			if !exists || current.Private != record.Private || current.Renamed {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			current.Renamed = true
			active[record.Original] = current
		case storageRemovalRenameCleared:
			if !exists || current.Private != record.Private {
				return storageRemovalClaim{}, nil, nil, ResultUncertain()
			}
			delete(active, record.Original)
			cleared[removalClearedDigest(record.Original)] = struct{}{}
		case storageRemovalRenameClearedProof:
			cleared[record.Digest] = struct{}{}
		default:
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
		if len(active) > maxSnapshotRemovalEntries {
			return storageRemovalClaim{}, nil, nil, ResultUncertain()
		}
	}
	pending := make([]storageRemovalRename, 0, len(active))
	for _, rename := range active {
		pending = append(pending, rename)
	}
	return claim, pending, cleared, nil
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
	return security.WriteAtomic(path, raw)
}

// A crash can occur after the claimed root has moved to its private final
// name and before the empty directory is unlinked. Reattach only that exact
// root identity to the original operation name; a foreign private directory
// remains protected behind recovery-required ownership.
func (m *Manager) restorePrivateRemovalRoot(ctx context.Context, r StorageRequest, removal string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	privateRoot := removalRootPrivatePath(m.Root, r.OperationID)
	exists, err := storageExists(privateRoot)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if present, err := storageExists(removal); err != nil {
		return false, err
	} else if present {
		return false, ResultUncertain()
	}
	intentRaw, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	if err != nil {
		return false, ResultUncertain()
	}
	claimRaw, err := security.ReadPrivate(m.removalClaimPath(r.OperationID), maxStorageRemovalClaim)
	if err != nil || !removalClaimMatches(claimRaw, removalReference(r), intentRaw) {
		return false, ResultUncertain()
	}
	var claim storageRemovalClaim
	if domain.Decode(claimRaw, &claim) != nil {
		return false, ResultUncertain()
	}
	identity, err := directoryPathIdentity(privateRoot)
	if err != nil || identity != claim.RootIdentity {
		return false, ResultUncertain()
	}
	if err := renameStorage(privateRoot, removal); err != nil {
		return false, ResultUncertain()
	}
	return true, nil
}

func (m *Manager) removalIntentPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-intents", string(id)+".json")
}
func (m *Manager) retainRemovalIntent(ctx context.Context, r StorageRequest, path string, expectedDigest ...string) error {
	var inventory snapshotInventory
	var snapshotDigest string
	if r.Action == StorageCleanup {
		pinned, digest, err := m.cleanupRemovalInventory(r)
		if err != nil {
			return err
		}
		if len(expectedDigest) > 0 && digest != expectedDigest[0] {
			return ResultUncertain()
		}
		inventory, snapshotDigest = pinned, digest
	} else if r.Action == StorageDelete {
		var err error
		inventory, err = snapshotRemovalInventory(ctx, r, path)
		if err != nil {
			return err
		}
		snapshotDigest = r.SnapshotDigest
	} else {
		return ResultUncertain()
	}
	intent := storageRemovalIntent{Version: 1, OperationID: r.OperationID, SessionID: r.Preparation.SessionID, SnapshotID: r.SnapshotID, Action: r.Action, Inventory: inventory, SnapshotDigest: snapshotDigest}
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
	return security.WriteAtomic(m.removalIntentPath(r.OperationID), raw)
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
	var cleared map[string]struct{}
	if partial {
		_, pending, cleared, err = m.readRemovalClaimPending(r, raw)
		if err != nil {
			return ResultUncertain()
		}
		exists, err := storageExists(path)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
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
			seen[logicalPath] = true
			if os.FileMode(old.Mode).IsDir() && os.FileMode(entry.Mode).IsDir() {
				continue
			}
			if !reflect.DeepEqual(old, entry) {
				return ResultUncertain()
			}
		}
		for i, rename := range mappings {
			originalPresent, privatePresent := false, false
			for _, entry := range current.Entries {
				if entry.Path == rename.Original {
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
				if _, proven := cleared[removalClearedDigest(rename.Original)]; proven {
					// A previously persisted pre-unlink proof makes a later
					// reappearance foreign; it cannot be treated as a stale
					// mapping whose rename never happened.
					return ResultUncertain()
				}
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
		if err := m.compactRemovalClaim(ctx, r, mappings); err != nil {
			return err
		}
	}
	return m.retainRemovalClaim(ctx, r, raw)
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
	if original.Action == StorageCleanup || original.Action == StorageDelete {
		restored, err := m.restorePrivateRemovalRoot(ctx, original, removal)
		if err != nil {
			return result, err
		}
		removed = removed || restored
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
			result.Snapshot = &metadata
		}
	}
	switch original.Action {
	case StoragePreview, StorageCreate, StorageCleanup:
		if live {
			manifest, err := m.Read(r.Preparation.SessionID)
			if err != nil || manifestDigest(manifest) != manifestDigest(r.Manifest) {
				return result, ResultUncertain()
			}
			if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, manifest, continuationIdentity); err != nil {
				return result, err
			}
			observation, err := m.storageObservation(ctx, original)
			if err != nil {
				return result, err
			}
			result.SourceBytes, result.PreviewDigest = observation.Whole.Bytes, observation.Digest
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
				current, err := walkSnapshot(ctx, staging, "", nil)
				if err != nil || inventoryDigest(current) != inventoryDigest(snapshot.Inventory) {
					return result, ResultUncertain()
				}
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
			metadata = *original.SnapshotMetadata
			metadata.Deleted = true
			result.Snapshot = &metadata
			result.RecoveredJobState = domain.JobSucceeded
		}
	default:
		return result, ResultUncertain()
	}
	retained, err := m.snapshotBytes(ctx, r.Preparation.SessionID)
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

// RemoveClaimedStorageRemoval removes a still-present removal namespace only
// through its original intent and version-2 claim. Permanent session deletion
// must not pass this namespace to the generic tree remover, which cannot bind
// replacement bytes to the captured inventory.
func (m *Manager) RemoveClaimedStorageRemoval(ctx context.Context, operationID, sessionID, snapshotID domain.ID) error {
	if operationID.Validate() != nil || sessionID.Validate() != nil || snapshotID.Validate() != nil {
		return ResultUncertain()
	}
	removal := filepath.Join(m.Root, "workspace-removals", string(operationID))
	exists, err := storageExists(removal)
	if err != nil {
		return err
	}
	privateExists, err := storageExists(removalRootPrivatePath(m.Root, operationID))
	if err != nil {
		return err
	}
	if !exists && !privateExists {
		return nil
	}
	raw, err := security.ReadPrivate(m.removalIntentPath(operationID), maxSnapshotManifest)
	var intent storageRemovalIntent
	if err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != operationID || intent.SessionID != sessionID || intent.SnapshotID != snapshotID || (intent.Action != StorageCleanup && intent.Action != StorageDelete) {
		return ResultUncertain()
	}
	request := StorageRequest{
		Version:     1,
		OperationID: operationID,
		Action:      intent.Action,
		Preparation: PrepareRequest{SessionID: sessionID},
		SnapshotID:  snapshotID,
	}
	if !exists {
		if _, err := m.restorePrivateRemovalRoot(ctx, request, removal); err != nil {
			return err
		}
	}
	return m.removeClaimedSnapshotTree(ctx, request, removal, true)
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
func snapshotRemovalInventory(ctx context.Context, r StorageRequest, path string) (snapshotInventory, error) {
	raw, err := security.ReadPrivate(filepath.Join(path, "snapshot.json"), maxSnapshotManifest)
	var pinned snapshotManifest
	sum := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != r.SnapshotDigest || domain.DecodeBounded(raw, &pinned, maxSnapshotManifest) != nil || pinned.ID != r.SnapshotID || manifestDigest(pinned.Workspace) != manifestDigest(r.Manifest) {
		return snapshotInventory{}, ResultUncertain()
	}
	inventory, err := walkSnapshotEntries(ctx, path, "", nil, maxSnapshotRemovalEntries)
	if err != nil {
		return snapshotInventory{}, err
	}
	var contents snapshotInventory
	wrappers := 0
	for _, entry := range inventory.Entries {
		switch {
		case entry.Path == "snapshot.json":
			if !os.FileMode(entry.Mode).IsRegular() || entry.SHA256 != r.SnapshotDigest {
				return snapshotInventory{}, ResultUncertain()
			}
			wrappers++
		case entry.Path == "workspace":
			if !os.FileMode(entry.Mode).IsDir() {
				return snapshotInventory{}, ResultUncertain()
			}
			wrappers++
		case strings.HasPrefix(entry.Path, "workspace/"):
			entry.Path = strings.TrimPrefix(entry.Path, "workspace/")
			contents.Entries = append(contents.Entries, entry)
			contents.Bytes += entry.Size
		default:
			return snapshotInventory{}, ResultUncertain()
		}
	}
	if wrappers != 2 || len(contents.Entries) > MaxSnapshotEntries || inventoryDigest(contents) != inventoryDigest(pinned.Inventory) {
		return snapshotInventory{}, ResultUncertain()
	}
	return inventory, nil
}
