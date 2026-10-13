// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// forkConfigLayer is the ordered list of raw `git config --list -z` records of
// one Git configuration file. A record is "key\nvalue" or just "key" for a
// valueless boolean. Keys are canonicalized by Git, so the comparison ignores
// formatting, comments and section spelling but keeps order and multiple values.
type forkConfigLayer struct {
	present bool
	records []string
}

// forkConfigProof is the expected-config evidence of one independent Fork. The
// source layers are frozen when the Fork source is inspected; the accepted remote
// is bound from the RepositorySpec. The mutable child is only ever compared to it.
type forkConfigProof struct {
	config, worktree      forkConfigLayer
	remoteName, remoteURL string
	bound                 bool
}

// forkRemoteName is the remote whose URL Fork preparation rewrites.
func forkRemoteName(spec RepositorySpec) string {
	if spec.PreferredRemote == "" {
		return "origin"
	}
	return spec.PreferredRemote
}

// bindRemote records the accepted remote selection for the expected child config.
func (i *forkGitInventory) bindRemote(spec RepositorySpec) {
	i.configProof.remoteName, i.configProof.remoteURL, i.configProof.bound = forkRemoteName(spec), spec.RemoteURL, true
}

func forkConfigKey(record string) string {
	key, _, _ := strings.Cut(record, "\n")
	return key
}

// listForkConfigFile lists one configuration file without following includes.
func listForkConfigFile(ctx context.Context, git Git, dir, path string) ([]string, error) {
	out, err := git.run(ctx, dir, "config", "--no-includes", "--file", path, "--list", "-z")
	if err != nil {
		return nil, err
	}
	records := strings.Split(string(out), "\x00")
	if len(records) > 0 && records[len(records)-1] == "" {
		records = records[:len(records)-1]
	}
	return records, nil
}

// readFrozenForkConfig reads a source config file named by the inventory. The
// file bytes must match the inventory digest before and after Git parses them, so
// the parsed list describes exactly the inventoried source content.
func readFrozenForkConfig(ctx context.Context, git Git, checkout, path string, entry snapshotEntry) (forkConfigLayer, error) {
	if os.FileMode(entry.Mode)&os.ModeType != 0 || entry.Size > MaxGitOutput {
		return forkConfigLayer{}, forkUnsupported()
	}
	matches := func() (bool, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		sum := sha256.Sum256(raw)
		return uint64(len(raw)) == entry.Size && hex.EncodeToString(sum[:]) == entry.SHA256, nil
	}
	if ok, err := matches(); err != nil || !ok {
		if err != nil {
			return forkConfigLayer{}, err
		}
		return forkConfigLayer{}, forkSnapshotChanged()
	}
	records, err := listForkConfigFile(ctx, git, checkout, path)
	if err != nil {
		return forkConfigLayer{}, err
	}
	if ok, err := matches(); err != nil || !ok {
		if err != nil {
			return forkConfigLayer{}, err
		}
		return forkConfigLayer{}, forkSnapshotChanged()
	}
	return forkConfigLayer{present: true, records: records}, nil
}

// sourceFile resolves which source file becomes the child's file with this
// name: the selected linked-worktree administration overlays the common store.
func (i forkGitInventory) sourceFile(name string) (string, snapshotEntry, bool) {
	if !sameNativePath(i.common, i.admin) {
		for _, e := range i.adminFiles.Entries {
			if e.Path == name {
				return filepath.Join(i.admin, filepath.FromSlash(name)), e, true
			}
		}
	}
	for _, e := range i.commonFiles.Entries {
		if e.Path == name {
			return filepath.Join(i.common, filepath.FromSlash(name)), e, true
		}
	}
	return "", snapshotEntry{}, false
}

// captureConfigProof freezes both retained config layers from the source.
func (i *forkGitInventory) captureConfigProof(ctx context.Context, git Git, checkout string) error {
	for _, layer := range []struct {
		name   string
		target *forkConfigLayer
	}{{"config", &i.configProof.config}, {"config.worktree", &i.configProof.worktree}} {
		path, entry, ok := i.sourceFile(layer.name)
		if !ok {
			continue
		}
		frozen, err := readFrozenForkConfig(ctx, git, checkout, path, entry)
		if err != nil {
			return err
		}
		*layer.target = frozen
	}
	return nil
}

// readChildForkConfig lists a copied config file; a missing file is an absent layer.
func readChildForkConfig(ctx context.Context, git Git, target, name string) (forkConfigLayer, error) {
	path := filepath.Join(target, ".git", name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return forkConfigLayer{}, nil
	}
	if err != nil {
		return forkConfigLayer{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxGitOutput {
		return forkConfigLayer{}, forkSnapshotChanged()
	}
	records, err := listForkConfigFile(ctx, git, target, path)
	if err != nil {
		return forkConfigLayer{}, err
	}
	return forkConfigLayer{present: true, records: records}, nil
}

// forkRemoteURLKey is the canonical Git config key of the selected remote URL.
func forkRemoteURLKey(remoteName string) string {
	return "remote." + remoteName + ".url"
}

// hasKey reports whether the layer holds at least one record for key.
func (l forkConfigLayer) hasKey(key string) bool {
	return slices.ContainsFunc(l.records, func(record string) bool { return forkConfigKey(record) == key })
}

// matchesNormalized reports whether the child layer is the source layer after the
// accepted normalization: every record except the normalized and removed keys is
// unchanged and in its original order, each normalized key has exactly its
// accepted value, and each removed key has no record at all.
func (l forkConfigLayer) matchesNormalized(child forkConfigLayer, normalized map[string]string, removed ...string) bool {
	rest := func(records []string) []string {
		result := []string{}
		for _, record := range records {
			key := forkConfigKey(record)
			if _, ok := normalized[key]; !ok && !slices.Contains(removed, key) {
				result = append(result, record)
			}
		}
		return result
	}
	if !slices.Equal(rest(l.records), rest(child.records)) {
		return false
	}
	for _, key := range removed {
		if child.hasKey(key) {
			return false
		}
	}
	for key, value := range normalized {
		got := []string{}
		for _, record := range child.records {
			if forkConfigKey(record) == key {
				got = append(got, record)
			}
		}
		if len(got) != 1 || got[0] != key+"\n"+value {
			return false
		}
	}
	return true
}

// verifyChildConfig requires the copied config files to equal the frozen source
// files plus only the accepted normalization (core.bare=false and core.worktree=..
// in each retained layer, the selected remote URL in config, and no selected
// remote URL in config.worktree, which Git reads after config and would
// otherwise keep the stale source endpoint effective).
func (i forkGitInventory) verifyChildConfig(ctx context.Context, git Git, target string) error {
	proof := i.configProof
	if !proof.bound {
		return ResultUncertain()
	}
	core := map[string]string{"core.bare": "false", "core.worktree": ".."}
	config, err := readChildForkConfig(ctx, git, target, "config")
	if err != nil {
		return err
	}
	// Git recreates a missing source config, so config is always expected.
	if !config.present || !proof.config.matchesNormalized(config, map[string]string{
		"core.bare": core["core.bare"], "core.worktree": core["core.worktree"],
		forkRemoteURLKey(proof.remoteName): proof.remoteURL,
	}) {
		return forkSnapshotChanged()
	}
	worktree, err := readChildForkConfig(ctx, git, target, "config.worktree")
	if err != nil {
		return err
	}
	if worktree.present != proof.worktree.present || worktree.present && !proof.worktree.matchesNormalized(worktree, core, forkRemoteURLKey(proof.remoteName)) {
		return forkSnapshotChanged()
	}
	return nil
}
