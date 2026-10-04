// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const MaxSnapshotBytes uint64 = 8 << 30
const MaxSnapshotEntries = 8192
const maxPublishedSnapshots = 4096

// Snapshot deletion additionally inventories the workspace directory and manifest.
const maxSnapshotRemovalEntries = MaxSnapshotEntries + 2
const maxSnapshotManifest = 8 << 20

const snapshotRemovalNameLength = len(".removing-") + 36

// Every ancestor may retain its private claim name after interruption. Reserve
// the largest original/private spelling before admitting a recoverable copy.
func snapshotRemovalPathFits(value string, limit int) bool {
	length := 0
	for _, component := range strings.Split(value, "/") {
		length += max(len(component), snapshotRemovalNameLength) + 1
	}
	return length-1 <= limit
}

func snapshotPrivatePathLimit(root string) int {
	nativeLimit := 4096
	if runtime.GOOS == "darwin" {
		nativeLimit = 1024
	}
	if runtime.GOOS == "windows" {
		nativeLimit = 32760
	}
	// Allow the removal root and the snapshot workspace/repository/Git wrappers
	// to retain private names simultaneously. Recovery reads keep the original
	// 4,096-byte wire bound; this admission bound applies only to new captures.
	prefix := len(filepath.Join(root, "workspace-removals")) + 1 + 36 + 1
	return min(4096, nativeLimit-prefix) - 3*(snapshotRemovalNameLength+1)
}

type snapshotLinkKind string

const (
	snapshotFileLink      snapshotLinkKind = "file"
	snapshotDirectoryLink snapshotLinkKind = "directory"
)

type snapshotEntry struct {
	LinkKind snapshotLinkKind `json:"link_kind,omitempty"`
	Path     string           `json:"path"`
	Mode     uint32           `json:"mode"`
	Size     uint64           `json:"size,omitempty"`
	SHA256   string           `json:"sha256,omitempty"`
	Link     string           `json:"link,omitempty"`
}
type snapshotInventory struct {
	Entries []snapshotEntry `json:"entries"`
	Bytes   uint64          `json:"bytes"`
}

// One copy budget spans the workspace and every independent Git store. Reused
// administration directories consume no additional entry during an overlay.
type snapshotCopyBudget struct {
	bytes            uint64
	entries          int
	privatePathLimit int
}

func (b *snapshotCopyBudget) take(size uint64) error {
	if b.entries == 0 || size > b.bytes {
		return domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
	}
	b.entries--
	b.bytes -= size
	return nil
}

func snapshotUnsupported() error {
	return domain.Fail(domain.Unsupported, "The workspace cannot be copied faithfully.", "Remove unsupported special files or external Git object/configuration dependencies before retrying; source files were preserved.")
}

// walkSnapshot anchors each child to an already opened parent. Links are copied
// as links, including escaping links; they are never opened. Compare identities
// around every read and the complete inventory again before source removal.
func walkSnapshot(ctx context.Context, source, destination string, skip func(string) bool, merge ...bool) (snapshotInventory, error) {
	return walkSnapshotEntries(ctx, source, destination, skip, MaxSnapshotEntries, merge...)
}

func walkSnapshotEntries(ctx context.Context, source, destination string, skip func(string) bool, entryLimit int, merge ...bool) (snapshotInventory, error) {
	return walkSnapshotBudget(ctx, source, destination, skip, entryLimit, nil, merge...)
}

func walkSnapshotBudget(ctx context.Context, source, destination string, skip func(string) bool, entryLimit int, budget *snapshotCopyBudget, merge ...bool) (snapshotInventory, error) {
	var inventory snapshotInventory
	rootInfo, err := os.Lstat(source)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return inventory, ResultUncertain()
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return inventory, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !sameSnapshotFile(rootInfo, opened) {
		return inventory, ResultUncertain()
	}
	if destination != "" {
		if err := os.Mkdir(destination, 0700); err != nil && !(len(merge) > 0 && merge[0] && os.IsExist(err)) {
			return inventory, err
		}
	}
	var visit func(*os.Root, string) error
	visit = func(parent *os.Root, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := parent.Open(".")
		if err != nil {
			return err
		}
		names, readErr := dir.Readdirnames(entryLimit + 1)
		dir.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		sort.Strings(names)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := filepath.Join(relative, name)
			if skip != nil && skip(filepath.ToSlash(path)) {
				continue
			}
			if budget != nil && budget.privatePathLimit != 0 && !snapshotRemovalPathFits(filepath.ToSlash(path), budget.privatePathLimit) {
				return domain.Fail(domain.ResourceExhausted, "Workspace paths exceed cleanup headroom.", "Shorten directory names or nesting before snapshot creation; source files remain intact.")
			}
			if len(inventory.Entries) >= entryLimit || !utf8.ValidString(path) || len(path) > 4096 {
				return domain.Fail(domain.ResourceExhausted, "Workspace inventory exceeds its bound.", "Reduce the workspace; no files were removed.")
			}
			before, err := parent.Lstat(name)
			if err != nil {
				return err
			}
			// Observation consumes the same shared allowance before opening or
			// hashing payloads, even though it has no copy destination.
			if budget != nil && destination == "" {
				var size uint64
				if before.Mode().IsRegular() {
					if before.Size() < 0 {
						return ResultUncertain()
					}
					size = uint64(before.Size())
				}
				if err := budget.take(size); err != nil {
					return err
				}
			}
			entry := snapshotEntry{Path: filepath.ToSlash(path), Mode: uint32(before.Mode())}
			target := ""
			if destination != "" {
				target = filepath.Join(destination, path)
			}
			switch {
			case before.IsDir():
				child, err := parent.OpenRoot(name)
				if err != nil {
					return err
				}
				actual, err := child.Stat(".")
				if err != nil || !os.SameFile(before, actual) {
					child.Close()
					return ResultUncertain()
				}
				if target != "" {
					_, existsErr := os.Lstat(target)
					reused := len(merge) > 0 && merge[0] && existsErr == nil
					if budget != nil && !reused {
						if err := budget.take(0); err != nil {
							child.Close()
							return err
						}
					}
					if err := os.Mkdir(target, 0700); err != nil && !reused {
						child.Close()
						return err
					}
				}
				inventory.Entries = append(inventory.Entries, entry)
				err = visit(child, path)
				child.Close()
				if err != nil {
					return err
				}
				if target != "" {
					if err := os.Chmod(target, before.Mode().Perm()|before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
						return err
					}
					if err := syncSnapshotDir(target); err != nil {
						return err
					}
				}
			case before.Mode()&os.ModeSymlink != 0:
				link, err := parent.Readlink(name)
				if err != nil || !utf8.ValidString(link) || len(link) > 4096 {
					return snapshotUnsupported()
				}
				entry.Link = link
				entry.LinkKind, err = snapshotSymlinkKind(before)
				if err != nil {
					return err
				}
				if target != "" {
					if budget != nil {
						if err := budget.take(0); err != nil {
							return err
						}
					}
					if err := createSnapshotSymlink(link, target, entry.LinkKind); err != nil {
						return err
					}
				}
				inventory.Entries = append(inventory.Entries, entry)
			case before.Mode().IsRegular():
				if before.Size() < 0 || uint64(before.Size()) > MaxSnapshotBytes-inventory.Bytes {
					return domain.Fail(domain.ResourceExhausted, "Workspace snapshot exceeds 8 GiB.", "Reduce the workspace; sources remain intact.")
				}
				file, err := openWorkspaceEntry(parent, name)
				if err != nil {
					return err
				}
				actual, err := file.Stat()
				if err != nil || !sameSnapshotFile(before, actual) {
					file.Close()
					return ResultUncertain()
				}
				hash := sha256.New()
				var output *os.File
				writer := io.Writer(hash)
				if target != "" {
					if budget != nil {
						if err := budget.take(uint64(before.Size())); err != nil {
							file.Close()
							return err
						}
					}
					output, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err != nil {
						file.Close()
						return err
					}
					writer = io.MultiWriter(hash, output)
				}
				// Read one extra byte to detect growth after the metadata observation.
				buffer := make([]byte, 128<<10)
				remaining := before.Size() + 1
				var copied int64
				for remaining > 0 {
					if err = ctx.Err(); err != nil {
						break
					}
					n, readErr := file.Read(buffer[:min(int64(len(buffer)), remaining)])
					if n > 0 {
						// The extra probe detects growth without writing bytes beyond
						// the reservation made before opening the destination.
						if copied+int64(n) > before.Size() {
							err = ResultUncertain()
							break
						}
						var written int
						written, err = writer.Write(buffer[:n])
						copied += int64(written)
						remaining -= int64(n)
						if err != nil {
							break
						}
					}
					if readErr == io.EOF {
						break
					}
					if readErr != nil {
						err = readErr
						break
					}
				}
				after, statErr := file.Stat()
				file.Close()
				if err == nil && (statErr != nil || !sameSnapshotFile(before, after) || copied != before.Size()) {
					err = ResultUncertain()
				}
				if output != nil {
					if err == nil {
						err = output.Chmod(before.Mode().Perm() | before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
					}
					if err == nil {
						err = output.Sync()
					}
					closeErr := output.Close()
					if err == nil {
						err = closeErr
					}
				}
				if err != nil {
					return err
				}
				entry.Size = uint64(copied)
				entry.SHA256 = hex.EncodeToString(hash.Sum(nil))
				inventory.Bytes += entry.Size
				inventory.Entries = append(inventory.Entries, entry)
			default:
				return snapshotUnsupported()
			}
			after, err := parent.Lstat(name)
			if err == nil && before.Mode()&os.ModeSymlink != 0 {
				kind, kindErr := snapshotSymlinkKind(after)
				if kindErr != nil || kind != entry.LinkKind {
					return ResultUncertain()
				}
			}
			if err != nil || !sameSnapshotFile(before, after) {
				return ResultUncertain()
			}
		}
		return nil
	}
	err = visit(root, "")
	if err == nil && destination != "" {
		err = syncSnapshotDir(destination)
	}
	final, finalErr := os.Lstat(source)
	anchored, anchoredErr := root.Stat(".")
	// Root entries are enumerated before child reads, just like nested entries.
	// A retained directory handle can mutate this root even after a rename claim;
	// identity alone must not authorize an incomplete inventory or source removal.
	if err == nil && (finalErr != nil || anchoredErr != nil || !sameSnapshotFile(rootInfo, final) || !sameSnapshotFile(rootInfo, anchored)) {
		err = ResultUncertain()
	}
	sort.Slice(inventory.Entries, func(i, j int) bool { return inventory.Entries[i].Path < inventory.Entries[j].Path })
	return inventory, err
}
func sameSnapshotFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime() == b.ModTime()
}
func inventoryDigest(in snapshotInventory) string {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func syncSnapshotDir(path string) error { return security.SyncParent(filepath.Join(path, "entry")) }

// Removal never follows a link or leaves the opened private namespace. It is
// cancellable and bounded; unfinished removal remains reachable for recovery.
func removeSnapshotTree(ctx context.Context, path string, expectedIdentity ...string) error {
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return ResultUncertain()
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Open(".")
	if err != nil {
		return ResultUncertain()
	}
	actual, statErr := opened.Stat()
	identity, identityErr := directoryFileIdentity(opened)
	opened.Close()
	named, namedErr := os.Lstat(path)
	if statErr != nil || identityErr != nil || namedErr != nil || !os.SameFile(before, actual) || !os.SameFile(actual, named) || (len(expectedIdentity) > 0 && identity != expectedIdentity[0]) {
		return ResultUncertain()
	}
	count := 0
	var remove func(*os.Root) error
	remove = func(parent *os.Root) error {
		file, err := parent.Open(".")
		if err != nil {
			return err
		}
		names, err := file.Readdirnames(MaxSnapshotEntries + 1)
		file.Close()
		if err != nil && err != io.EOF {
			return err
		}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > MaxSnapshotEntries*2 {
				return ResultUncertain()
			}
			info, err := parent.Lstat(name)
			if err != nil {
				return err
			}
			if info.IsDir() {
				child, err := parent.OpenRoot(name)
				if err != nil {
					return err
				}
				actual, err := child.Stat(".")
				if err != nil || !os.SameFile(info, actual) {
					child.Close()
					return ResultUncertain()
				}
				// Files can be read-only in a faithful snapshot; the owned directory
				// remains removable without changing the user's original source modes.
				if err := child.Chmod(".", 0700); err != nil {
					child.Close()
					return err
				}
				err = remove(child)
				child.Close()
				if err != nil {
					return err
				}
			}
			now, err := parent.Lstat(name)
			if err != nil || !os.SameFile(info, now) {
				return ResultUncertain()
			}
			if err := parent.Remove(name); err != nil {
				return err
			}
		}
		return nil
	}
	if err := root.Chmod(".", 0700); err != nil {
		return err
	}
	if err := remove(root); err != nil {
		return err
	}
	root.Close()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return ResultUncertain()
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return security.SyncParent(path)
}
func skipSnapshotGit(path string) bool { return path == ".git" || strings.HasPrefix(path, ".git/") }
