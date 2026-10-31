// SPDX-License-Identifier: Apache-2.0

// Package clone prepares independent writable cache trees. Callers must hold
// source and destination coordination locks and validate repository ownership.
package clone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type Mode string

const (
	ModeClone Mode = "clone"
	ModeCopy  Mode = "copy"
	ModeMixed Mode = "mixed"
)

type Result struct {
	ClonedFiles int  `json:"clonedFiles"`
	CopiedFiles int  `json:"copiedFiles"`
	Mode        Mode `json:"mode"`
}

// ErrUnsupported identifies unavailable CoW capability. Only this error allows
// an explicitly authorized ordinary copy; operational failures remain fatal.
var ErrUnsupported = errors.New("copy-on-write is unavailable; use a supported same-volume cache or explicitly allow ordinary copying")

type cloneFileFunc func(context.Context, *os.File, string) error

// Tree creates destination, which must not exist, and removes it on any error.
// It preserves permission bits and modification times, not inode identity or
// platform-specific ACLs. It never rewrites build fingerprints. The caller owns
// publication/replacement of this staging tree and must prevent concurrent writes.
// Symlinks and special files are rejected; no hard links are created.
func Tree(ctx context.Context, source, destination string, allowCopy bool) (Result, error) {
	return tree(ctx, source, destination, allowCopy, nativeClone)
}

func tree(ctx context.Context, source, destination string, allowCopy bool, cloneFile cloneFileFunc) (result Result, err error) {
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return Result{}, fmt.Errorf("resolve source cache: %w", err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return Result{}, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return Result{}, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return Result{}, fmt.Errorf("resolve destination parent: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if overlaps(source, destination) {
		return Result{}, errors.New("source and destination overlap; choose separate cache trees")
	}
	src, err := os.OpenRoot(source)
	if err != nil {
		return Result{}, fmt.Errorf("open source cache: %w", err)
	}
	defer src.Close()
	// String comparison cannot detect case aliases or bind-mounted source roots.
	// Compare existing ancestor identities before creating anything in the source.
	sourceInfo, err := src.Stat(".")
	if err != nil {
		return Result{}, err
	}
	for ancestor := parent; ; ancestor = filepath.Dir(ancestor) {
		info, statErr := os.Stat(ancestor)
		if statErr != nil {
			return Result{}, statErr
		}
		if os.SameFile(sourceInfo, info) {
			return Result{}, errors.New("destination is within the source cache; choose separate cache trees")
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return Result{}, fmt.Errorf("create absent destination cache: %w", err)
	}
	defer func() {
		if err != nil {
			// Finalized directories may be read-only. Restore owner access only in our
			// staging tree so failure cleanup can remove their children.
			cleanupErr := filepath.WalkDir(destination, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return os.Chmod(path, 0700)
				}
				return os.Chmod(path, 0600)
			})
			err = errors.Join(err, cleanupErr, os.RemoveAll(destination))
			result = Result{}
		}
	}()
	result.Mode = ModeClone
	err = walk(ctx, src, ".", destination, allowCopy, cloneFile, &result)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		if result.CopiedFiles > 0 {
			result.Mode = ModeCopy
			if result.ClonedFiles > 0 {
				result.Mode = ModeMixed
			}
		}
		slog.DebugContext(ctx, "prepared cache tree", "mode", result.Mode, "cloned_files", result.ClonedFiles, "copied_files", result.CopiedFiles)
	}
	return result, err
}

func overlaps(a, b string) bool {
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func walk(ctx context.Context, root *os.Root, name, dst string, allowCopy bool, cloneFile cloneFileFunc, result *Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("cache entry %q is a link or special file; remove it or select another source", name)
	}
	f, err := root.OpenFile(name, sourceOpenFlags, 0)
	if err != nil {
		return fmt.Errorf("open cache entry %q: %w", name, err)
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, actual) || info.Mode().Type() != actual.Mode().Type() {
		return fmt.Errorf("cache entry %q changed during preparation; stop source writers and retry", name)
	}
	if info.IsDir() {
		if name != "." {
			if err := os.Mkdir(dst, 0700); err != nil {
				return err
			}
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				if err := walk(ctx, root, filepath.Join(name, entry.Name()), filepath.Join(dst, entry.Name()), allowCopy, cloneFile, result); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
	} else {
		err = cloneFile(ctx, f, dst)
		if err != nil {
			if !allowCopy || !errors.Is(err, ErrUnsupported) {
				return fmt.Errorf("clone cache entry %q (check space, permissions, and filesystem capability): %w", name, err)
			}
			// A native operation may have created or partially cloned its destination.
			// Discard that inode before copying; never reuse a partial native result.
			if removeErr := os.Remove(dst); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				return removeErr
			}
			if _, err = f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if err = copyFile(ctx, f, dst); err != nil {
				return fmt.Errorf("copy cache entry %q: %w", name, err)
			}
			result.CopiedFiles++
		} else {
			result.ClonedFiles++
		}
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
		return fmt.Errorf("cache entry %q changed during preparation; stop source writers and retry", name)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyFile(ctx context.Context, src *os.File, dst string) (err error) {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, contextReader{ctx, src})
	return err
}
