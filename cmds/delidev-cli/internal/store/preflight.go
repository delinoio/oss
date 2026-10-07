// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// A read-only SQLite open may create or update shared-memory sidecars. Inspect
// a private copy when sidecars exist, so unsupported databases (including a
// committed schema marker in WAL) are rejected without changing source bytes.
// The server lock is held throughout this check and the subsequent live open.
func preflightDatabase(ctx context.Context, path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return storageError(err)
	}
	if err := security.RegularPrivate(path); err != nil {
		return storageError(err)
	}
	suffixes := []string{""}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return storageError(err)
		}
		suffixes = append(suffixes, suffix)
	}
	uri := databaseURI(path, true) + "&immutable=1"
	if len(suffixes) > 1 {
		dir, err := os.MkdirTemp("", "delidev-database-preflight-")
		if err != nil {
			return storageError(err)
		}
		defer os.RemoveAll(dir)
		copyPath := filepath.Join(dir, "state.sqlite")
		for _, suffix := range suffixes {
			if err := copyPreflightFile(ctx, path+suffix, copyPath+suffix); err != nil {
				return err
			}
		}
		uri = databaseURI(copyPath, false)
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return storageError(err)
	}
	defer db.Close()
	return inspect(ctx, db, false)
}

func copyPreflightFile(ctx context.Context, source, target string) error {
	if err := security.RegularPrivate(source); err != nil {
		return storageError(err)
	}
	in, err := os.Open(source)
	if err != nil {
		return storageError(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return storageError(err)
	}
	defer out.Close()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return storageError(err)
		}
		n, err := in.Read(buffer)
		if n > 0 {
			if _, writeErr := out.Write(buffer[:n]); writeErr != nil {
				return storageError(writeErr)
			}
		}
		if errors.Is(err, io.EOF) {
			return storageError(out.Close())
		}
		if err != nil {
			return storageError(err)
		}
	}
}
