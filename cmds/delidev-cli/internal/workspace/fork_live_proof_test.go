// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func syntheticProofCopy(source string, retained uint64) forkCopy {
	return forkCopy{source: source, git: true, gitInventory: &forkGitInventory{configProof: forkConfigProof{retainedBytes: retained}}}
}

func setForkVerifyByteLimit(t *testing.T, limit uint64) {
	t.Helper()
	previous := forkVerifyByteLimit
	forkVerifyByteLimit = limit
	t.Cleanup(func() { forkVerifyByteLimit = previous })
}

func TestForkLiveConfigProofBytesSumsEveryRepository(t *testing.T) {
	snapshot := []forkCopy{syntheticProofCopy("/a", 100), syntheticProofCopy("/b", 200), {source: "/chat"}, syntheticProofCopy("/c", 300)}
	// Prepared copies of snapshot sources share the snapshot's parsed buffers.
	prepared := []forkCopy{syntheticProofCopy("/a", 100), syntheticProofCopy("/b", 200)}
	// A source that has no snapshot proof holds its own freshly parsed proof.
	fresh := []forkCopy{syntheticProofCopy("/d", 400)}
	total, err := forkLiveConfigProofBytes(snapshot, prepared, fresh)
	if err != nil || total != 1000 {
		t.Fatalf("live proof total = %d, %v; want 1000", total, err)
	}
	total, err = forkLiveConfigProofBytes(nil, prepared, fresh)
	if err != nil || total != 700 {
		t.Fatalf("live proof total without snapshot = %d, %v; want 700", total, err)
	}
	total, err = forkLiveConfigProofBytes([]forkCopy{syntheticProofCopy("/same", 5), syntheticProofCopy("/same", 7)})
	if err != nil || total != 12 {
		t.Fatalf("copies inside one group must be counted separately: %d, %v", total, err)
	}
}

func TestForkLiveConfigProofBytesRejectsOverflow(t *testing.T) {
	_, err := forkLiveConfigProofBytes([]forkCopy{syntheticProofCopy("/a", ^uint64(0)), syntheticProofCopy("/b", 1)})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("proof total overflow was not a snapshot conflict: %v", err)
	}
}

func TestNewForkVerifyBudgetKeepsLimitAndDeductsLiveProofs(t *testing.T) {
	budget, err := newForkVerifyBudget(0)
	if err != nil || budget.bytes != maxForkBytes-4096 || budget.entries != maxForkEntries {
		t.Fatalf("verification limit changed: %+v, %v", budget, err)
	}
	budget, err = newForkVerifyBudget(maxForkBytes - 4096)
	if err != nil || budget.bytes != 0 {
		t.Fatalf("exact allowance was not admitted: %+v, %v", budget, err)
	}
	if _, err = newForkVerifyBudget(maxForkBytes - 4095); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("one byte above the allowance was not a conflict: %v", err)
	}
}

// childConfigNeeds returns the retained proof bytes of the copied config and
// config.worktree layers, which verification charges against the allowance.
func childConfigNeeds(t *testing.T, git Git, target string) (uint64, uint64) {
	t.Helper()
	var needs []uint64
	for _, name := range []string{"config", "config.worktree"} {
		layer, size, err := readChildForkConfig(context.Background(), git, target, name, maxForkBytes)
		if err != nil || !layer.present {
			t.Fatalf("fixture child %s is missing: %v", name, err)
		}
		need, err := forkConfigProofBytes(size, len(layer.records))
		if err != nil {
			t.Fatal(err)
		}
		needs = append(needs, need)
	}
	return needs[0], needs[1]
}

func TestListForkConfigFileRejectsBeforeParsingAtProofBoundary(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	path := filepath.Join(proof.target, ".git", "config")
	records, size, err := listForkConfigFile(context.Background(), git, proof.target, path, maxForkBytes)
	if err != nil {
		t.Fatal(err)
	}
	need, err := forkConfigProofBytes(size, len(records))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		allowance uint64
		code      domain.Code
	}{{"one byte below", need - 1, domain.ResourceExhausted}, {"exact", need, ""}, {"one byte above", need + 1, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, _, err := listForkConfigFile(context.Background(), git, proof.target, path, tc.allowance)
			if tc.code == "" {
				if err != nil || len(parsed) == 0 {
					t.Fatalf("allowance %d rejected a fitting layer: %v", tc.allowance, err)
				}
				return
			}
			if domain.SafeError(err).Code != tc.code || parsed != nil {
				t.Fatalf("allowance %d did not reject before parsing: %v", tc.allowance, err)
			}
		})
	}
}

func TestVerifyChildConfigBoundsBothLayersByRemainingAllowance(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	config, worktree := childConfigNeeds(t, git, proof.target)
	for _, tc := range []struct {
		name  string
		bytes uint64
		code  domain.Code
	}{
		{"first layer one byte below", config - 1, domain.Conflict},
		{"both layers one byte below", config + worktree - 1, domain.Conflict},
		{"both layers exact", config + worktree, ""},
		{"both layers one byte above", config + worktree + 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := proof.gitInventory.verifyChildConfig(context.Background(), git, proof.target, &snapshotCopyBudget{bytes: tc.bytes, entries: maxForkEntries})
			if tc.code == "" {
				if err != nil {
					t.Fatalf("fitting child layers were rejected: %v", err)
				}
				return
			}
			if domain.SafeError(err).Code != tc.code {
				t.Fatalf("oversized child layers were not mapped to a snapshot conflict: %v", err)
			}
		})
	}
}

func TestVerifyChildConfigChargesFirstLayerBeforeReadingSecond(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	config, worktree := childConfigNeeds(t, git, proof.target)
	budget := &snapshotCopyBudget{bytes: config + worktree - 1, entries: maxForkEntries}
	err := proof.gitInventory.verifyChildConfig(context.Background(), git, proof.target, budget)
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("second layer beyond the remaining allowance was not a conflict: %v", err)
	}
	// Only the first layer was admitted; the second was rejected uncharged.
	if budget.bytes != worktree-1 {
		t.Fatalf("remaining allowance = %d, want %d after the first layer", budget.bytes, worktree-1)
	}
}

func TestIndependentForkVerifyDeductsEverySiblingProof(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	config, worktree := childConfigNeeds(t, git, proof.target)
	own := proof.gitInventory.configProof.retainedBytes
	// The target Git directory is charged to the same allowance first.
	probe, err := newForkVerifyBudget(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = walkSnapshotBudget(context.Background(), filepath.Join(proof.target, ".git"), "", nil, maxForkEntries, probe); err != nil {
		t.Fatal(err)
	}
	used := maxForkBytes - 4096 - probe.bytes
	const siblings = uint64(1 << 20)
	for _, tc := range []struct {
		name   string
		delta  int64
		live   uint64
		accept bool
	}{
		{"siblings plus child one byte below", -1, own + siblings, false},
		{"siblings plus child exact", 0, own + siblings, true},
		{"siblings plus child one byte above", 1, own + siblings, true},
		{"ignoring siblings would have admitted the same child", -1, own, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := int64(4096+used+own+siblings+config+worktree) + tc.delta
			setForkVerifyByteLimit(t, uint64(limit))
			err := proof.gitInventory.verify(context.Background(), git, source, proof.target, tc.live)
			if tc.accept && err != nil {
				t.Fatalf("fitting verification was rejected: %v", err)
			}
			if !tc.accept && domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("verification beyond the live proof allowance was not a conflict: %v", err)
			}
		})
	}
}

func TestIndependentForkVerifyRejectsLiveProofsBelowOwnProof(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	own := proof.gitInventory.configProof.retainedBytes
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, own-1); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("a live total that omits the verified proof was accepted: %v", err)
	}
}

func TestPrepareForkGitInventoryDeductsSnapshotSiblingProofs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		siblings []uint64
		accept   bool
	}{
		{"no sibling", nil, true},
		{"one small sibling", []uint64{1 << 10}, true},
		{"three expanding siblings", []uint64{(maxForkBytes - 4096) / 3, (maxForkBytes - 4096) / 3, (maxForkBytes - 4096) / 3}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := forkInventoryFixtureInputWith(t, false, retainedConfigSetup)
			inventory, err := inspectForkGitInventory(context.Background(), input.git, input.selected, newForkCopyBudget(1))
			if err != nil {
				t.Fatal(err)
			}
			inventory.bindRemote(input.spec)
			snapshot := &ForkSnapshot{}
			for index, retained := range tc.siblings {
				snapshot.copies = append(snapshot.copies, syntheticProofCopy(filepath.Join("/sibling", string(rune('a'+index))), retained))
			}
			snapshot.copies = append(snapshot.copies, forkCopy{source: input.selected, git: true, gitInventory: &inventory})
			_, _, err = input.m.prepareForkGitInventory(context.Background(), input.git, input.spec, &input.manifest, func() error { return nil }, newForkCopyBudget(1), snapshot, nil)
			if tc.accept && err != nil {
				t.Fatalf("small Fork was rejected: %v", err)
			}
			if !tc.accept && domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("sibling proofs were not deducted before child parsing: %v", err)
			}
		})
	}
}

func TestPrepareForkGitInventoryDeductsEarlierPreparedProofs(t *testing.T) {
	input := forkInventoryFixtureInputWith(t, false, retainedConfigSetup)
	prior := []forkCopy{syntheticProofCopy("/earlier-a", maxForkBytes/2), syntheticProofCopy("/earlier-b", maxForkBytes/2)}
	_, _, err := input.m.prepareForkGitInventory(context.Background(), input.git, input.spec, &input.manifest, func() error { return nil }, newForkCopyBudget(1), nil, prior)
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("earlier prepared proofs were not deducted: %v", err)
	}
}

func TestForkSnapshotVerifyDeductsEverySiblingProof(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	snapshot := &ForkSnapshot{git: git, copies: []forkCopy{proof, syntheticProofCopy("/sibling", maxForkBytes)}}
	snapshot.copies[0].source = source
	child.Repositories = append(child.Repositories, child.Repositories[0])
	err := snapshot.Verify(context.Background(), child)
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("snapshot verification ignored the sibling proof: %v", err)
	}
}

func TestMatchesNormalizedAllocatesNoComparisonScratch(t *testing.T) {
	source := forkConfigLayer{present: true, records: []string{"a.b\none", "core.bare\ntrue", "a.c\ntwo", "remote.origin.url\nold"}}
	child := forkConfigLayer{present: true, records: []string{"a.b\none", "core.bare\nfalse", "a.c\ntwo", "remote.origin.url\nnew"}}
	normalized := map[string]string{"core.bare": "false", "remote.origin.url": "new"}
	if !source.matchesNormalized(child, normalized) {
		t.Fatal("accepted normalization was rejected")
	}
	if allocations := testing.AllocsPerRun(100, func() { source.matchesNormalized(child, normalized) }); allocations != 0 {
		t.Fatalf("comparison allocated %v times; scratch would escape the verification allowance", allocations)
	}
	reordered := forkConfigLayer{present: true, records: []string{"a.c\ntwo", "a.b\none", "core.bare\nfalse", "remote.origin.url\nnew"}}
	if source.matchesNormalized(reordered, normalized) {
		t.Fatal("reordered retained settings were accepted")
	}
	extra := forkConfigLayer{present: true, records: append(slices.Clone(child.records), "a.d\nthree")}
	if source.matchesNormalized(extra, normalized) {
		t.Fatal("an added setting was accepted")
	}
	if source.matchesNormalized(forkConfigLayer{present: true, records: child.records[:3]}, normalized) {
		t.Fatal("a missing normalized setting was accepted")
	}
}
