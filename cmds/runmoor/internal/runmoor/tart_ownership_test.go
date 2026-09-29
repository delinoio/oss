package runmoor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func replacedTartVM(t *testing.T, c Config, installation, entity string) (string, string) {
	t.Helper()
	name := "rm-" + entity
	if err := claimVM(c, name, installation, entity); err != nil {
		t.Fatal(err)
	}
	dir := vmPath(c, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishVMOwnerMarker(c, name, installation, entity); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign-disk")
	if err := os.WriteFile(foreign, []byte("unrelated Tart VM data"), 0600); err != nil {
		t.Fatal(err)
	}
	return name, foreign
}

func assertTartNeverStoppedOrDeleted(t *testing.T, fixture *tartFixture) {
	t.Helper()
	for _, args := range fixture.commands {
		if len(args) != 0 && (args[0] == "stop" || args[0] == "delete") {
			t.Fatalf("ambiguous VM was mutated with Tart %q", args[0])
		}
	}
}

func TestTartStaleOwnerRecordCannotAuthorizeReplacementVM(t *testing.T) {
	for _, action := range []string{"inspect", "stop", "cleanup", "image removal"} {
		t.Run(action, func(t *testing.T) {
			c, s := fixtureStore(t)
			id := newID()
			name, foreign := replacedTartVM(t, c, s.View().Installation, id)
			driver, fixture := fakeTart(c)
			fixture.running[name] = true
			r := Runner{ID: id, Handle: Handle{VM: name}, Phase: Busy}
			var err error
			switch action {
			case "inspect":
				_, err = driver.Inspect(context.Background(), c, r, s.View())
			case "stop":
				err = driver.Stop(context.Background(), c, r, s.View())
			case "cleanup":
				err = driver.Cleanup(context.Background(), c, r, s.View())
			case "image removal":
				if err = s.Update(func(snapshot *Snapshot) error {
					snapshot.Images[id] = &Image{ID: id, VM: name, Phase: ImageSealed}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				images := &ImageManager{Store: s, Tart: driver}
				_, err = images.Operate(context.Background(), c, ImageRequest{Action: "remove", ID: id})
			}
			requireCode(t, err, ErrOwnership)
			assertTartNeverStoppedOrDeleted(t, fixture)
			if _, err = os.Stat(foreign); err != nil {
				t.Fatal("replacement VM data was removed", err)
			}
			if err = verifyVMOwnerRecord(c, name, s.View().Installation, id); err != nil {
				t.Fatal("durable owner record was removed", err)
			}
			if action == "image removal" && s.View().Images[id] == nil {
				t.Fatal("ambiguous image record was removed")
			}
		})
	}
}

func TestTartOwnershipIsRecheckedAfterVMInspection(t *testing.T) {
	for _, action := range []string{"inspect", "stop", "cleanup"} {
		t.Run(action, func(t *testing.T) {
			c, s := fixtureStore(t)
			id := newID()
			name := "rm-" + id
			if err := claimVM(c, name, s.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			driver, fixture := fakeTart(c)
			fixture.running[name] = true
			foreign := filepath.Join(vmPath(c, name), "foreign-disk")
			fixture.afterGet = func(got string) {
				if got != name {
					t.Errorf("inspection targeted %q, want %q", got, name)
				}
				if err := os.RemoveAll(vmPath(c, name)); err != nil {
					t.Error(err)
					return
				}
				if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
					t.Error(err)
					return
				}
				if err := os.WriteFile(foreign, []byte("replacement during inspection"), 0600); err != nil {
					t.Error(err)
				}
			}
			r := Runner{ID: id, Handle: Handle{VM: name}, Phase: Busy}
			var err error
			switch action {
			case "inspect":
				_, err = driver.Inspect(context.Background(), c, r, s.View())
			case "stop":
				err = driver.Stop(context.Background(), c, r, s.View())
			case "cleanup":
				err = driver.Cleanup(context.Background(), c, r, s.View())
			}
			requireCode(t, err, ErrOwnership)
			assertTartNeverStoppedOrDeleted(t, fixture)
			if _, err = os.Stat(foreign); err != nil {
				t.Fatal("replacement VM data was removed", err)
			}
		})
	}
}

func TestTartRejectsInvalidEmbeddedOwnershipMarkers(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "symlink", "unsafe permissions", "wrong installation", "wrong entity", "wrong VM"} {
		t.Run(mode, func(t *testing.T) {
			c, s := fixtureStore(t)
			id := newID()
			name := "rm-" + id
			if err := claimVM(c, name, s.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			markerPath := vmOwnerMarkerPath(c, name)
			switch mode {
			case "missing":
				if err := os.Remove(markerPath); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(markerPath, []byte("not-json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "owner.json")
				if err := os.WriteFile(target, []byte(`{"installation":"`+s.View().Installation+`","entity":"`+id+`","vm":"`+name+`"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(markerPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, markerPath); err != nil {
					t.Fatal(err)
				}
			case "unsafe permissions":
				if err := os.Chmod(markerPath, 0644); err != nil {
					t.Fatal(err)
				}
			default:
				owner := vmOwner{Installation: s.View().Installation, Entity: id, VM: name}
				switch mode {
				case "wrong installation":
					owner.Installation = newID()
				case "wrong entity":
					owner.Entity = newID()
				case "wrong VM":
					owner.VM = "rm-" + newID()
				}
				b, err := json.Marshal(owner)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(markerPath, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			requireCode(t, verifyVMOwner(c, name, s.View().Installation, id), ErrOwnership)
		})
	}
}

func TestTartForceStopKeepsReplacementAndReservation(t *testing.T) {
	m, c, remote, _, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	name, foreign := replacedTartVM(t, c, m.Store.View().Installation, id)
	driver, fixture := fakeTart(c)
	fixture.running[name] = true
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Runners[id].Backend = Tart
		s.Runners[id].Handle = Handle{VM: name}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Drivers = func(Backend) (Driver, error) { return driver, nil }
	if err := m.Stop(true); err != nil {
		t.Fatal(err)
	}
	m.cleanup(context.Background(), id)
	r := m.Store.View().Runners[id]
	requireCode(t, r.Problem, ErrOwnership)
	if r.Terminated || r.LocalCleaned || r.Phase != Quarantined {
		t.Fatal("force-stop released an ambiguous Tart execution", r)
	}
	if _, count, _ := usage(m.Store.View()); count != 1 {
		t.Fatal("force-stop released the ambiguous execution reservation")
	}
	if remote.removed != 0 {
		t.Fatal("force-stop removed the remote registration before local ownership was verified")
	}
	assertTartNeverStoppedOrDeleted(t, fixture)
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("force-stop removed foreign VM data", err)
	}
}

func TestTartCleanupKnownAbsenceIsIdempotent(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	driver, _ := fakeTart(c)
	r := Runner{ID: id, Handle: Handle{VM: name}}
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal("confirmed absent VM did not clean up idempotently", err)
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
		t.Fatal("completed absence retained its matching durable owner record", err)
	}
}

func TestTartCleanupDeletesTheVerifiedDirectoryAfterOriginalNameReplacement(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.json", "nvram.bin", "disk.img"} {
		if err := os.WriteFile(filepath.Join(vmPath(c, name), file), []byte("owned VM"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	deletionName := deletionVMName(id)
	driver, fixture := fakeTart(c)
	foreign := filepath.Join(vmPath(c, name), "foreign-disk")
	fixture.beforeDelete = func(target string) {
		if target != deletionName {
			t.Errorf("Tart delete target = %q, want staged name %q", target, deletionName)
		}
		if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(foreign, []byte("replacement at original name"), 0600); err != nil {
			t.Error(err)
		}
	}
	r := Runner{ID: id, Handle: Handle{VM: name}}
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "replacement at original name" {
		t.Fatal("cleanup removed the replacement at the original name", err)
	}
	if _, err := os.Lstat(vmPath(c, deletionName)); !os.IsNotExist(err) {
		t.Fatal("cleanup left the verified Tart VM at its staged name", err)
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
		t.Fatal("successful cleanup retained the durable owner record", err)
	}
}

func TestTartCleanupRecoversAStagedOwnedVM(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.json", "nvram.bin", "disk.img"} {
		if err := os.WriteFile(filepath.Join(vmPath(c, name), file), []byte("owned VM"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	deletionName := deletionVMName(id)
	if err := renameTartVMNoReplace(vmPath(c, name), vmPath(c, deletionName)); err != nil {
		t.Fatal(err)
	}
	driver, fixture := fakeTart(c)
	r := Runner{ID: id, Handle: Handle{VM: name}}
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.commands) == 0 || fixture.commands[len(fixture.commands)-1][0] != "delete" || fixture.commands[len(fixture.commands)-1][1] != deletionName {
		t.Fatal("recovery did not delete the verified staged VM", fixture.commands)
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
		t.Fatal("recovered cleanup retained the durable owner record", err)
	}
}

func TestTartCleanupPreservesADeletionNameCollision(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.json", "nvram.bin", "disk.img"} {
		if err := os.WriteFile(filepath.Join(vmPath(c, name), file), []byte("owned VM"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	deletionName := deletionVMName(id)
	foreign := filepath.Join(vmPath(c, deletionName), "foreign-disk")
	if err := os.MkdirAll(vmPath(c, deletionName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("unrelated VM"), 0600); err != nil {
		t.Fatal(err)
	}
	driver, fixture := fakeTart(c)
	err := driver.Cleanup(context.Background(), c, Runner{ID: id, Handle: Handle{VM: name}}, s.View())
	requireCode(t, err, ErrOwnership)
	assertTartNeverStoppedOrDeleted(t, fixture)
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "unrelated VM" {
		t.Fatal("cleanup removed the unrelated deletion-name occupant", err)
	}
	if _, err := os.Stat(vmOwnerMarkerPath(c, name)); err != nil {
		t.Fatal("collision discarded the original owned VM", err)
	}
	if err := verifyVMOwnerRecord(c, name, s.View().Installation, id); err != nil {
		t.Fatal("collision discarded the durable owner record", err)
	}
}

func TestTartOwnerMarkerPublicationIsExclusiveAndPrivate(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(vmOwnerMarkerPath(c, name))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("owner marker permissions = %04o, want 0600", info.Mode().Perm())
	}
	if err = publishVMOwnerMarker(c, name, s.View().Installation, newID()); err == nil {
		t.Fatal("publication overwrote an existing marker")
	}
	entries, err := os.ReadDir(vmPath(c, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".runmoor-owner-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary owner marker was left behind: %s", entry.Name())
		}
	}
}
