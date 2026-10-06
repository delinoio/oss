//go:build darwin || linux

package runmoor

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func replaceFixtureDefinition(t *testing.T, f *reloadFixture, binary string) []byte {
	t.Helper()
	body, err := serviceDefinition(f.r.Platform, binary, f.path)
	if err != nil {
		t.Fatal(err)
	}
	temporary := f.r.Unit + ".external"
	if err := os.WriteFile(temporary, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, f.r.Unit); err != nil {
		t.Fatal(err)
	}
	return []byte(body)
}

func replaceFixtureBytes(t *testing.T, f *reloadFixture, body []byte) {
	t.Helper()
	temporary := f.r.Unit + ".external"
	if err := os.WriteFile(temporary, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, f.r.Unit); err != nil {
		t.Fatal(err)
	}
}

func fixtureReloadJournal(t *testing.T, f *reloadFixture) *serviceReloadJournal {
	t.Helper()
	j, err := readReloadJournal(f.r.Unit)
	if err != nil || j == nil {
		t.Fatalf("missing recovery intent: %v", err)
	}
	return j
}

func requireFixtureFile(t *testing.T, path string, expected []byte) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(body, expected) {
		t.Fatalf("retained definition differs: %v", err)
	}
}

func TestServiceReloadPublicationPreservesTwoExternalWriters(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newReloadFixture(t, platform)
			var first, latest []byte
			calls := 0
			f.r.RenameDefinition = func(from, to string) error {
				calls++
				j := fixtureReloadJournal(t, f)
				if from == f.r.Unit {
					if j.Publication != publicationClaimPending {
						t.Fatal("claim preceded durable intent")
					}
					first = replaceFixtureDefinition(t, f, "/external/first/runmoor")
				} else if from == reloadClaimPath(j) {
					if j.Publication != publicationRestorePending {
						t.Fatal("restoration preceded durable intent")
					}
					latest = replaceFixtureDefinition(t, f, "/external/latest/runmoor")
				} else {
					t.Fatal("changed claim authorized target publication")
				}
				return renameServiceDefinitionNoReplace(from, to)
			}
			if err := f.reload(); err == nil || f.mutated() || calls != 2 {
				t.Fatalf("unsafe two-writer outcome: calls=%d err=%v", calls, err)
			}
			j := fixtureReloadJournal(t, f)
			requireFixtureFile(t, f.r.Unit, latest)
			requireFixtureFile(t, reloadClaimPath(j), first)
			requireFixtureFile(t, reloadTargetPath(j), j.Target)
			f.r.RenameDefinition = nil
			f.commands = nil
			if err := f.reload(); err == nil || f.mutated() {
				t.Fatal("conflict recovery replaced a native service")
			}
			requireFixtureFile(t, f.r.Unit, latest)
			requireFixtureFile(t, reloadClaimPath(j), first)
		})
	}
}

func TestServiceReloadPublicationRejectsWriterBeforeTargetRename(t *testing.T) {
	for _, timing := range []string{"after claim", "before publish"} {
		t.Run(timing, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			var latest []byte
			f.r.RenameDefinition = func(from, to string) error {
				j := fixtureReloadJournal(t, f)
				if from == reloadTargetPath(j) && timing == "before publish" {
					latest = replaceFixtureDefinition(t, f, "/external/latest/runmoor")
				}
				err := renameServiceDefinitionNoReplace(from, to)
				if err == nil && from == f.r.Unit && timing == "after claim" {
					latest = replaceFixtureDefinition(t, f, "/external/latest/runmoor")
				}
				return err
			}
			if err := f.reload(); err == nil || f.mutated() {
				t.Fatal("occupied destination authorized replacement")
			}
			j := fixtureReloadJournal(t, f)
			requireFixtureFile(t, f.r.Unit, latest)
			requireFixtureFile(t, reloadClaimPath(j), j.Original)
			requireFixtureFile(t, reloadTargetPath(j), j.Target)
		})
	}
}

func TestServiceReloadPublicationRestoresChangedClaimOnlyIntoVacancy(t *testing.T) {
	f := newReloadFixture(t, "linux")
	var foreign []byte
	f.r.BeforePublish = func() { foreign = replaceFixtureDefinition(t, f, "/external/runmoor") }
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("changed claim authorized replacement")
	}
	j := fixtureReloadJournal(t, f)
	if j.Publication != publicationRestored || originalClaim(j) || !absentReloadFile(reloadClaimPath(j)) {
		t.Fatal("changed claim was not recorded and restored")
	}
	requireFixtureFile(t, f.r.Unit, foreign)
	requireFixtureFile(t, reloadTargetPath(j), j.Target)
	f.r.BeforePublish = nil
	f.commands = nil
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("unresolved external definition became authorized original")
	}
	replaceFixtureBytes(t, f, j.Original)
	f.commands = nil
	if err := f.reload(); err != nil {
		t.Fatalf("corrected definition did not permit retry: %v", err)
	}
	if !f.mutated() {
		t.Fatal("corrected definition did not complete native replacement")
	}
}

func TestServiceReloadPublicationRecoversInterruptedRenames(t *testing.T) {
	for _, boundary := range []string{"before claim", "after claim", "before publish", "after publish", "before restore", "after restore"} {
		t.Run(boundary, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			restoring := strings.Contains(boundary, "restore")
			var foreign []byte
			if restoring {
				f.r.BeforePublish = func() { foreign = replaceFixtureDefinition(t, f, "/external/runmoor") }
			}
			interrupted := false
			f.r.RenameDefinition = func(from, to string) error {
				j := fixtureReloadJournal(t, f)
				operation := "claim"
				if from == reloadTargetPath(j) {
					operation = "publish"
				} else if from == reloadClaimPath(j) {
					operation = "restore"
				}
				if !interrupted && boundary == "before "+operation {
					interrupted = true
					return errors.New("fixture interruption before rename")
				}
				err := renameServiceDefinitionNoReplace(from, to)
				if err == nil && !interrupted && boundary == "after "+operation {
					interrupted = true
					return errors.New("fixture interruption after rename")
				}
				return err
			}
			if err := f.reload(); err == nil || !interrupted || f.mutated() {
				t.Fatal("interrupted publication reached native replacement")
			}
			j := fixtureReloadJournal(t, f)
			f.r.RenameDefinition, f.r.BeforePublish = nil, nil
			f.commands = nil
			err := f.reload()
			if restoring {
				if err == nil || f.mutated() {
					t.Fatal("restoration recovery authorized an external original")
				}
				requireFixtureFile(t, f.r.Unit, foreign)
				requireFixtureFile(t, reloadTargetPath(j), j.Target)
				fixtureReloadJournal(t, f)
			} else if err != nil {
				// A failed publication first restores the original. That retry
				// records restoration; the next retry can safely claim it again.
				if fixtureReloadJournal(t, f).Publication != publicationRestored {
					t.Fatalf("unexpected recovery failure: %v", err)
				}
				if err := f.reload(); err != nil {
					t.Fatal(err)
				}
			}
			if !restoring {
				requireFixtureFile(t, f.r.Unit, j.Target)
				if journal, err := readReloadJournal(f.r.Unit); err != nil || journal != nil {
					t.Fatal("uncontended recovery did not complete")
				}
			}
		})
	}
}

func TestServiceReloadPublicationRetainsUnsafeClaims(t *testing.T) {
	for _, scenario := range []string{"pre-existing", "symlink", "permissions", "changed identity", "changed bytes", "unsupported rename"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			f.r.RenameDefinition = func(from, to string) error {
				j := fixtureReloadJournal(t, f)
				claim := reloadClaimPath(j)
				if scenario == "unsupported rename" {
					return problem(ErrPlatform, "Fixture no-replace operation is unavailable.", "Preserve reload intent.")
				}
				if from != f.r.Unit {
					t.Fatal("unsafe claim authorized another rename")
				}
				if scenario == "pre-existing" {
					if err := os.WriteFile(claim, []byte("external private bytes"), 0600); err != nil {
						t.Fatal(err)
					}
					return renameServiceDefinitionNoReplace(from, to)
				}
				if err := renameServiceDefinitionNoReplace(from, to); err != nil {
					return err
				}
				switch scenario {
				case "symlink":
					if err := os.Rename(claim, claim+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(claim+".retained", claim); err != nil {
						t.Fatal(err)
					}
				case "permissions":
					if err := os.Chmod(claim, 0644); err != nil {
						t.Fatal(err)
					}
				case "changed identity":
					if err := os.Rename(claim, claim+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(claim, j.Original, 0600); err != nil {
						t.Fatal(err)
					}
					// Force a recovery read rather than allowing restoration now.
					return errors.New("interrupt with changed claim")
				case "changed bytes":
					if err := os.WriteFile(claim, []byte("external changed bytes"), 0600); err != nil {
						t.Fatal(err)
					}
					return errors.New("interrupt with changed claim")
				}
				return nil
			}
			if err := f.reload(); err == nil || f.mutated() {
				t.Fatal("unsafe claim authorized native replacement")
			}
			j := fixtureReloadJournal(t, f)
			requireFixtureFile(t, reloadTargetPath(j), j.Target)
			if scenario != "unsupported rename" {
				if _, err := os.Lstat(reloadClaimPath(j)); err != nil {
					t.Fatal("unsafe claim was discarded")
				}
			}
		})
	}
}

func TestServiceReloadPublicationRecoversDirectorySyncFailures(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			calls := 0
			f.r.SyncDefinitionDir = func(path string) error {
				calls++
				if calls == failAt {
					return errors.New("fixture directory sync failure")
				}
				return syncPrivateDir(path)
			}
			if err := f.reload(); err == nil {
				t.Fatal("sync failure claimed success")
			}
			j := fixtureReloadJournal(t, f)
			if failAt < 3 && f.mutated() {
				t.Fatal("unsynced publication reached native replacement")
			}
			f.r.SyncDefinitionDir = nil
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			requireFixtureFile(t, f.r.Unit, j.Target)
			requireFixtureFile(t, reloadClaimPath(j), j.Original)
		})
	}
}

func TestServiceReloadRetiresPublishPendingJournalAfterTargetRename(t *testing.T) {
	f := newReloadFixture(t, "linux")
	calls := 0
	f.r.SyncDefinitionDir = func(path string) error {
		calls++
		if calls == 2 {
			return errors.New("fixture directory sync failure")
		}
		return syncPrivateDir(path)
	}
	if err := f.reload(); err == nil {
		t.Fatal("sync failure claimed success")
	}
	j := fixtureReloadJournal(t, f)
	if j.Publication != publicationPublishPending || !matchesReloadFile(f.r.Unit, j.TargetFileID, j.Target) {
		t.Fatal("target publication intent was not retained after the rename")
	}

	// Simulate the replacement manager starting from the already published
	// target. Its startup path must be able to retire the initiator's pending
	// publication instead of rejecting a valid interrupted outcome.
	f.r.SyncDefinitionDir = nil
	f.pid, f.peer = os.Getpid(), os.Getpid()
	targetArgs, err := reloadArguments(f.r.Platform, j.Target)
	if err != nil {
		t.Fatal(err)
	}
	f.args[f.pid] = targetArgs
	_, preserveStop, recovery, err := f.r.startup(context.Background(), f.path, f.c)
	if err != nil || !preserveStop || recovery == nil {
		t.Fatalf("target startup did not retain recovery: preserve=%t recovery=%v err=%v", preserveStop, recovery != nil, err)
	}
	if err := f.r.retire(recovery); err != nil {
		t.Fatalf("publish-pending journal was not reconciled: %v", err)
	}
	if journal, err := readReloadJournal(f.r.Unit); err != nil || journal != nil {
		t.Fatalf("reconciled journal was not retired: %v", err)
	}
	if !matchesReloadFile(reloadClaimPath(j), j.OriginalFileID, j.Original) {
		t.Fatal("verified displaced definition was not retained")
	}
}

func TestServiceReloadLaunchdPreparedStageRequiresLoadedJob(t *testing.T) {
	f := newReloadFixture(t, "darwin")
	original, info, err := readPrivateServiceDefinition(f.r.Unit)
	if err != nil {
		t.Fatal(err)
	}
	target, err := serviceDefinition(f.r.Platform, f.r.Binary, f.path)
	if err != nil {
		t.Fatal(err)
	}
	j := &serviceReloadJournal{
		Schema: 1, Token: newID(), Installation: f.m.Store.View().Installation,
		Storage: f.c.Storage, Platform: f.r.Platform, Unit: f.r.Unit,
		ConfigPath: f.path, Binary: f.r.Binary, Version: f.r.Version,
		PreviousVersion: f.version, Original: original, Target: []byte(target),
		OriginalFileID: hostFileIdentity(info), PID: f.pid,
		ProcessStart: "fixture:" + strconv.Itoa(f.pid), Stage: reloadPrepared,
		Publication: publicationPrepared,
	}
	if err := writePrivateExclusive(reloadTargetPath(j), j.Target); err != nil {
		t.Fatal(err)
	}
	_, targetInfo, err := readPrivateServiceDefinition(reloadTargetPath(j))
	if err != nil {
		t.Fatal(err)
	}
	j.TargetFileID = hostFileIdentity(targetInfo)
	before, err := os.ReadFile(f.r.Unit)
	if err != nil {
		t.Fatal(err)
	}
	f.loaded, f.pid = false, 0
	if err := f.r.replaceLaunchd(context.Background(), f.path, f.c, j); err == nil {
		t.Fatal("unloaded prepared launchd job unexpectedly authorized publication")
	}
	after, err := os.ReadFile(f.r.Unit)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("unloaded prepared launchd job changed the canonical definition")
	}
}

func TestServiceDefinitionRenameNeverReplacesDestination(t *testing.T) {
	f := newReloadFixture(t, "linux")
	source := f.r.Unit + ".source"
	if err := os.WriteFile(source, []byte("staged"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.r.Unit)
	if err != nil {
		t.Fatal(err)
	}
	if err := renameServiceDefinitionNoReplace(source, f.r.Unit); err == nil {
		t.Fatal("no-replace rename replaced an occupied destination")
	}
	requireFixtureFile(t, f.r.Unit, before)
	requireFixtureFile(t, source, []byte("staged"))
}

func TestServiceReloadPublicationRecoversRestoreSyncFailure(t *testing.T) {
	f := newReloadFixture(t, "linux")
	var foreign []byte
	f.r.BeforePublish = func() { foreign = replaceFixtureDefinition(t, f, "/external/runmoor") }
	calls := 0
	f.r.SyncDefinitionDir = func(path string) error {
		calls++
		if calls == 2 {
			return errors.New("fixture restoration sync failure")
		}
		return syncPrivateDir(path)
	}
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("unsynced restoration claimed success")
	}
	j := fixtureReloadJournal(t, f)
	if j.Publication != publicationRestorePending {
		t.Fatal("restoration intent was lost")
	}
	requireFixtureFile(t, f.r.Unit, foreign)
	f.r.BeforePublish, f.r.SyncDefinitionDir = nil, nil
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("restored foreign file authorized native replacement")
	}
	if fixtureReloadJournal(t, f).Publication != publicationRestored {
		t.Fatal("restoration was not reconciled")
	}
	requireFixtureFile(t, f.r.Unit, foreign)
}

func TestServiceReloadPublicationRetainsFilesAfterJournalFailure(t *testing.T) {
	f := newReloadFixture(t, "linux")
	f.r.RenameDefinition = func(from, to string) error {
		if err := renameServiceDefinitionNoReplace(from, to); err != nil {
			return err
		}
		if err := os.Chmod(reloadJournalPath(f.r.Unit), 0644); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("failed journal update authorized replacement")
	}
	if err := os.Chmod(reloadJournalPath(f.r.Unit), 0600); err != nil {
		t.Fatal(err)
	}
	j := fixtureReloadJournal(t, f)
	if j.Publication != publicationClaimPending {
		t.Fatal("claim intent was not retained")
	}
	requireFixtureFile(t, reloadClaimPath(j), j.Original)
	requireFixtureFile(t, reloadTargetPath(j), j.Target)
	f.r.RenameDefinition = nil
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceReloadPublicationKeepsClaimChangedAfterVerification(t *testing.T) {
	f := newReloadFixture(t, "linux")
	f.r.RenameDefinition = func(from, to string) error {
		j := fixtureReloadJournal(t, f)
		if from == reloadTargetPath(j) {
			claim := reloadClaimPath(j)
			if err := os.Rename(claim, claim+".retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(claim, j.Original, 0600); err != nil {
				t.Fatal(err)
			}
			return errors.New("fixture claim identity changed")
		}
		return renameServiceDefinitionNoReplace(from, to)
	}
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("changed claim authorized replacement")
	}
	j := fixtureReloadJournal(t, f)
	if matchesReloadFile(reloadClaimPath(j), j.OriginalFileID, j.Original) {
		t.Fatal("fixture did not change the claim identity")
	}
	requireFixtureFile(t, reloadClaimPath(j), j.Original)
	requireFixtureFile(t, reloadClaimPath(j)+".retained", j.Original)
	requireFixtureFile(t, reloadTargetPath(j), j.Target)
	f.r.RenameDefinition = nil
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("recovery adopted changed claim")
	}
	requireFixtureFile(t, reloadClaimPath(j), j.Original)
}

func TestServiceReloadPublicationRecoversLegacyJournalsConservatively(t *testing.T) {
	for _, outcome := range []string{"prepared", "published", "unknown displaced"} {
		t.Run(outcome, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			if outcome == "prepared" {
				f.r.RenameDefinition = func(string, string) error { return errors.New("fixture retain prepared intent") }
			} else {
				f.onCommand = func(command string) error {
					if strings.Contains(command, " daemon-reload") {
						return errors.New("fixture retain published intent")
					}
					return nil
				}
			}
			if err := f.reload(); err == nil {
				t.Fatal("fixture did not retain intent")
			}
			j := fixtureReloadJournal(t, f)
			copy := *j
			copy.Publication, copy.ClaimFileID, copy.ClaimSHA256 = "", "", ""
			if err := f.r.saveJournal(j, &copy); err != nil {
				t.Fatal(err)
			}
			if outcome == "unknown displaced" {
				if err := os.WriteFile(reloadTargetPath(j), []byte("unknown external bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.r.RenameDefinition, f.onCommand = nil, nil
			f.commands = nil
			err := f.reload()
			if outcome == "unknown displaced" {
				if err == nil || f.mutated() {
					t.Fatal("legacy recovery discarded unknown displaced bytes")
				}
				requireFixtureFile(t, reloadTargetPath(j), []byte("unknown external bytes"))
				fixtureReloadJournal(t, f)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceReloadPublicationJournalDoesNotCopyExternalContent(t *testing.T) {
	f := newReloadFixture(t, "linux")
	foreign := []byte("external private content must stay outside the journal")
	f.r.BeforePublish = func() {
		path := f.r.Unit + ".external"
		if err := os.WriteFile(path, foreign, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, f.r.Unit); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.reload(); err == nil || f.mutated() {
		t.Fatal("unknown external content authorized replacement")
	}
	j := fixtureReloadJournal(t, f)
	if j.ClaimSHA256 != definitionDigest(foreign) {
		t.Fatal("external byte verification was not recorded")
	}
	body, err := os.ReadFile(reloadJournalPath(f.r.Unit))
	if err != nil || bytes.Contains(body, foreign) || bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(foreign))) {
		t.Fatal("journal copied external content")
	}
	requireFixtureFile(t, f.r.Unit, foreign)
}
