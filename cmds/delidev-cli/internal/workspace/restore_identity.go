// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Directory identities survive ordinary commits, config/index rewrites and file
// edits, but never adopt a replacement workspace or Git store at the same path.
// Only a newly published restore may capture them; legacy proofs stay uncertain.
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

func directoryPathIdentity(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return "", ResultUncertain()
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", ResultUncertain()
	}
	file, err := os.Open(path)
	if err != nil {
		return "", ResultUncertain()
	}
	opened, statErr := file.Stat()
	identity, identityErr := directoryFileIdentity(file)
	file.Close()
	after, namedErr := os.Lstat(path)
	if statErr != nil || identityErr != nil || namedErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) || after.Mode()&os.ModeSymlink != 0 {
		return "", ResultUncertain()
	}
	return identity, nil
}
