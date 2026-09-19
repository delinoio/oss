package core

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// One batch session streams raw objects without filters or working-tree conversions.
func materializeBlobs(ctx context.Context, dir string, owned *os.Root, sha string) error {
	cmd := gitCommand(ctx, dir, "cat-file", "--batch")
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(output)
	buffer := make([]byte, 32*1024)
	err = walkTree(ctx, dir, sha, func(entry treeEntry) error {
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = io.WriteString(input, entry.OID+"\n"); err != nil {
			return err
		}
		header, err := reader.ReadSlice('\n')
		if err != nil {
			return E("git-object-unavailable", "cannot read committed object header", 3)
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[0] != entry.OID || fields[1] != "blob" {
			return E("git-object-unavailable", "unexpected committed object header", 3)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return E("git-object-unavailable", "invalid committed object size", 3)
		}
		if size > 0 && size < lfsPointerCutoff {
			candidate, err := reader.Peek(int(size))
			if err != nil {
				return E("git-object-unavailable", "truncated committed object", 3)
			}
			if isLFSPointer(candidate) {
				return E("lfs-unsupported", "Git LFS pointer source is unsupported", 2)
			}
		}

		path := filepath.FromSlash(entry.Path)
		if err = owned.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if entry.Mode == "120000" {
			// OS symlink targets are bounded; never allocate an arbitrary blob as a path.
			if size > 64*1024 {
				return E("unsupported-source-path", "symlink target exceeds supported path size", 2)
			}
			data, err := io.ReadAll(io.LimitReader(reader, size))
			if err != nil || int64(len(data)) != size {
				return E("git-object-unavailable", "truncated symlink object", 3)
			}
			if err = owned.Symlink(string(data), path); err != nil {
				return Wrap("workspace-write", err)
			}
		} else {
			mode := os.FileMode(0600)
			if entry.Mode == "100755" {
				mode = 0700
			}
			file, err := owned.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return Wrap("workspace-write", err)
			}
			copied, copyErr := io.CopyBuffer(file, io.LimitReader(reader, size), buffer)
			closeErr := file.Close()
			if copyErr != nil {
				return Wrap("workspace-write", copyErr)
			}
			if copied != size {
				return E("git-object-unavailable", "truncated committed object", 3)
			}
			if closeErr != nil {
				return Wrap("workspace-write", closeErr)
			}
		}
		delimiter, err := reader.ReadByte()
		if err != nil || delimiter != '\n' {
			return E("git-object-unavailable", "invalid committed object boundary", 3)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = input.Close(); err != nil {
		return err
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		return E("git-object-unavailable", "committed object reader failed", 3)
	}
	return nil
}
