// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// forkConfigLayer is the ordered list of raw `git config --list -z` records of
// one Git configuration file. A record is "key\nvalue" or just "key" for a
// valueless boolean. Keys are canonicalized by Git, so the comparison ignores
// formatting, comments and section spelling but keeps order and multiple values.
type forkConfigLayer struct {
	present bool
	records []string
}

const (
	// A retained record is a string header pointing into the command output buffer.
	forkConfigRecordHeaderBytes = uint64(unsafe.Sizeof(string("")))
	// Each retained layer also keeps its []string slice header.
	forkConfigSliceHeaderBytes = uint64(unsafe.Sizeof([]string{}))
)

// forkConfigProof is the expected-config evidence of one independent Fork. The
// source layers are frozen when the Fork source is inspected; the accepted remote
// is bound from the RepositorySpec. The mutable child is only ever compared to it.
type forkConfigProof struct {
	config, worktree      forkConfigLayer
	remoteName, remoteURL string
	retainedBytes         uint64
	bound                 bool
}

// sliceHeaderBytes is the part of retainedBytes that a holder reusing this
// proof's parsed buffers still owns itself: one []string header per present layer.
func (p forkConfigProof) sliceHeaderBytes() uint64 {
	var total uint64
	for _, layer := range []forkConfigLayer{p.config, p.worktree} {
		if layer.present {
			total += forkConfigSliceHeaderBytes
		}
	}
	return total
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

// splitForkConfigRecords retains each NUL-delimited record as a string backed by
// the command output buffer. The buffer remains live through the returned
// strings, so parsing does not create a second copy of the complete output.
func splitForkConfigRecords(out []byte, recordsCount int) []string {
	records := make([]string, 0, recordsCount)
	start := 0
	for index, value := range out {
		if value != 0 {
			continue
		}
		records = append(records, unsafe.String(unsafe.SliceData(out[start:index]), index-start))
		start = index + 1
	}
	return records
}

// listForkConfigFile lists one configuration file without following includes.
// The output must fit the aggregate Fork allowance for retained config records.
func listForkConfigFile(ctx context.Context, git Git, dir, path string, allowance uint64) ([]string, uint64, error) {
	out, _, err := git.runCommandWithOutputLimit(ctx, dir, int(min(allowance, maxForkBytes)), "config", "--no-includes", "--file", path, "--list", "-z")
	if err != nil {
		return nil, 0, err
	}
	if len(out) > 0 && out[len(out)-1] != 0 {
		return nil, 0, forkSnapshotChanged()
	}
	recordsCount := 0
	for _, value := range out {
		if value != 0 {
			continue
		}
		recordsCount++
		if recordsCount > maxForkEntries {
			return nil, 0, domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
		}
	}
	// The retained proof is the complete backing allocation plus the record and
	// slice headers. Reject before allocating the record slice when that total
	// cannot fit the caller's remaining allowance.
	proofBytes, err := forkConfigProofBytes(uint64(cap(out)), recordsCount)
	if err != nil {
		return nil, 0, err
	}
	if proofBytes > allowance {
		return nil, 0, domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
	}
	// splitForkConfigRecords keeps strings backed by out. Charge the complete
	// backing allocation because bytes.Buffer may retain spare capacity beyond
	// the logical output length while those strings keep the buffer alive.
	return splitForkConfigRecords(out, recordsCount), uint64(cap(out)), nil
}

// forkConfigProofBytes estimates the retained parsed representation: the
// command output bytes, one string header per record, and the slice header.
// The raw config bytes are charged separately by snapshotCopyBudget.take.
func forkConfigProofBytes(size uint64, records int) (uint64, error) {
	if records < 0 {
		return 0, domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
	}
	overhead := forkConfigSliceHeaderBytes
	if uint64(records) > (^uint64(0)-overhead)/forkConfigRecordHeaderBytes {
		return 0, domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
	}
	overhead += uint64(records) * forkConfigRecordHeaderBytes
	if size > ^uint64(0)-overhead {
		return 0, domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
	}
	return size + overhead, nil
}

func chargeForkConfigProof(budget *snapshotCopyBudget, layer forkConfigLayer, size uint64) error {
	if !layer.present {
		return nil
	}
	proofBytes, err := forkConfigProofBytes(size, len(layer.records))
	if err != nil {
		return err
	}
	return budget.takeBytes(proofBytes)
}

func readForkConfigBytes(ctx context.Context, file *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(forkReader{ctx, io.LimitReader(file, limit)})
}

// readFrozenForkConfig reads a source config file named by the inventory. The
// file bytes must match the inventory digest before and after Git parses them, so
// the parsed list describes exactly the inventoried source content.
func readFrozenForkConfig(ctx context.Context, git Git, checkout, path string, entry snapshotEntry, allowance uint64) (forkConfigLayer, uint64, error) {
	if os.FileMode(entry.Mode)&os.ModeType != 0 {
		return forkConfigLayer{}, 0, forkUnsupported()
	}
	matches := func() (bool, error) {
		file, err := os.Open(path)
		if err != nil {
			return false, err
		}
		defer file.Close()
		// Inventory bounded entry.Size; a changed file cannot force an
		// unbounded read before the digest comparison reports drift.
		raw, err := readForkConfigBytes(ctx, file, int64(entry.Size)+1)
		if err != nil {
			return false, err
		}
		sum := sha256.Sum256(raw)
		return uint64(len(raw)) == entry.Size && hex.EncodeToString(sum[:]) == entry.SHA256, nil
	}
	if ok, err := matches(); err != nil || !ok {
		if err != nil {
			return forkConfigLayer{}, 0, err
		}
		return forkConfigLayer{}, 0, forkSnapshotChanged()
	}
	records, size, err := listForkConfigFile(ctx, git, checkout, path, allowance)
	if err != nil {
		return forkConfigLayer{}, 0, err
	}
	if ok, err := matches(); err != nil || !ok {
		if err != nil {
			return forkConfigLayer{}, 0, err
		}
		return forkConfigLayer{}, 0, forkSnapshotChanged()
	}
	return forkConfigLayer{present: true, records: records}, size, nil
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
func (i *forkGitInventory) captureConfigProof(ctx context.Context, git Git, checkout string, budget *snapshotCopyBudget) error {
	for _, layer := range []struct {
		name   string
		target *forkConfigLayer
	}{{"config", &i.configProof.config}, {"config.worktree", &i.configProof.worktree}} {
		path, entry, ok := i.sourceFile(layer.name)
		if !ok {
			continue
		}
		allowance := budget.bytes
		if entry.Size > ^uint64(0)-allowance {
			return domain.Fail(domain.ResourceExhausted, "The complete workspace snapshot exceeds its bound.", "Reduce workspace data before retrying; sources remain intact.")
		}
		// Keep the raw config charge in the shared copy budget. The parsed proof
		// consumes the remaining shared allowance for its output and record headers.
		frozen, size, err := readFrozenForkConfig(ctx, git, checkout, path, entry, allowance)
		if err != nil {
			return err
		}
		if err := chargeForkConfigProof(budget, frozen, size); err != nil {
			return err
		}
		proofBytes, err := forkConfigProofBytes(size, len(frozen.records))
		if err != nil {
			return err
		}
		i.configProof.retainedBytes += proofBytes
		*layer.target = frozen
	}
	return nil
}

// readChildForkConfig lists a copied config file; a missing file is an absent layer.
func readChildForkConfig(ctx context.Context, git Git, target, name string, allowance uint64) (forkConfigLayer, uint64, error) {
	path := filepath.Join(target, ".git", name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return forkConfigLayer{}, 0, nil
	}
	if err != nil {
		return forkConfigLayer{}, 0, err
	}
	if !info.Mode().IsRegular() {
		return forkConfigLayer{}, 0, forkSnapshotChanged()
	}
	records, size, err := listForkConfigFile(ctx, git, target, path, allowance)
	if err != nil {
		return forkConfigLayer{}, 0, err
	}
	return forkConfigLayer{present: true, records: records}, size, nil
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
	// Compare the two filtered record sequences in place. Allocating filtered
	// copies would add comparison scratch that the remaining verification
	// allowance does not account for.
	retained := func(record string) bool {
		key := forkConfigKey(record)
		_, ok := normalized[key]
		return !ok && !slices.Contains(removed, key)
	}
	next := func(records []string, index int) int {
		for index < len(records) && !retained(records[index]) {
			index++
		}
		return index
	}
	i, j := 0, 0
	for {
		i, j = next(l.records, i), next(child.records, j)
		if i == len(l.records) || j == len(child.records) {
			break
		}
		if l.records[i] != child.records[j] {
			return false
		}
		i, j = i+1, j+1
	}
	if i != len(l.records) || j != len(child.records) {
		return false
	}
	for _, key := range removed {
		if child.hasKey(key) {
			return false
		}
	}
	for key, value := range normalized {
		count, exact := 0, false
		for _, record := range child.records {
			if forkConfigKey(record) == key {
				count++
				exact = len(record) == len(key)+1+len(value) && record[len(key)+1:] == value
			}
		}
		if count != 1 || !exact {
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
func forkChildConfigError(err error) error {
	if domain.SafeError(err).Code == domain.ResourceExhausted {
		return forkSnapshotChanged()
	}
	return err
}

func (i forkGitInventory) verifyChildConfig(ctx context.Context, git Git, target string, budget *snapshotCopyBudget) error {
	proof := i.configProof
	if !proof.bound {
		return ResultUncertain()
	}
	core := map[string]string{"core.bare": "false", "core.worktree": ".."}
	config, size, err := readChildForkConfig(ctx, git, target, "config", budget.bytes)
	if err != nil {
		return forkChildConfigError(err)
	}
	if err := chargeForkConfigProof(budget, config, size); err != nil {
		return forkChildConfigError(err)
	}
	// Git recreates a missing source config, so config is always expected.
	if !config.present || !proof.config.matchesNormalized(config, map[string]string{
		"core.bare": core["core.bare"], "core.worktree": core["core.worktree"],
		forkRemoteURLKey(proof.remoteName): proof.remoteURL,
	}) {
		return forkSnapshotChanged()
	}
	worktree, size, err := readChildForkConfig(ctx, git, target, "config.worktree", budget.bytes)
	if err != nil {
		return forkChildConfigError(err)
	}
	if err := chargeForkConfigProof(budget, worktree, size); err != nil {
		return forkChildConfigError(err)
	}
	if worktree.present != proof.worktree.present || worktree.present && !proof.worktree.matchesNormalized(worktree, core, forkRemoteURLKey(proof.remoteName)) {
		return forkSnapshotChanged()
	}
	return nil
}
