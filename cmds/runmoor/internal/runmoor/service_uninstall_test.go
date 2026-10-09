//go:build darwin || linux

package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newUninstallFixture(t *testing.T) (*reloadFixture, *serviceUninstaller, serviceDefinitionSnapshot) {
	t.Helper()
	f := newReloadFixture(t, runtime.GOOS)
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.r.Unit, unit); err != nil {
		t.Fatal(err)
	}
	f.r.Unit = unit
	snapshot, err := validateServiceConfigMatch(f.r.Platform, f.r.Unit, f.path)
	if err != nil {
		t.Fatal(err)
	}
	return f, &serviceUninstaller{unit: f.r.Unit}, snapshot
}
func fixtureUninstallJournal(t *testing.T, u *serviceUninstaller) *serviceUninstallJournal {
	t.Helper()
	j, err := readUninstallJournal(u.unit)
	if err != nil || j == nil {
		t.Fatalf("missing uninstall intent: %v", err)
	}
	return j
}
func requireUninstallAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected absence: %v", err)
	}
}

func TestServiceUninstallClaimSuccess(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	if err := u.begin(f.r.Platform, f.path, original); err != nil {
		t.Fatal(err)
	}
	requireUninstallAbsent(t, u.unit)
	requireUninstallAbsent(t, uninstallJournalPath(u.unit))
	if len(f.commands) != 0 {
		t.Fatal("filesystem cleanup contacted a native service")
	}
}

func TestServiceUninstallPreservesConcurrentDefinitions(t *testing.T) {
	for _, scenario := range []string{"replacement before claim", "refill after claim", "replacement and refill"} {
		t.Run(scenario, func(t *testing.T) {
			f, u, original := newUninstallFixture(t)
			var replaced, latest []byte
			u.boundary = func(stage string) error {
				if stage == "before_claim" && scenario != "refill after claim" {
					replaced = replacePublicationDefinition(t, f, "/external/first/runmoor")
				}
				if stage == "after_claim" && scenario != "replacement before claim" {
					latest = replacePublicationDefinition(t, f, "/external/latest/runmoor")
				}
				return nil
			}
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("changed definition claimed complete uninstall")
			}
			if scenario == "replacement before claim" {
				requireFixtureFile(t, u.unit, replaced)
				j := fixtureUninstallJournal(t, u)
				if j.Stage != uninstallRestored {
					t.Fatal("restoration not recorded")
				}
				requireUninstallAbsent(t, uninstallClaimPath(j))
			} else if scenario == "refill after claim" {
				requireFixtureFile(t, u.unit, latest)
				requireUninstallAbsent(t, uninstallJournalPath(u.unit))
			} else {
				requireFixtureFile(t, u.unit, latest)
				j := fixtureUninstallJournal(t, u)
				if j.Stage != uninstallRestorePending {
					t.Fatal("restore conflict lost its intent")
				}
				requireFixtureFile(t, uninstallClaimPath(j), replaced)
				u.boundary = nil
				if err := u.resume(j); err == nil {
					t.Fatal("conflict retry overwrote canonical writer")
				}
				requireFixtureFile(t, u.unit, latest)
				requireFixtureFile(t, uninstallClaimPath(j), replaced)
			}
		})
	}
}

func TestServiceUninstallRecoversInterruptedBoundaries(t *testing.T) {
	for _, interrupt := range []string{"before_claim", "after_claim", "before_delete", "after_delete", "before_retire"} {
		t.Run(interrupt, func(t *testing.T) {
			f, u, original := newUninstallFixture(t)
			u.boundary = func(stage string) error {
				if stage == interrupt {
					return errors.New("fixture interruption")
				}
				return nil
			}
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("interruption was ignored")
			}
			j := fixtureUninstallJournal(t, u)
			if interrupt == "before_claim" {
				requireFixtureFile(t, u.unit, original.data)
			}
			if interrupt == "after_claim" || interrupt == "before_delete" {
				requireFixtureFile(t, uninstallClaimPath(j), original.data)
			}
			u.boundary = nil
			if err := u.resume(j); err != nil {
				t.Fatal("owned interrupted uninstall did not recover", err)
			}
			requireUninstallAbsent(t, u.unit)
			requireUninstallAbsent(t, uninstallClaimPath(j))
			requireUninstallAbsent(t, uninstallJournalPath(u.unit))
		})
	}
}

func TestServiceUninstallRecoversInterruptedRestoration(t *testing.T) {
	for _, interrupt := range []string{"before_restore", "after_restore"} {
		t.Run(interrupt, func(t *testing.T) {
			f, u, original := newUninstallFixture(t)
			var external []byte
			u.boundary = func(stage string) error {
				if stage == "before_claim" {
					external = replacePublicationDefinition(t, f, "/external/runmoor")
				}
				if stage == interrupt {
					return errors.New("fixture restore interruption")
				}
				return nil
			}
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("restore interruption lost")
			}
			j := fixtureUninstallJournal(t, u)
			u.boundary = nil
			if err := u.resume(j); err == nil {
				t.Fatal("mismatch recovery claimed uninstall success")
			}
			requireFixtureFile(t, u.unit, external)
			if j.Stage != uninstallRestored {
				t.Fatal("recovered restoration not checkpointed")
			}
		})
	}
}

func TestServiceUninstallRetainsUnsafeClaims(t *testing.T) {
	for _, scenario := range []string{"preexisting", "symlink", "permissions", "identity", "bytes", "unknown interrupted claim"} {
		t.Run(scenario, func(t *testing.T) {
			f, u, original := newUninstallFixture(t)
			var claim string
			u.boundary = func(stage string) error {
				j := fixtureUninstallJournal(t, u)
				claim = uninstallClaimPath(j)
				if stage == "before_claim" && scenario == "preexisting" {
					return os.WriteFile(claim, []byte("unknown retained bytes"), 0600)
				}
				if stage != "before_delete" && !(stage == "after_claim" && scenario == "unknown interrupted claim") {
					return nil
				}
				switch scenario {
				case "symlink":
					if err := os.Rename(claim, claim+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(claim+".original", claim); err != nil {
						t.Fatal(err)
					}
				case "permissions":
					if err := os.Chmod(claim, 0644); err != nil {
						t.Fatal(err)
					}
				case "identity", "unknown interrupted claim":
					if err := os.Rename(claim, claim+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(claim, original.data, 0600); err != nil {
						t.Fatal(err)
					}
				case "bytes":
					if err := os.WriteFile(claim, []byte("unknown changed bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "unknown interrupted claim" {
					return errors.New("fixture interrupted unknown claim")
				}
				return nil
			}
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("unsafe claim was deleted")
			}
			j := fixtureUninstallJournal(t, u)
			if _, err := os.Lstat(claim); err != nil {
				t.Fatal("unsafe claim lost", err)
			}
			u.boundary = nil
			if err := u.resume(j); err == nil {
				t.Fatal("retry adopted unsafe claim")
			}
			if _, err := os.Lstat(claim); err != nil {
				t.Fatal("retry removed unknown claim", err)
			}
			if scenario == "identity" || scenario == "symlink" || scenario == "unknown interrupted claim" {
				if _, err := os.Lstat(claim + ".original"); err != nil {
					t.Fatal("original bytes lost", err)
				}
			}
		})
	}
}

func TestServiceUninstallFailsClosedOnRenameAndSync(t *testing.T) {
	for _, scenario := range []string{"rename", "journal sync", "claim sync", "delete sync", "completed sync", "retire sync"} {
		t.Run(scenario, func(t *testing.T) {
			f, u, original := newUninstallFixture(t)
			if scenario == "rename" {
				u.rename = func(string, string) error { return errors.New("fixture no-replace unavailable") }
			}
			calls := 0
			u.syncDir = func(dir string) error {
				calls++
				if scenario == "journal sync" && calls == 1 || scenario == "claim sync" && calls == 2 || scenario == "delete sync" && calls == 3 || scenario == "completed sync" && calls == 4 || scenario == "retire sync" && calls == 5 {
					return errors.New("fixture sync failure")
				}
				return syncPrivateDir(dir)
			}
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("failed durable boundary acknowledged success")
			}
			j := fixtureUninstallJournal(t, u)
			u.rename, u.syncDir = nil, nil
			if err := u.resume(j); err != nil {
				t.Fatal("durable retry could not recover", err)
			}
		})
	}
}

func TestServiceUninstallPendingAdmission(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(stage string) error {
		if stage == "after_claim" {
			return errors.New("fixture pending claim")
		}
		return nil
	}
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("fixture did not interrupt")
	}
	j := fixtureUninstallJournal(t, u)
	for _, action := range []string{"install", "start", "stop"} {
		if err := Service(context.Background(), action, f.path, f.c, f); err == nil {
			t.Fatalf("%s bypassed pending uninstall", action)
		}
	}
	if err := f.r.Reload(context.Background(), f.path, f.c); err == nil {
		t.Fatal("reload bypassed pending uninstall")
	}
	if err := f.r.start(context.Background(), f.path, f.c); err == nil {
		t.Fatal("internal Start bypassed pending uninstall")
	}
	if len(f.commands) != 0 {
		t.Fatal("pending intent authorized native commands")
	}
	if err := Service(context.Background(), "uninstall", f.path+".foreign", f.c, f); err == nil {
		t.Fatal("foreign config gained recovery authority")
	}
	if err := Service(context.Background(), "uninstall", f.path, f.c, f); err != nil {
		t.Fatal("explicit uninstall did not recover missing canonical definition", err)
	}
	requireUninstallAbsent(t, uninstallClaimPath(j))
}

func TestServiceUninstallRejectsMalformedIntent(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(stage string) error { return errors.New("fixture pending intent") }
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("fixture did not interrupt")
	}
	j := fixtureUninstallJournal(t, u)
	j.Token = "../foreign"
	body, _ := json.Marshal(j)
	if err := os.WriteFile(uninstallJournalPath(u.unit), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUninstallJournal(u.unit); err == nil {
		t.Fatal("malformed intent admitted")
	}
	requireFixtureFile(t, u.unit, original.data)
	if _, err := os.Stat(filepath.Dir(u.unit)); err != nil {
		t.Fatal(err)
	}
}

func TestServiceUninstallAbortsVerifiedRestoredIntentBeforeFreshAuthority(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	var external []byte
	u.boundary = func(stage string) error {
		if stage == "before_claim" {
			external = replacePublicationDefinition(t, f, "/external/restored/runmoor")
		}
		return nil
	}
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("changed definition was deleted")
	}
	j := fixtureUninstallJournal(t, u)
	u.boundary = nil
	if err := u.resume(j); err == nil {
		t.Fatal("old intent adopted the restored replacement")
	}
	requireFixtureFile(t, u.unit, external)
	requireUninstallAbsent(t, uninstallJournalPath(u.unit))
	if len(f.commands) != 0 {
		t.Fatal("old intent authorized native actions")
	}
}

func TestServiceUninstallKeepsSymlinkedCanonicalDefinition(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(stage string) error {
		if stage != "before_claim" {
			return nil
		}
		if err := os.Rename(u.unit, u.unit+".original"); err != nil {
			return err
		}
		return os.Symlink(u.unit+".original", u.unit)
	}
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("symlink acquired deletion authority")
	}
	j := fixtureUninstallJournal(t, u)
	if info, err := os.Lstat(uninstallClaimPath(j)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("unknown symlink was removed", err)
	}
	requireFixtureFile(t, u.unit+".original", original.data)
}

func TestServiceUninstallIntentPublicationPreservesExistingReceipt(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(stage string) error { return errors.New("fixture before claim") }
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("fixture did not interrupt")
	}
	j := fixtureUninstallJournal(t, u)
	foreign := []byte("external receipt bytes")
	if err := os.WriteFile(uninstallJournalPath(u.unit), foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeUninstallIntent(u.unit, j); err == nil {
		t.Fatal("intent publication overwrote existing receipt")
	}
	requireFixtureFile(t, uninstallJournalPath(u.unit), foreign)
	requireFixtureFile(t, u.unit, original.data)
}

func TestServiceUninstallRecoveryPreservesConfigPathChecks(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(string) error { return errors.New("fixture pending intent") }
	if err := u.begin(f.r.Platform, f.path, original); err == nil {
		t.Fatal("fixture did not interrupt")
	}
	j := fixtureUninstallJournal(t, u)
	home := filepath.Dir(f.path)
	direct := filepath.Join(home, "direct")
	if err := os.Mkdir(direct, 0700); err != nil {
		t.Fatal(err)
	}
	if err := requireUninstallConfig(j, f.r.Platform, direct+"/../"+filepath.Base(f.path)); err != nil {
		t.Fatal("harmless direct parent component was rejected", err)
	}
	foreign := t.TempDir()
	if err := os.Mkdir(filepath.Join(foreign, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, filepath.Base(f.path)), []byte("foreign configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(filepath.Join(foreign, "child"), alias); err != nil {
		t.Fatal(err)
	}
	if err := requireUninstallConfig(j, f.r.Platform, alias+"/../"+filepath.Base(f.path)); err == nil {
		t.Fatal("symlink-based parent traversal gained recovery authority")
	}
	requireFixtureFile(t, u.unit, original.data)
}

func TestServiceUninstallBlocksForegroundReloadFallbackAfterProbe(t *testing.T) {
	f, u, original := newUninstallFixture(t)
	u.boundary = func(stage string) error {
		if stage == "after_claim" {
			return errors.New("fixture pending claim")
		}
		return nil
	}
	f.r.Control = func(ctx context.Context, c Config, req ControlRequest, expected int) (ControlResponse, int, error) {
		response, peer, err := f.control(ctx, c, req, expected)
		if req.Action == "status" {
			if err := u.begin(f.r.Platform, f.path, original); err == nil {
				t.Fatal("fixture did not interrupt")
			}
		}
		return response, peer, err
	}
	if err := f.reload(); err == nil {
		t.Fatal("new pending claim allowed foreground reload fallback")
	}
	for _, action := range f.actions {
		if action == "reload" {
			t.Fatal("pending uninstall authorized manager configuration mutation")
		}
	}
	j := fixtureUninstallJournal(t, u)
	requireFixtureFile(t, uninstallClaimPath(j), original.data)
}
