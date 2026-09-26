package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// The initial non-VCS text replacement profile needs only SQLite state. Copy
// the exact database plus any original WAL/SHM; never copy account/config/cache
// files, discard a WAL, rewrite native rows or mutate the original runtime.
// Auxiliary/tool/project histories are refused before this staging boundary.
func copyCheckpointDatabase(ctx context.Context, source nativeCheckpoint, home string) error {
	if !canonicalDirectory(home) || security.CheckPrivateDir(home) != nil || !validateCheckpointFiles(source.Files) {
		return sessionUncertain()
	}
	data := filepath.Join(home, "data")
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 0 || security.CheckPrivateDir(data) != nil {
		return sessionUncertain()
	}
	destination := filepath.Join(data, "opencode")
	if err := os.Mkdir(destination, 0700); err != nil {
		return sessionUncertain()
	}
	if security.CheckPrivateDir(destination) != nil || security.SyncParent(destination) != nil {
		return sessionUncertain()
	}
	root, err := os.OpenRoot(source.RuntimeHome)
	if err != nil {
		return sessionUncertain()
	}
	defer root.Close()
	target, err := os.OpenRoot(destination)
	if err != nil {
		return sessionUncertain()
	}
	defer target.Close()
	database := false
	for _, entry := range source.Files {
		switch entry.Path {
		case "data/opencode/opencode.db", "data/opencode/opencode.db-wal", "data/opencode/opencode.db-shm":
		default:
			continue
		}
		if entry.Directory || ctx.Err() != nil {
			return sessionUncertain()
		}
		if err := copyCheckpointDatabaseFile(ctx, root, target, entry); err != nil {
			return err
		}
		database = database || entry.Path == "data/opencode/opencode.db"
	}
	if !database || security.SyncParent(filepath.Join(destination, "opencode.db")) != nil {
		return sessionUncertain()
	}
	return nil
}

func copyCheckpointDatabaseFile(ctx context.Context, source, target *os.Root, entry checkpointFile) error {
	before, err := source.Lstat(entry.Path)
	if err != nil || !before.Mode().IsRegular() || before.Size() != entry.Size || uint32(before.Mode().Perm()) != entry.Mode {
		return sessionUncertain()
	}
	input, err := source.OpenFile(entry.Path, checkpointReadFlags(), 0)
	if err != nil {
		return sessionUncertain()
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !sameCheckpointFile(before, opened) || !ownedCheckpointOpenFile(input) {
		return sessionUncertain()
	}
	name := filepath.Base(entry.Path)
	output, err := target.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return sessionUncertain()
	}
	defer output.Close()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(checkpointReader{ctx, input}, entry.Size+1))
	after, statErr := input.Stat()
	pathAfter, pathErr := source.Lstat(entry.Path)
	if err != nil || statErr != nil || pathErr != nil || count != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 || !sameCheckpointFile(before, after) || !sameCheckpointFile(before, pathAfter) || !ownedCheckpointOpenFile(input) || ctx.Err() != nil {
		return sessionUncertain()
	}
	if output.Sync() != nil || output.Close() != nil {
		return sessionUncertain()
	}
	return nil
}
