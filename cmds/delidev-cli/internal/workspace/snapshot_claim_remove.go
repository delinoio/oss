// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Scratch cleanup may enumerate its own output. Claimed source removal instead
// uses only the immutable intent and checks each entry immediately before unlink.
// New entries are never selected; a nonempty directory cannot be removed by rmdir.
func (m *Manager) removeClaimedSnapshotTree(ctx context.Context, r StorageRequest, removal string, partial bool) error {
	if removal != filepath.Join(m.Root, "workspace-removals", string(r.OperationID)) {
		return ResultUncertain()
	}
	raw, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	var intent storageRemovalIntent
	if err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != r.OperationID || intent.SessionID != r.Preparation.SessionID || intent.SnapshotID != r.SnapshotID || intent.Action != r.Action || len(intent.Inventory.Entries) > maxSnapshotRemovalEntries {
		return ResultUncertain()
	}
	claim, pending, err := m.readRemovalClaimPending(r, raw)
	if err != nil {
		return ResultUncertain()
	}
	identity, err := directoryPathIdentity(removal)
	if err != nil || identity != claim.RootIdentity {
		return ResultUncertain()
	}
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
	root, err := os.OpenRoot(removal)
	if err != nil {
		return ResultUncertain()
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return ResultUncertain()
	}
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
			before, err := parent.Lstat(name)
			if os.IsNotExist(err) && partial {
				continue
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
			privateName := ".removing-" + string(domain.NewID())
			privatePath := filepath.Join(parent.Name(), privateName)
			originalPath := filepath.Join(parent.Name(), name)
			parentRelative, err := filepath.Rel(removal, parent.Name())
			if err != nil {
				return ResultUncertain()
			}
			privateRelative := path.Join(filepath.ToSlash(parentRelative), privateName)
			if parentRelative == "." {
				privateRelative = privateName
			}
			reused := false
			for _, rename := range pending {
				if rename.Original != entry.Path {
					continue
				}
				// Recovery may resume after the private rename but before the
				// mapping-clear record. Reuse that durable private name; appending
				// another prepared record would make the journal unrecoverable.
				if !rename.Renamed {
					return ResultUncertain()
				}
				privateRelative = rename.Private
				privatePath = filepath.Join(removal, filepath.FromSlash(privateRelative))
				privateName = filepath.Base(privatePath)
				originalPath = filepath.Join(parent.Name(), path.Base(entry.Path))
				reused = true
				break
			}
			if !reused {
				if len(pending) >= maxSnapshotRemovalEntries {
					return ResultUncertain()
				}
				pending = append(pending, storageRemovalRename{Original: entry.Path, Private: privateRelative})
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenamePrepared}); err != nil {
					return err
				}
				if err := renameStorage(originalPath, privatePath); err != nil {
					return ResultUncertain()
				}
				pending[len(pending)-1].Renamed = true
				if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenameRenamed}); err != nil {
					return err
				}
				if m.storageAfterRemovalClaim != nil {
					m.storageAfterRemovalClaim(entry.Path)
				}
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
			if before.IsDir() {
				if !os.FileMode(entry.Mode).IsDir() || !partial && uint32(before.Mode()) != entry.Mode {
					if restoreErr := restore(); restoreErr != nil {
						return restoreErr
					}
					return ResultUncertain()
				}
				child, err := openVerifiedChildRoot(parent, privateName, before)
				if err == nil {
					// Owned directory permission changes are necessary for faithfully
					// captured read-only trees; partial recovery retains their identity.
					if err = child.Chmod(".", 0700); err == nil {
						err = remove(child, entry.Path)
					}
					child.Close()
				}
				if err != nil {
					if restoreErr := restore(); restoreErr != nil {
						return restoreErr
					}
					return err
				}
			} else if err := verifyRemovalEntry(ctx, parent, privateName, before, entry, buffer); err != nil {
				if restoreErr := restore(); restoreErr != nil {
					return restoreErr
				}
				return err
			}
			if err := parent.Remove(privateName); err != nil {
				if restoreErr := restore(); restoreErr != nil {
					return restoreErr
				}
				return ResultUncertain()
			}
			if err := m.appendRemovalClaimRecord(ctx, r, storageRemovalRenameRecord{Original: entry.Path, Private: privateRelative, State: storageRemovalRenameCleared}); err != nil {
				return err
			}
			if err := removePending(entry.Path, privateRelative); err != nil {
				return err
			}
		}
		return nil
	}
	if err := root.Chmod(".", 0700); err != nil {
		return err
	}
	if err := remove(root, "."); err != nil {
		return err
	}
	if m.storageBeforeRemovalUnlink != nil {
		m.storageBeforeRemovalUnlink(".")
	}
	identity, err = directoryPathIdentity(removal)
	if err != nil || identity != claim.RootIdentity {
		return ResultUncertain()
	}
	root.Close()
	if err := os.Remove(removal); err != nil {
		return ResultUncertain()
	}
	return security.SyncParent(removal)
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
