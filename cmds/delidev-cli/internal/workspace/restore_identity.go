// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// Directory identities survive ordinary commits, config/index rewrites and file
// edits, but never adopt a replacement workspace or Git store at the same path.
// Only a newly published snapshot or restore may capture them; legacy proofs
// retain their previous verification requirements.
func restoredDirectoryIdentity(root string, manifest Manifest) (string, error) {
	paths := []string{root}
	if len(manifest.Repositories) == 0 {
		paths = append(paths, filepath.Join(root, "chat"))
	}
	for _, repo := range manifest.Repositories {
		path := filepath.Join(root, string(repo.ID))
		paths = append(paths, path, filepath.Join(path, ".git"))
	}
	identities := make([]string, 0, len(paths))
	for _, path := range paths {
		identity, err := directoryPathIdentity(path)
		if err != nil {
			return "", err
		}
		identities = append(identities, identity)
	}
	raw, _ := json.Marshal(identities)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

type directoryIdentityStage string

const (
	identityNamedShape directoryIdentityStage = "named-shape"
	identityCanonical  directoryIdentityStage = "canonical-path"
	identityOpen       directoryIdentityStage = "open-original"
	identityOpenedStat directoryIdentityStage = "opened-stat"
	identityNative     directoryIdentityStage = "native-file-identity"
	identityNamedStat  directoryIdentityStage = "named-stat"
	identityMismatch   directoryIdentityStage = "original-identity-mismatch"
)

func directoryIdentityRejected(stage directoryIdentityStage) error {
	// The private diagnostic identifies a closed predicate, never a path or
	// self-reported identity. It grants no adoption or cleanup authority.
	slog.Warn("workspace_directory_identity_rejected", "stage", stage)
	return ResultUncertain()
}
func directoryPathIdentity(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return "", directoryIdentityRejected(identityNamedShape)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", directoryIdentityRejected(identityCanonical)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", directoryIdentityRejected(identityOpen)
	}
	opened, statErr := file.Stat()
	identity, identityErr := directoryFileIdentity(file)
	file.Close()
	after, namedErr := os.Lstat(path)
	if statErr != nil {
		return "", directoryIdentityRejected(identityOpenedStat)
	}
	if identityErr != nil {
		return "", directoryIdentityRejected(identityNative)
	}
	if namedErr != nil {
		return "", directoryIdentityRejected(identityNamedStat)
	}
	if !os.SameFile(before, opened) || !os.SameFile(opened, after) || after.Mode()&os.ModeSymlink != 0 {
		return "", directoryIdentityRejected(identityMismatch)
	}
	return identity, nil
}

// Publication captures the original managed directories without reading mutable
// files or external Git stores. Recovery can settle a verified snapshot after
// later unsupported user edits, while replacement source directories stay foreign.
func sourceWorkspaceDirectoryIdentity(root string, manifest Manifest) (string, error) {
	paths := []string{root}
	if len(manifest.Repositories) == 0 {
		paths = append(paths, filepath.Join(root, "chat"))
	}
	for _, repo := range manifest.Repositories {
		paths = append(paths, filepath.Join(root, string(repo.ID)))
	}
	identities := make([]string, 0, len(paths))
	for _, path := range paths {
		identity, err := directoryPathIdentity(path)
		if err != nil {
			return "", err
		}
		identities = append(identities, identity)
	}
	raw, _ := json.Marshal(identities)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
