// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readForkFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err != nil {
		return nil
	}
	return raw
}

// retainedConfigSetup gives the source ordered retained settings in both config
// layers so the normalized child can be compared setting by setting.
func retainedConfigSetup(t *testing.T, source, selected string) {
	t.Helper()
	gitTest(t, source, "config", "--local", "fixture.first", "one")
	gitTest(t, source, "config", "--local", "fixture.second", "two")
	gitTest(t, source, "config", "--local", "extensions.worktreeConfig", "true")
	gitTest(t, selected, "config", "--worktree", "fixture.scoped", "scoped")
	gitTest(t, selected, "config", "--worktree", "fixture.other", "other")
}

func appendLargeForkConfig(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("[fixture]\n\tlarge = " + strings.Repeat("x", MaxGitOutput) + "\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendForkConfigRecords(t *testing.T, path string, count int) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "\n[cardinality]\n%s", strings.Repeat("\tentry = value\n", count)); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSplitForkConfigRecordsRetainsCommandBuffer(t *testing.T) {
	raw := []byte("fixture.first\none\x00fixture.second\ntwo\x00")
	records := splitForkConfigRecords(raw, 2)
	if len(records) != 2 {
		t.Fatalf("split returned %d records, want 2", len(records))
	}
	if unsafe.StringData(records[0]) != unsafe.SliceData(raw) {
		t.Fatal("first record does not retain the command output buffer")
	}
	if unsafe.StringData(records[1]) != unsafe.SliceData(raw[18:]) {
		t.Fatal("second record does not retain the command output buffer")
	}
}

func TestListForkConfigFileChargesRetainedBufferCapacity(t *testing.T) {
	input := forkInventoryFixtureInputWith(t, false, retainedConfigSetup)
	inventory, err := inspectForkGitInventory(context.Background(), input.git, input.selected, newForkCopyBudget(1))
	if err != nil {
		t.Fatal(err)
	}
	path, _, ok := inventory.sourceFile("config")
	if !ok {
		t.Fatal("source config is missing")
	}
	raw, _, err := input.git.runCommandWithOutputLimit(context.Background(), input.selected, maxForkBytes, "config", "--no-includes", "--file", path, "--list", "-z")
	if err != nil {
		t.Fatal(err)
	}
	_, size, err := listForkConfigFile(context.Background(), input.git, input.selected, path, maxForkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if size != uint64(cap(raw)) {
		t.Fatalf("retained config charge used logical length: got %d, want backing capacity %d", size, cap(raw))
	}
}

func TestIndependentForkInventoryAdmitsLargeConfigLayers(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "common config"
		if linked {
			name = "linked config.worktree"
		}
		t.Run(name, func(t *testing.T) {
			configName := "config"
			if linked {
				configName = "config.worktree"
			}
			m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, linked, func(t *testing.T, source, selected string) {
				retainedConfigSetup(t, source, selected)
				gitDir := gitTest(t, source, "rev-parse", "--absolute-git-dir")
				if linked {
					gitDir = gitTest(t, selected, "rev-parse", "--absolute-git-dir")
				}
				appendLargeForkConfig(t, filepath.Join(gitDir, configName))
			})
			git := m.Git
			git.OwnerID = child.SessionID
			childPath := filepath.Join(child.PrimaryPath, ".git", configName)
			if info, err := os.Stat(childPath); err != nil || info.Size() <= MaxGitOutput {
				t.Fatalf("child config did not cross the old limit: %v, %v", info, err)
			}
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
				t.Fatalf("large config lost its normalized ordered proof: %v", err)
			}
			original := readForkFixtureFile(t, childPath)
			gitTest(t, child.PrimaryPath, "config", "--file", childPath, "fixture.drift", "changed")
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("large child config drift accepted: %v", err)
			}
			if err := os.WriteFile(childPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			sourcePath, _, ok := proof.gitInventory.sourceFile(configName)
			if !ok {
				t.Fatal("source config is missing")
			}
			file, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
			if err = file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); domain.SafeError(err).Code != domain.Conflict {
				t.Fatalf("large source config drift accepted: %v", err)
			}
			if _, err := git.run(context.Background(), child.PrimaryPath, "config", "--no-includes", "--file", childPath, "--list", "-z"); err == nil {
				t.Fatal("generic Git command accepted output above 4 MiB")
			}
		})
	}
}

func TestIndependentForkInventoryRejectsLargeConfigOutsideBudgetOrGrammar(t *testing.T) {
	for _, mode := range []string{"aggregate overflow", "prohibited key", "invalid config"} {
		t.Run(mode, func(t *testing.T) {
			input := forkInventoryFixtureInputWith(t, false, func(t *testing.T, source, selected string) {
				path := filepath.Join(gitTest(t, source, "rev-parse", "--absolute-git-dir"), "config")
				appendLargeForkConfig(t, path)
				if mode == "prohibited key" {
					file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = file.WriteString("[include]\n\tpath = /tmp/forbidden\n"); err != nil {
						t.Fatal(err)
					}
					if err = file.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "invalid config" {
					file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = file.WriteString("invalid config line\n"); err != nil {
						t.Fatal(err)
					}
					if err = file.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
			budget := newForkCopyBudget(1)
			if mode == "aggregate overflow" {
				budget.bytes = MaxGitOutput
			}
			_, err := inspectForkGitInventory(context.Background(), input.git, input.selected, budget)
			if err == nil {
				t.Fatal("large config bypassed rejection")
			}
			if mode == "aggregate overflow" && domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatalf("aggregate overflow was misclassified: %v", err)
			}
			if mode == "prohibited key" && domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("prohibited key was misclassified: %v", err)
			}
		})
	}
}

func TestIndependentForkInventoryRejectsConfigRecordCardinalityOverflow(t *testing.T) {
	input := forkInventoryFixtureInputWith(t, false, func(t *testing.T, source, _ string) {
		path := filepath.Join(gitTest(t, source, "rev-parse", "--absolute-git-dir"), "config")
		appendForkConfigRecords(t, path, maxForkEntries)
	})
	_, err := inspectForkGitInventory(context.Background(), input.git, input.selected, newForkCopyBudget(1))
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("config record cardinality overflow was not rejected: %v", err)
	}
}

func TestIndependentForkInventoryRecheckMapsConfigCardinalityDriftToConflict(t *testing.T) {
	m, source, manifest, proof, _ := forkInventoryCopyFixture(t, false)
	path := filepath.Join(gitTest(t, source, "rev-parse", "--absolute-git-dir"), "config")
	appendForkConfigRecords(t, path, maxForkEntries)

	git := m.Git
	git.OwnerID = manifest.SessionID
	if err := proof.gitInventory.verify(context.Background(), git, source, proof.target, proof.gitInventory.configProof.retainedBytes); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("source config cardinality drift was misclassified: %v", err)
	}
}

func TestIndependentForkInventoryChargesRawAndParsedConfigInSharedBudget(t *testing.T) {
	input := forkInventoryFixtureInputWith(t, false, retainedConfigSetup)
	budget := newForkCopyBudget(1)
	inventory, err := inspectForkGitInventory(context.Background(), input.git, input.selected, budget)
	if err != nil {
		t.Fatal(err)
	}

	expected := maxForkBytes - 4096 - inventory.commonFiles.Bytes - inventory.adminFiles.Bytes
	for _, name := range []string{"config", "config.worktree"} {
		path, _, ok := inventory.sourceFile(name)
		if !ok {
			continue
		}
		records, size, err := listForkConfigFile(context.Background(), input.git, input.selected, path, maxForkBytes)
		if err != nil {
			t.Fatal(err)
		}
		proofBytes, err := forkConfigProofBytes(size, len(records))
		if err != nil {
			t.Fatal(err)
		}
		expected -= proofBytes
	}
	if budget.bytes != expected {
		t.Fatalf("shared budget did not charge raw and parsed config: got %d, want %d", budget.bytes, expected)
	}
}

func TestIndependentForkInventoryProvesRetainedConfig(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "common checkout"
		if linked {
			name = "linked worktree"
		}
		t.Run(name, func(t *testing.T) {
			m, source, child, proof, _ := forkInventoryCopyFixtureWith(t, linked, retainedConfigSetup)
			git := m.Git
			git.OwnerID = child.SessionID
			target := filepath.Join(child.PrimaryPath, ".git")
			sourceCommon := proof.gitInventory.common
			sourceConfig := readForkFixtureFile(t, filepath.Join(sourceCommon, "config"))
			sourceHead := gitTest(t, source, "rev-parse", "HEAD")
			if _, err := os.Lstat(filepath.Join(target, "config.worktree")); err != nil {
				t.Fatal("fixture has no retained config.worktree", err)
			}
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
				t.Fatal("accepted normalization was rejected", err)
			}
			config, worktree := filepath.Join(target, "config"), filepath.Join(target, "config.worktree")
			originalConfig, originalWorktree := readForkFixtureFile(t, config), readForkFixtureFile(t, worktree)
			restore := func() {
				t.Helper()
				if err := os.WriteFile(config, originalConfig, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(worktree, originalWorktree, 0600); err != nil {
					t.Fatal(err)
				}
			}
			mutations := []struct {
				name   string
				mutate func()
			}{
				{"remote URL changed", func() {
					gitTest(t, child.PrimaryPath, "config", "remote.origin.url", "https://example.invalid/other.git")
				}},
				{"second remote URL added", func() {
					gitTest(t, child.PrimaryPath, "config", "--add", "remote.origin.url", "https://example.invalid/extra.git")
				}},
				{"remote URL removed", func() { gitTest(t, child.PrimaryPath, "config", "--unset", "remote.origin.url") }},
				{"unrelated setting added", func() { gitTest(t, child.PrimaryPath, "config", "--local", "fixture.added", "x") }},
				{"retained setting changed", func() { gitTest(t, child.PrimaryPath, "config", "--local", "fixture.first", "changed") }},
				{"retained setting removed", func() { gitTest(t, child.PrimaryPath, "config", "--local", "--unset", "fixture.second") }},
				{"retained settings reordered", func() {
					gitTest(t, child.PrimaryPath, "config", "--local", "--unset", "fixture.first")
					gitTest(t, child.PrimaryPath, "config", "--local", "fixture.first", "one")
				}},
				{"core.worktree changed", func() { gitTest(t, child.PrimaryPath, "config", "--local", "core.worktree", ".") }},
				{"core.bare changed", func() { gitTest(t, child.PrimaryPath, "config", "--local", "core.bare", "true") }},
				{"worktree setting added", func() { gitTest(t, child.PrimaryPath, "config", "--file", worktree, "fixture.added", "x") }},
				{"worktree setting changed", func() { gitTest(t, child.PrimaryPath, "config", "--file", worktree, "fixture.scoped", "changed") }},
				{"worktree setting removed", func() { gitTest(t, child.PrimaryPath, "config", "--file", worktree, "--unset", "fixture.other") }},
				{"worktree core.worktree changed", func() { gitTest(t, child.PrimaryPath, "config", "--file", worktree, "core.worktree", source) }},
				{"worktree config removed", func() {
					if err := os.Remove(worktree); err != nil {
						t.Fatal(err)
					}
				}},
			}
			for _, mutation := range mutations {
				mutation.mutate()
				err := proof.verify(context.Background(), git, soloLiveProofBytes(proof))
				if err == nil || domain.SafeError(err).Code != domain.Conflict {
					t.Fatalf("%s accepted or misclassified: %v", mutation.name, err)
				}
				restore()
				if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
					t.Fatalf("%s: restored child rejected: %v", mutation.name, err)
				}
			}
			// The check never changes the source files, HEAD, index or content.
			if string(readForkFixtureFile(t, filepath.Join(sourceCommon, "config"))) != string(sourceConfig) || gitTest(t, source, "rev-parse", "HEAD") != sourceHead {
				t.Fatal("verification changed the source")
			}
		})
	}
}

// staleWorktreeRemoteSetup gives the selected checkout a worktree-scoped URL for
// the selected remote, which Git would read after the rewritten common URL.
func staleWorktreeRemoteSetup(t *testing.T, source, selected string) {
	t.Helper()
	retainedConfigSetup(t, source, selected)
	gitTest(t, selected, "config", "--worktree", "remote.origin.url", "https://example.invalid/stale-source.git")
}

func TestIndependentForkInventoryDropsStaleWorktreeRemoteURL(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "common checkout"
		if linked {
			name = "linked worktree"
		}
		t.Run(name, func(t *testing.T) {
			m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, linked, staleWorktreeRemoteSetup)
			git := m.Git
			git.OwnerID = child.SessionID
			worktree := filepath.Join(child.PrimaryPath, ".git", "config.worktree")
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
				t.Fatal("normalized child was rejected", err)
			}
			// Git must resolve exactly the selected URL, with no stale endpoint.
			if urls := gitTest(t, child.PrimaryPath, "config", "--get-all", "remote.origin.url"); urls != proof.gitInventory.configProof.remoteURL {
				t.Fatalf("child exposes remote URLs other than the selected one: %q", urls)
			}
			if records, _, err := listForkConfigFile(context.Background(), git, child.PrimaryPath, worktree, maxForkBytes); err != nil {
				t.Fatal(err)
			} else if (forkConfigLayer{present: true, records: records}).hasKey("remote.origin.url") {
				t.Fatal("child config.worktree retained the selected remote URL")
			}
			// Other worktree-scoped settings are still retained unchanged.
			if gitTest(t, child.PrimaryPath, "config", "--file", worktree, "fixture.scoped") != "scoped" {
				t.Fatal("unrelated worktree setting was not retained")
			}
			original := readForkFixtureFile(t, worktree)
			for _, stale := range []string{"https://example.invalid/stale-source.git", proof.gitInventory.configProof.remoteURL} {
				gitTest(t, child.PrimaryPath, "config", "--file", worktree, "remote.origin.url", stale)
				if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); domain.SafeError(err).Code != domain.Conflict {
					t.Fatalf("worktree-scoped remote URL %q accepted: %v", stale, err)
				}
				if err := os.WriteFile(worktree, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIndependentForkInventoryConfigWorktreeLayerFollowsSource(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixture(t, false)
	git := m.Git
	git.OwnerID = child.SessionID
	worktree := filepath.Join(child.PrimaryPath, ".git", "config.worktree")
	if _, err := os.Lstat(worktree); !os.IsNotExist(err) {
		t.Fatal("source without config.worktree produced one", err)
	}
	if err := os.WriteFile(worktree, []byte("[fixture]\n\tinjected = yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("child config.worktree absent from source accepted: %v", err)
	}
}

func TestIndependentForkInventoryConfigProofIsFrozenSourceNotChild(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixtureWith(t, false, retainedConfigSetup)
	git := m.Git
	git.OwnerID = child.SessionID
	// A source config edit invalidates the proof even if the child is unchanged.
	path := filepath.Join(proof.gitInventory.common, "config")
	original := readForkFixtureFile(t, path)
	gitTest(t, proof.source, "config", "--local", "fixture.first", "edited")
	if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("source config drift accepted: %v", err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := proof.verify(context.Background(), git, soloLiveProofBytes(proof)); err != nil {
		t.Fatal(err)
	}
	// An unbound proof has no accepted remote and fails closed.
	unbound := proof
	inventory := *proof.gitInventory
	inventory.configProof.bound = false
	unbound.gitInventory = &inventory
	if err := unbound.verify(context.Background(), git, soloLiveProofBytes(unbound)); err == nil {
		t.Fatal("proof without a remote selection accepted")
	}
	// A different accepted remote selection is not satisfied by this child.
	inventory = *proof.gitInventory
	inventory.configProof.remoteURL = "https://example.invalid/different.git"
	unbound.gitInventory = &inventory
	if err := unbound.verify(context.Background(), git, soloLiveProofBytes(unbound)); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("different accepted remote URL satisfied: %v", err)
	}
}

func TestForkChildConfigOverflowIsSnapshotConflict(t *testing.T) {
	err := forkChildConfigError(domain.Fail(domain.ResourceExhausted, "output exceeded", "retry"))
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("child config output overflow was not classified as snapshot conflict: %v", err)
	}
	if err := forkChildConfigError(context.Canceled); domain.SafeError(err).Code != domain.Canceled {
		t.Fatalf("child config cancellation was misclassified: %v", err)
	}
}

func TestReadFrozenForkConfigHonorsCancellation(t *testing.T) {
	m, _, child, proof, _ := forkInventoryCopyFixture(t, false)
	git := m.Git
	git.OwnerID = child.SessionID
	path, entry, ok := proof.gitInventory.sourceFile("config")
	if !ok {
		t.Fatal("source config is missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readFrozenForkConfig(ctx, git, proof.source, path, entry, maxForkBytes); domain.SafeError(err).Code != domain.Canceled {
		t.Fatalf("frozen config read ignored cancellation: %v", err)
	}
}

func TestForkSnapshotVerifyRejectsChildConfigDrift(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	m.Git.Executable = ""
	fork, err := m.ForkPreparation(context.Background(), manifest, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.InspectForkSnapshot(context.Background(), manifest, fork)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.PrepareFork(context.Background(), fork, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Verify(context.Background(), child); err != nil {
		t.Fatal("native-boundary verification rejected the prepared child", err)
	}
	remote := forkRemoteName(fork.Repositories[0])
	for _, mutate := range []func(){
		func() {
			gitTest(t, child.PrimaryPath, "config", "remote."+remote+".url", "https://example.invalid/other.git")
		},
		func() { gitTest(t, child.PrimaryPath, "config", "--local", "fixture.drift", "x") },
	} {
		path := filepath.Join(child.PrimaryPath, ".git", "config")
		original := readForkFixtureFile(t, path)
		mutate()
		if err := snapshot.Verify(context.Background(), child); domain.SafeError(err).Code != domain.Conflict {
			t.Fatalf("child config drift accepted at the native boundary: %v", err)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := snapshot.Verify(context.Background(), child); err != nil {
		t.Fatal(err)
	}
}

// soloLiveProofBytes is the live proof total for a copy verified without any
// sibling repository.
func soloLiveProofBytes(c forkCopy) uint64 {
	if c.gitInventory == nil {
		return 0
	}
	return c.gitInventory.configProof.retainedBytes
}
