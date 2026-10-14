package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ownedRuntimeFiles are the DeliDev-written files of one run inside a
// persistent OpenCode profile. They are rewritten for every run so a previous
// run's instructions or workspace references cannot leak into the next one.
// Native history, caches, logs and helper files are never in this list.
var ownedRuntimeFiles = []string{"instructions.txt", "opencode/opencode.json"}

// ownedRuntimeTrees are DeliDev-owned directories that are rebuilt per run: the
// exported snapshot archive and the native scratch directory.
var ownedRuntimeTrees = []string{checkpointSnapshotArchive}

// retireOwnedRuntimeFiles removes the per-run DeliDev-owned files and the
// scratch contents of a persistent profile before another run starts. It is
// anchored to the profile root and never follows links. It does not delete the
// profile, its native history or its caches.
func retireOwnedRuntimeFiles(ctx context.Context, home string) error {
	if !canonicalDirectory(home) || security.CheckPrivateDir(home) != nil {
		return sessionUncertain()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return sessionUncertain()
	}
	defer root.Close()
	for _, name := range ownedRuntimeFiles {
		if ctx.Err() != nil {
			return unavailable()
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || root.Remove(name) != nil {
			return sessionUncertain()
		}
	}
	for _, name := range ownedRuntimeTrees {
		if ctx.Err() != nil {
			return unavailable()
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || root.RemoveAll(name) != nil {
			return sessionUncertain()
		}
	}
	// Native scratch files (for example extracted libraries) are transient and
	// would otherwise accumulate for every run of a persistent profile.
	scratch, err := root.OpenRoot("tmp")
	if err != nil {
		return sessionUncertain()
	}
	defer scratch.Close()
	directory, err := scratch.Open(".")
	if err != nil {
		return sessionUncertain()
	}
	names, err := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		return sessionUncertain()
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return unavailable()
		}
		if scratch.RemoveAll(name) != nil {
			return sessionUncertain()
		}
	}
	if security.SyncParent(filepath.Join(home, "tmp", "x")) != nil {
		return sessionUncertain()
	}
	return nil
}

// resetNativeSnapshot removes the native snapshot repositories of the profile.
// A profile serves one conversation, so only the checkpoint's project is
// rebuilt from its verified self-contained archive; the parent directories are
// recreated private exactly as a copied runtime receives them.
func resetNativeSnapshot(ctx context.Context, home string, source nativeCheckpoint) error {
	if !validCheckpointSnapshot(source) || !snapshotProjectComponent(source.Project) || ctx.Err() != nil {
		return sessionUncertain()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return sessionUncertain()
	}
	defer root.Close()
	name := "data/opencode/snapshot"
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || root.RemoveAll(name) != nil {
		return sessionUncertain()
	}
	return nil
}
