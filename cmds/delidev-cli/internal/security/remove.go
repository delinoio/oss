// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// OwnedTreeAdmission retains the originally admitted native root and every
// existing ancestor. An empty identity is an explicit absence proof, never
// permission to baseline a later replacement. Paths are private relative names.
type OwnedTreeAdmission struct {
	Path     string            `json:"path"`
	Identity string            `json:"identity"`
	Parents  map[string]string `json:"parents"`
}

func CaptureOwnedTree(root, path string) (OwnedTreeAdmission, error) {
	proof := OwnedTreeAdmission{Parents: map[string]string{}}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return proof, domain.SessionDeletionPending()
	}
	proof.Path = relative
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		rel, err := filepath.Rel(root, parent)
		if err != nil || rel != "." && !filepath.IsLocal(rel) {
			return proof, domain.SessionDeletionPending()
		}
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			proof.Parents[rel] = ""
		} else {
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return proof, domain.SessionDeletionPending()
			}
			identity, err := removalPathIdentity(parent, info)
			if err != nil {
				return proof, domain.SessionDeletionPending()
			}
			proof.Parents[rel] = identity
		}
		if parent == root {
			break
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return proof, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return proof, domain.SessionDeletionPending()
	}
	proof.Identity, err = removalPathIdentity(path, info)
	if err != nil {
		return proof, domain.SessionDeletionPending()
	}
	return proof, nil
}

// CheckOwnedTreeAdmission accepts disappearance during independent original
// cleanup, but no new parent/root identity. It is also used before each unlink
// so traversal cannot borrow authority from a moved ancestor.
func CheckOwnedTreeAdmission(root, path string, proof OwnedTreeAdmission) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative != proof.Path || relative == "." || !filepath.IsLocal(relative) || len(proof.Parents) == 0 {
		return domain.SessionDeletionPending()
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		rel, err := filepath.Rel(root, parent)
		identity, known := proof.Parents[rel]
		if err != nil || !known {
			return domain.SessionDeletionPending()
		}
		info, err := os.Lstat(parent)
		if !errors.Is(err, os.ErrNotExist) {
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || identity == "" {
				return domain.SessionDeletionPending()
			}
			current, err := removalPathIdentity(parent, info)
			if err != nil || current != identity {
				return domain.SessionDeletionPending()
			}
		}
		if parent == root {
			break
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || proof.Identity == "" || info.Mode()&os.ModeSymlink != 0 {
		return domain.SessionDeletionPending()
	}
	current, err := removalPathIdentity(path, info)
	if err != nil || current != proof.Identity {
		return domain.SessionDeletionPending()
	}
	return nil
}

func RemoveAdmittedTree(ctx context.Context, root, path string, proof OwnedTreeAdmission) error {
	return removeOwnedTree(ctx, root, path, &proof)
}

// RemoveOwnedTree is used only after independently stopping original owners.
// It never follows links, rejects replaced roots and observes cancellation and
// an explicit traversal bound before each unlink. It makes no disk-space claim.
func RemoveOwnedTree(ctx context.Context, root, path string) error {
	return removeOwnedTree(ctx, root, path, nil)
}

func removeOwnedTree(ctx context.Context, root, path string, proof *OwnedTreeAdmission) error {
	if proof != nil {
		if err := CheckOwnedTreeAdmission(root, path, *proof); err != nil {
			return err
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			if proof.Identity == "" {
				return nil
			}
			// An original root may have been unlinked immediately before a crash.
			// Preserve its original durability gate; initial absence has no such work.
			if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err := SyncParent(path); err != nil {
				return domain.SessionDeletionPending()
			}
			return nil
		}
	}
	parent := filepath.Dir(path)
	if _, e := os.Lstat(parent); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return domain.SessionDeletionPending()
	}
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent || !strings.HasPrefix(parent, root+string(filepath.Separator)) {
		return domain.SessionDeletionPending()
	}
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return SyncParent(path)
	} else if e != nil || info.Mode()&os.ModeSymlink != 0 {
		return domain.SessionDeletionPending()
	}
	type entry struct {
		path string
		info os.FileInfo
	}
	entries := []entry{}
	directories := map[string]os.FileInfo{}
	e = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if proof != nil {
			if err := CheckOwnedTreeAdmission(root, path, *proof); err != nil {
				return err
			}
		}
		if e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		entries = append(entries, entry{p, info})
		if info.IsDir() {
			directories[p] = info
		}
		if len(entries) > 100000 {
			return domain.SessionDeletionPending()
		}
		return nil
	})
	if e != nil {
		return domain.SessionDeletionPending()
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		if proof != nil {
			if err := CheckOwnedTreeAdmission(root, path, *proof); err != nil {
				return err
			}
		}
		if proof != nil {
			for parent := filepath.Dir(entries[i].path); parent != filepath.Dir(path); parent = filepath.Dir(parent) {
				original, known := directories[parent]
				current, err := os.Lstat(parent)
				if !known || err != nil || !os.SameFile(current, original) {
					return domain.SessionDeletionPending()
				}
			}
		}
		current, e := os.Lstat(entries[i].path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !os.SameFile(current, entries[i].info) {
			return domain.SessionDeletionPending()
		}
		if e := os.Remove(entries[i].path); e != nil {
			return domain.SessionDeletionPending()
		}
	}
	if e := SyncParent(path); e != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
