package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxCheckpointFiles = 8192
const maxCheckpointFileBytes = 256 << 20

// These private records pin every original closed runtime entry, including
// SQLite WAL and native file artifacts. Paths and bytes never enter logs or
// public execution reports. An inventory is not process-cleanup authority.
type checkpointFile struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Mode      uint32 `json:"mode"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
}

func checkpointFiles(ctx context.Context, home string) ([]checkpointFile, error) {
	if !canonicalDirectory(home) || security.CheckPrivateDir(home) != nil {
		return nil, sessionUncertain()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, sessionUncertain()
	}
	defer root.Close()
	var files []checkpointFile
	var total int64
	database := false
	var entries []struct {
		name string
		info fs.FileInfo
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil || len(files) >= maxCheckpointFiles || !fs.ValidPath(name) {
			return sessionUncertain()
		}
		before, err := root.Lstat(name)
		if err != nil || !ownedCheckpointEntry(filepath.Join(home, filepath.FromSlash(name)), before) {
			return sessionUncertain()
		}
		file := checkpointFile{Path: name, Directory: before.IsDir(), Mode: uint32(before.Mode().Perm())}
		entries = append(entries, struct {
			name string
			info fs.FileInfo
		}{name, before})
		if !before.IsDir() {
			if before.Size() < 0 || before.Size() > maxCheckpointFileBytes-total {
				return sessionUncertain()
			}
			opened, err := root.OpenFile(name, checkpointReadFlags(), 0)
			if err != nil {
				return sessionUncertain()
			}
			defer opened.Close()
			actual, err := opened.Stat()
			if err != nil || !sameCheckpointFile(before, actual) || !ownedCheckpointOpenFile(opened) {
				return sessionUncertain()
			}
			hash := sha256.New()
			count, err := io.Copy(hash, io.LimitReader(checkpointReader{ctx, opened}, before.Size()+1))
			after, statErr := opened.Stat()
			pathAfter, pathErr := root.Lstat(name)
			closeErr := opened.Close()
			if err != nil || statErr != nil || pathErr != nil || closeErr != nil || count != before.Size() || !sameCheckpointFile(before, after) || !sameCheckpointFile(before, pathAfter) {
				return sessionUncertain()
			}
			file.Size, file.SHA256 = count, hex.EncodeToString(hash.Sum(nil))
			total += count
			if name == "data/opencode/opencode.db" && count > 0 {
				database = true
			}
		}
		files = append(files, file)
		return nil
	})
	if err != nil || !database || ctx.Err() != nil {
		return nil, sessionUncertain()
	}
	// A previously read file can change while a later file is being hashed.
	// Recheck every captured identity, not only directory listings.
	for _, entry := range entries {
		after, err := root.Lstat(entry.name)
		if err != nil || ctx.Err() != nil || !sameCheckpointFile(entry.info, after) || !ownedCheckpointEntry(filepath.Join(home, filepath.FromSlash(entry.name)), after) {
			return nil, sessionUncertain()
		}
	}
	// Recheck the caller's canonical root, not just the handle's renamed inode.
	original, err := root.Stat(".")
	current, pathErr := os.Stat(home)
	if err != nil || pathErr != nil || !sameCheckpointFile(original, current) || !canonicalDirectory(home) || security.CheckPrivateDir(home) != nil {
		return nil, sessionUncertain()
	}
	return files, nil
}

func sameCheckpointFile(a, b fs.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

type checkpointReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r checkpointReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func validateCheckpointFiles(files []checkpointFile) bool {
	if len(files) == 0 || len(files) > maxCheckpointFiles || files[0].Path != "." || !files[0].Directory {
		return false
	}
	seen := map[string]bool{}
	var total int64
	database := false
	for _, file := range files {
		_, exists := seen[file.Path]
		if !fs.ValidPath(file.Path) || exists || file.Mode > 0777 || file.Size < 0 || file.Size > maxCheckpointFileBytes-total {
			return false
		}
		if file.Path != "." && !seen[filepath.ToSlash(filepath.Dir(filepath.FromSlash(file.Path)))] {
			return false
		}
		seen[file.Path] = file.Directory
		if file.Directory {
			if file.Size != 0 || file.SHA256 != "" {
				return false
			}
			continue
		}
		if !checkpointDigest(file.SHA256) {
			return false
		}
		total += file.Size
		database = database || file.Path == "data/opencode/opencode.db" && file.Size > 0
	}
	return database
}

func sameCheckpointFiles(a, b []checkpointFile) bool { return slices.Equal(a, b) }

func checkpointDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}
