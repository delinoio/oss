// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Scratch cleanup may enumerate its own output. Claimed source removal instead
// uses only the immutable intent and checks each entry immediately before unlink.
// New entries are never selected; a nonempty directory cannot be removed by rmdir.
func (m *Manager) removeClaimedSnapshotTree(ctx context.Context, r StorageRequest, removal string, partial bool) (returned error) {
	stage := "intent"
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_claimed_removal_incomplete", "operation_id", r.OperationID, "action", r.Action, "stage", stage, "code", domain.SafeError(returned).Code)
		}
	}()
	if removal != filepath.Join(m.Root, "workspace-removals", string(r.OperationID)) {
		return ResultUncertain()
	}
	raw, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	var intent storageRemovalIntent
	if err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != r.OperationID ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(intent.SessionID), intent.SessionID != r.Preparation.SessionID) ||
		intent.SnapshotID != r.SnapshotID || intent.Action != r.Action || len(intent.Inventory.Entries) > maxSnapshotRemovalEntries {
		return ResultUncertain()
	}
	stage = "claim-journal"
	claim, pending, removed, err := m.readRemovalClaimState(r, raw)
	if err != nil {
		return ResultUncertain()
	}
	// A final-root transition has its own namespace and original proof. Never
	// restart child removal or adopt the old public name once it exists.
	if _, err := os.Lstat(m.finalRemovalClaimPath(r.OperationID)); err == nil {
		stage = "final-root-recovery"
		return m.finishFinalRootRemoval(ctx, r, claim)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	stage = "root-identity"
	identity, err := directoryPathIdentity(removal)
	if err != nil || identity != claim.RootIdentity {
		return ResultUncertain()
	}
	claimedRootInfo, claimedStatErr := os.Lstat(removal)
	if claimedStatErr != nil || claimedRootInfo.Mode() != removalWritableDirectoryMode() || intent.Inventory.RootMode != 0 && uint32(claimedRootInfo.Mode()) != intent.Inventory.RootMode {
		return ResultUncertain()
	}
	stage = "inventory"
	children := map[string][]snapshotEntry{}
	known := map[string]snapshotEntry{}
	for _, entry := range intent.Inventory.Entries {
		if entry.Path == "." || path.Clean(entry.Path) != entry.Path || path.IsAbs(entry.Path) || filepath.IsAbs(filepath.FromSlash(entry.Path)) || entry.Path == ".." || len(entry.Path) > 4096 {
			return ResultUncertain()
		}
		if _, duplicate := known[entry.Path]; duplicate {
			return ResultUncertain()
		}
		known[entry.Path] = entry
		parent := path.Dir(entry.Path)
		children[parent] = append(children[parent], entry)
	}
	for parent, entries := range children {
		if parent != "." && !os.FileMode(known[parent].Mode).IsDir() {
			return ResultUncertain()
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	}
	stage = "root-open"
	root, err := os.OpenRoot(removal)
	if err != nil {
		return ResultUncertain()
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return ResultUncertain()
	}
	stage = "opened-identity"
	openedIdentity, err := directoryFileIdentity(file)
	file.Close()
	if err != nil || openedIdentity != claim.RootIdentity {
		return ResultUncertain()
	}
	buffer := make([]byte, 128<<10)
	physicalFor := func(logical string) string {
		best := ""
		physical := logical
		for _, rename := range pending {
			if logical != rename.Original && !strings.HasPrefix(logical, rename.Original+"/") {
				continue
			}
			if len(rename.Original) > len(best) {
				best = rename.Original
				physical = rename.Private + strings.TrimPrefix(logical, rename.Original)
			}
		}
		return physical
	}
	logicalFor := func(physical string) (string, bool) {
		best := ""
		logical := ""
		for _, rename := range pending {
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
	removePending := func(original, private string) error {
		for i, rename := range pending {
			if rename.Original != original || rename.Private != private {
				continue
			}
			pending = append(pending[:i], pending[i+1:]...)
			return nil
		}
		return ResultUncertain()
	}
	var remove func(*os.Root, string) error
	remove = func(parent *os.Root, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stage = "directory-read"
		file, err := parent.Open(".")
		if err != nil {
			return err
		}
		names, readErr := file.Readdirnames(maxSnapshotRemovalEntries + 1)
		file.Close()
		if readErr != nil && readErr != io.EOF || len(names) > maxSnapshotRemovalEntries {
			return ResultUncertain()
		}
		physicalParent := physicalFor(relative)
		for _, name := range names {
			physicalEntry := path.Join(physicalParent, name)
			logicalEntry, mapped := logicalFor(physicalEntry)
			if !mapped {
				logicalEntry = physicalEntry
			}
			if _, ok := known[logicalEntry]; !ok {
				return ResultUncertain()
			}
		}
		for _, entry := range children[relative] {
			if err := ctx.Err(); err != nil {
				return err
			}
			physicalEntry := physicalFor(entry.Path)
			name := path.Base(physicalEntry)
			stage = "entry-inspection"
			before, err := parent.Lstat(name)
			if os.IsNotExist(err) && partial {
				_, proven := removed[entry.Path]
				for _, rename := range pending {
					proven = proven || rename.Original == entry.Path && rename.Renamed
				}
				if !proven {
					return ResultUncertain()
				}
				continue
			}
			if _, settled := removed[entry.Path]; settled {
				return ResultUncertain()
			}
			if err != nil {
				return ResultUncertain()
			}
			if m.storageBeforeRemovalUnlink != nil {
				m.storageBeforeRemovalUnlink(entry.Path)
			}
			// Move the observed entry out of its replaceable name before verifying
			// it. A writer retaining this directory can replace the original name
			// after this claim without making the cleanup unlink that replacement.
			privateName := name
			privateRelative := physicalEntry
			pendingIndex := -1
			for i, rename := range pending {
				if rename.Original == entry.Path && rename.Private == physicalEntry && rename.Renamed {
					pendingIndex = i
					break
				}
			}
			originalPath := filepath.Join(parent.Name(), path.Base(entry.Path))
			if pendingIndex < 0 {
				privateName = ".removing-" + string(domain.NewID())
				parentRelative, err := filepath.Rel(removal, parent.Name())
				if err != nil || len(pending) >= maxSnapshotRemovalEntries {
					return ResultUncertain()
				}
				privateRelative = path.Join(filepath.ToSlash(parentRelative), privateName)
				if parentRelative == "." {
					privateRelative = privateName
				}
				pendingIndex = len(pending)
				pending = append(pending, storageRemovalRename{Original: entry.Path, Private: privateRelative})
				stage = "rename-prepared-journal"
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenamePrepared}); err != nil {
					return err
				}
				stage = "entry-rename"
				if err := renameStorage(originalPath, filepath.Join(parent.Name(), privateName)); err != nil {
					return ResultUncertain()
				}
				stage = "rename-committed-journal"
				pending[pendingIndex].Renamed = true
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenameRenamed}); err != nil {
					return err
				}
			}
			privatePath := filepath.Join(parent.Name(), privateName)
			if m.storageAfterRemovalClaim != nil {
				m.storageAfterRemovalClaim(entry.Path)
			}
			restore := func() error {
				if err := renameStorage(privatePath, originalPath); err != nil {
					return ResultUncertain()
				}
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenameCleared}); err != nil {
					return err
				}
				if err := removePending(entry.Path, privateRelative); err != nil {
					return err
				}
				return nil
			}
			stage = "claimed-entry-verification"
			if before.IsDir() {
				if !os.FileMode(entry.Mode).IsDir() || !removalDirectoryModeMatches(before.Mode(), entry.Mode, pending[pendingIndex].ModePrepared) {
					if !pending[pendingIndex].ModePrepared {
						if restoreErr := restore(); restoreErr != nil {
							return restoreErr
						}
					}
					return ResultUncertain()
				}
				child, err := openVerifiedChildRoot(parent, privateName, before)
				if err == nil {
					current, statErr := child.Lstat(".")
					if statErr != nil || !removalDirectoryModeMatches(current.Mode(), entry.Mode, pending[pendingIndex].ModePrepared) {
						err = ResultUncertain()
					}
				}
				if err == nil {
					// Record the only authorized permission transition before chmod.
					// Recovery accepts that exact native mode, never arbitrary changes
					// made through a writer retaining the claimed directory.
					if !pending[pendingIndex].ModePrepared {
						err = m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalDirectoryModePrepared})
						if err == nil {
							pending[pendingIndex].ModePrepared = true
						}
					}
					if err == nil {
						err = child.Chmod(".", 0700)
					}
					if err == nil {
						err = remove(child, entry.Path)
					}
					if err == nil {
						current, statErr := child.Lstat(".")
						if statErr != nil || !os.SameFile(before, current) || current.Mode() != removalWritableDirectoryMode() {
							err = ResultUncertain()
						}
					}
					child.Close()
				}
				if err != nil {
					// Keep the private name and mode proof together after a native
					// chmod. Clearing either would discard original recovery authority.
					if !pending[pendingIndex].ModePrepared {
						if restoreErr := restore(); restoreErr != nil {
							return restoreErr
						}
					}
					return err
				}
			} else if err := verifyRemovalEntry(ctx, parent, privateName, before, entry, buffer); err != nil {
				if restoreErr := restore(); restoreErr != nil {
					return restoreErr
				}
				return err
			}
			stage = "entry-unlink"
			if err := parent.Remove(privateName); err != nil {
				if !before.IsDir() {
					if restoreErr := restore(); restoreErr != nil {
						return restoreErr
					}
				}
				return ResultUncertain()
			}
			if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenameRemoved}); err != nil {
				return err
			}
			if err := removePending(entry.Path, privateRelative); err != nil {
				return err
			}
		}
		return nil
	}
	stage = "root-permissions"
	rootInfo, err := root.Lstat(".")
	if err != nil || rootInfo.Mode() != removalWritableDirectoryMode() {
		return ResultUncertain()
	}
	if err := remove(root, "."); err != nil {
		return err
	}
	if m.storageBeforeRemovalUnlink != nil {
		m.storageBeforeRemovalUnlink(".")
	}
	stage = "final-root-identity"
	identity, err = directoryPathIdentity(removal)
	rootInfo, statErr := root.Lstat(".")
	if err != nil || identity != claim.RootIdentity || statErr != nil || rootInfo.Mode() != removalWritableDirectoryMode() {
		return ResultUncertain()
	}
	file, err = root.Open(".")
	if err != nil {
		return ResultUncertain()
	}
	names, readErr := file.Readdirnames(1)
	file.Close()
	if len(names) != 0 || readErr != io.EOF {
		return ResultUncertain()
	}
	root.Close()
	stage = "final-root-claim"
	return m.claimFinalRemovalRoot(ctx, r, claim)
}

func verifyRemovalEntry(ctx context.Context, parent *os.Root, name string, before os.FileInfo, entry snapshotEntry, buffer []byte) error {
	if uint32(before.Mode()) != entry.Mode {
		return ResultUncertain()
	}
	switch {
	case before.Mode()&os.ModeSymlink != 0:
		link, err := parent.Readlink(name)
		kind, kindErr := snapshotSymlinkKind(before)
		if err != nil || kindErr != nil || link != entry.Link || kind != entry.LinkKind {
			return ResultUncertain()
		}
	case before.Mode().IsRegular():
		if before.Size() < 0 || uint64(before.Size()) != entry.Size || entry.Size > MaxSnapshotBytes {
			return ResultUncertain()
		}
		file, err := openVerifiedEntry(parent, name, before)
		if err != nil {
			return ResultUncertain()
		}
		defer file.Close()
		hash := sha256.New()
		var read int64
		for read <= int64(entry.Size) {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := file.Read(buffer[:int(min(int64(len(buffer)), int64(entry.Size)-read+1))])
			read += int64(n)
			hash.Write(buffer[:n])
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return ResultUncertain()
			}
		}
		actual, err := file.Stat()
		if err != nil || !sameSnapshotFile(before, actual) || uint64(read) != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return ResultUncertain()
		}
	default:
		return ResultUncertain()
	}
	after, err := parent.Lstat(name)
	if err != nil || !sameSnapshotFile(before, after) {
		return ResultUncertain()
	}
	return nil
}

// Go exposes Windows directory permissions through the read-only attribute;
// Unix retains the exact private 0700 permission bits used by native cleanup.
func removalWritableDirectoryMode() os.FileMode {
	if runtime.GOOS == "windows" {
		return os.ModeDir | 0777
	}
	return os.ModeDir | 0700
}

func removalDirectoryModeMatches(current os.FileMode, pinned uint32, modePrepared bool) bool {
	return current.IsDir() && (uint32(current) == pinned || modePrepared && current == removalWritableDirectoryMode())
}
