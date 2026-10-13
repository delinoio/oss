// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Hooks let tests swap a path deterministically; both are nil in production.
var (
	walkAfterOpenDirHook func(path string)
	walkBeforeUnlinkHook func()
)

// walkNode is one object of the inventory. Directories hold their handle open so
// their identity cannot be reused while the inventory is live.
type walkNode struct {
	path   string
	record PathIdentityRecord
	held   *os.File
	parent *walkNode
}

func (n *walkNode) close() {
	if n.held != nil {
		n.held.Close()
		n.held = nil
	}
}

// bound reports whether n's path still resolves to the inventoried object. A
// path that no longer exists is reported as os.ErrNotExist.
func (n *walkNode) bound() error {
	f, current, e := openPathIdentity(n.path)
	if errors.Is(e, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	if f != nil {
		f.Close()
	}
	if !n.record.sameObject(current) {
		return domain.SessionDeletionPending()
	}
	return nil
}

// removeTreeByPath is the path-based remover for platforms without
// descriptor-relative unlink (Windows). It inventories the whole tree first,
// opening and holding every directory before listing it and confirming the path
// still resolves to that held directory afterwards, so a listing never comes from
// a replacement. Before every unlink it re-binds the entry and every ancestor up
// to the root to their inventoried objects, using handle-derived records only:
// path-based os.SameFile opens the path with zero sharing, which conflicts with
// the handles held here. Each check-then-unlink pair remains non-atomic, so this
// narrows the replacement window but does not close it.
func removeTreeByPath(ctx context.Context, path string, expected PathIdentity) error {
	f, record, e := openPathIdentity(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil // the validated original already vanished
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	root := &walkNode{path: path, record: record, held: f}
	order := []*walkNode{}
	defer func() {
		root.close()
		for _, n := range order {
			n.close()
		}
	}()
	if !expected.record.sameObject(record) || record.Kind == ObjectSymlink {
		return domain.SessionDeletionPending()
	}
	count := 0
	if record.Kind == ObjectDirectory {
		if e := inventoryDirectory(ctx, root, 1, &count, &order); e != nil {
			return e
		}
	}
	order = append(order, root)
	if walkBeforeUnlinkHook != nil {
		walkBeforeUnlinkHook()
	}
	for _, n := range order {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		for ancestor := n.parent; ancestor != nil; ancestor = ancestor.parent {
			if e := ancestor.bound(); errors.Is(e, os.ErrNotExist) {
				return domain.SessionDeletionPending()
			} else if e != nil {
				return e
			}
		}
		if e := n.bound(); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		// Open handles would leave a removed directory delete-pending.
		n.close()
		if n == root {
			expected.Release()
		}
		if e := os.Remove(n.path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}

// inventoryDirectory appends dir's descendants in removal order (children before
// their directory). dir is already open and held.
func inventoryDirectory(ctx context.Context, dir *walkNode, depth int, count *int, order *[]*walkNode) error {
	if depth > maxRemoveDepth {
		return domain.SessionDeletionPending()
	}
	if walkAfterOpenDirHook != nil {
		walkAfterOpenDirHook(dir.path)
	}
	entries, e := os.ReadDir(dir.path)
	if e != nil {
		return domain.SessionDeletionPending()
	}
	// The listing is only trustworthy if the path still names the held directory.
	if e := dir.bound(); e != nil {
		return domain.SessionDeletionPending()
	}
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		if *count++; *count > maxRemoveEntries {
			return domain.SessionDeletionPending()
		}
		child := filepath.Join(dir.path, entry.Name())
		f, record, e := openPathIdentity(child)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return domain.SessionDeletionPending()
		}
		node := &walkNode{path: child, record: record, parent: dir}
		if record.Kind != ObjectDirectory {
			if f != nil {
				f.Close()
			}
			*order = append(*order, node)
			continue
		}
		node.held = f
		if record.Device != dir.record.Device {
			node.close()
			return domain.SessionDeletionPending()
		}
		// Register before recursing so its handle is released on any failure.
		pos := len(*order)
		*order = append(*order, node)
		if e := inventoryDirectory(ctx, node, depth+1, count, order); e != nil {
			return e
		}
		// Move the directory after its descendants.
		moved := append([]*walkNode(nil), (*order)[pos+1:]...)
		*order = append((*order)[:pos], moved...)
		*order = append(*order, node)
	}
	return nil
}
