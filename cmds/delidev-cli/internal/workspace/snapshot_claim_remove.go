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
	claimRaw, err := security.ReadPrivate(m.removalClaimPath(r.OperationID), maxStorageRemovalClaim)
	var claim storageRemovalClaim
	if err != nil || !removalClaimMatches(claimRaw, removalReference(r), raw) || domain.Decode(claimRaw, &claim) != nil {
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
		for _, name := range names {
			if _, ok := known[path.Join(relative, name)]; !ok {
				return ResultUncertain()
			}
		}
		for _, entry := range children[relative] {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := path.Base(entry.Path)
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
			if before.IsDir() {
				if !os.FileMode(entry.Mode).IsDir() || !partial && uint32(before.Mode()) != entry.Mode {
					return ResultUncertain()
				}
				child, err := openVerifiedChildRoot(parent, name, before)
				if err != nil {
					return ResultUncertain()
				}
				// Owned directory permission changes are necessary for faithfully
				// captured read-only trees; partial recovery retains their identity.
				if err = child.Chmod(".", 0700); err == nil {
					err = remove(child, entry.Path)
				}
				child.Close()
				if err != nil {
					return err
				}
				after, err := parent.Lstat(name)
				if err != nil || !os.SameFile(before, after) || !after.IsDir() {
					return ResultUncertain()
				}
			} else if err := verifyRemovalEntry(ctx, parent, name, before, entry, buffer); err != nil {
				return err
			}
			if err := parent.Remove(name); err != nil {
				return ResultUncertain()
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
