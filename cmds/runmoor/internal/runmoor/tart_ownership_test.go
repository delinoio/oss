package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func createFixtureTartVM(t *testing.T, c Config, home, name string) {
	t.Helper()
	dir := tartVMPathAtHome(home, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.json", "nvram.bin", "disk.img"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTartConfigLockProbeProcess(t *testing.T) {
	path := os.Getenv("RUNMOOR_TART_CONFIG_LOCK_PROBE")
	if path == "" {
		return
	}
	lock, err := lockTartVMConfig(path)
	if err != nil {
		os.Exit(11)
	}
	_ = lock.Close()
	os.Exit(0)
}

func tartConfigLockHeldByOtherProcess(t *testing.T, path string) bool {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Tart config locks use Unix advisory locks")
	}
	t.Setenv("RUNMOOR_TART_CONFIG_LOCK_PROBE", path)
	cmd := exec.Command(os.Args[0], "-test.run=^TestTartConfigLockProbeProcess$")
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode() == 11
	}
	return false
}

func TestTartCreationKeepsForeignFinalVMOutsideMarkerPublication(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	home, lock, err := prepareTartCreationHome(c, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = lock.Close()
		cleanupTartCreationHome(c, id)
	}()
	createFixtureTartVM(t, c, home, creationVMName(id))

	foreignPath := vmPath(c, name)
	if err = os.MkdirAll(foreignPath, 0700); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(foreignPath, "foreign-disk")
	if err = os.WriteFile(foreignFile, []byte("foreign Tart VM"), 0600); err != nil {
		t.Fatal(err)
	}
	err = publishAndMoveCreatedTartVM(c, home, creationVMName(id), name, s.View().Installation, id)
	requireCode(t, err, ErrOwnership)
	if _, err = os.Lstat(filepath.Join(foreignPath, vmOwnerMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("foreign VM received an ownership marker: %v", err)
	}
	if b, readErr := os.ReadFile(foreignFile); readErr != nil || string(b) != "foreign Tart VM" {
		t.Fatalf("foreign VM changed: %q, %v", b, readErr)
	}
}

func TestTartMarkedCreationIsPromotedAfterRestart(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	installation := s.View().Installation
	if err := claimVM(c, name, installation, id); err != nil {
		t.Fatal(err)
	}
	home, lock, err := prepareTartCreationHome(c, id)
	if err != nil {
		t.Fatal(err)
	}
	createFixtureTartVM(t, c, home, creationVMName(id))
	if err = publishVMOwnerMarkerAt(c, tartCreationVMPath(c, id), name, installation, id); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = promoteCreatedTartVM(c, name, installation, id); err != nil {
		t.Fatal(err)
	}
	if err = verifyVMOwner(c, name, installation, id); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(tartCreationVMPath(c, id)); !os.IsNotExist(err) {
		t.Fatalf("staged VM remains after promotion: %v", err)
	}
}

func TestTartInspectionDoesNotTreatActiveCreationAsAbsent(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	installation := s.View().Installation
	if err := claimVM(c, name, installation, id); err != nil {
		t.Fatal(err)
	}
	_, lock, err := prepareTartCreationHome(c, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = lock.Close()
		cleanupTartCreationHome(c, id)
	}()
	driver, _ := fakeTart(c)
	_, err = driver.Inspect(context.Background(), c, Runner{ID: id, Handle: Handle{VM: name}, Phase: Preparing}, s.View())
	requireCode(t, err, ErrRetry)
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

func TestTartOwnedCommandUsesTheVerifiedDirectoryAfterNameReplacement(t *testing.T) {
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
	ownedBackup := vmPath(c, name) + ".owned"
	foreignFile := filepath.Join(vmPath(c, name), "foreign-data")
	var aliasedName string
	fixture.beforePinned = func(args []string, _ *os.File) {
		if len(args) < 2 || args[0] != "set" {
			t.Errorf("pinned Tart arguments = %q, want set against an alias", args)
			return
		}
		aliasedName = args[1]
		if aliasedName == name {
			t.Error("Tart still received the replaceable VM name")
		}
		if target, err := os.Readlink(filepath.Join(c.Storage.Data, "tart", "vms", aliasedName)); err != nil || target != "/dev/fd/3" {
			t.Errorf("pinned alias target = %q, %v", target, err)
		}
		if err := os.Rename(vmPath(c, name), ownedBackup); err != nil {
			t.Error(err)
			return
		}
		if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(foreignFile, []byte("foreign VM"), 0600); err != nil {
			t.Error(err)
		}
	}
	_, err := driver.runOwned(context.Background(), c, s.View().Installation, id, name, []string{"set", name, "--cpu", "2"}, nil)
	requireCode(t, err, ErrOwnership)
	if aliasedName == "" {
		t.Fatal("owned Tart command did not use the pinned command executor")
	}
	backupInfo, err := os.Stat(ownedBackup)
	if err != nil || fixture.pinnedSetIdentity != name || fixture.pinnedSetInfo == nil || !os.SameFile(fixture.pinnedSetInfo, backupInfo) {
		t.Fatal("the Tart operation was not bound to the verified VM descriptor", err)
	}
	if _, err = os.Stat(foreignFile); err != nil {
		t.Fatal("replacement VM data was removed", err)
	}
}

func TestTartStartUsesTheVerifiedDirectoryAfterNameReplacement(t *testing.T) {
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
	ownedBackup := vmPath(c, name) + ".owned"
	foreignFile := filepath.Join(vmPath(c, name), "foreign-data")
	var aliasedName string
	fixture.beforeStartPinned = func(args []string, _ *os.File) {
		if len(args) == 0 || args[len(args)-1] == name {
			t.Errorf("Tart start arguments = %q, want a pinned alias", args)
			return
		}
		aliasedName = args[len(args)-1]
		if target, err := os.Readlink(filepath.Join(c.Storage.Data, "tart", "vms", aliasedName)); err != nil || target != "/dev/fd/3" {
			t.Errorf("pinned start alias target = %q, %v", target, err)
		}
		if err := os.Rename(vmPath(c, name), ownedBackup); err != nil {
			t.Error(err)
			return
		}
		if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(foreignFile, []byte("foreign VM"), 0600); err != nil {
			t.Error(err)
		}
	}
	pid, _, err := driver.startOwned(context.Background(), c, name, s.View().Installation, id, []string{"run", "--no-graphics", "--no-audio", name})
	requireCode(t, err, ErrOwnership)
	if pid != fixtureTartPID {
		t.Fatalf("started Tart PID was lost after ownership recheck failed: got %d, want %d", pid, fixtureTartPID)
	}
	if aliasedName != tartRunAlias(id) {
		t.Fatalf("Tart start alias = %q, want %q", aliasedName, tartRunAlias(id))
	}
	if !fixture.running[name] {
		t.Fatal("Tart did not start the VM reached through the verified descriptor")
	}
	if _, err = os.Stat(foreignFile); err != nil {
		t.Fatal("replacement VM data was removed", err)
	}
	if err = removeTartRunAlias(c, tartRunAlias(id)); err != nil {
		t.Fatal(err)
	}
}

func TestTartStartDoesNotLaunchAfterCallerCancelsDuringPrecheck(t *testing.T) {
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
	driver, fixture := fakeTart(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.afterGet = func(got string) {
		if got != name {
			t.Errorf("owned start precheck inspected %q, want %q", got, name)
		}
		cancel()
	}
	_, _, err := driver.startOwned(ctx, c, name, s.View().Installation, id, []string{"run", "--no-graphics", "--no-audio", name})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("owned start error = %v, want context cancellation", err)
	}
	for _, args := range fixture.commands {
		if len(args) > 0 && args[0] == "run" {
			t.Fatalf("cancelled owned start launched Tart: %q", args)
		}
	}
	if fixture.running[name] {
		t.Fatal("cancelled owned start started the VM")
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

func TestTartCleanupKnownAbsenceRemovesStaleRunAliasBeforeOwnerRecord(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
		t.Fatal(err)
	}
	driver, _ := fakeTart(c)
	var checkedPID int
	driver.processAlive = func(pid int) (bool, error) {
		checkedPID = pid
		return false, nil
	}
	r := Runner{ID: id, Handle: Handle{VM: name, PID: 4343}}
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal(err)
	}
	if checkedPID != r.Handle.PID {
		t.Fatalf("checked Tart PID = %d, want %d", checkedPID, r.Handle.PID)
	}
	if _, err := os.Lstat(filepath.Join(c.Storage.Data, "tart", "vms", tartRunAlias(id))); !os.IsNotExist(err) {
		t.Fatalf("stale Tart run alias remains after cleanup: %v", err)
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
		t.Fatalf("owner record remains after confirmed absent cleanup: %v", err)
	}
}

func TestTartStopWaitsForDetachedProcessAfterVMStops(t *testing.T) {
	t.Run("process exits within cleanup context", func(t *testing.T) {
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
		if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(snapshot *Snapshot) error {
			snapshot.RunnerTartStarts = map[string]string{id: "fixture-process-start"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		driver, fixture := fakeTart(c)
		fixture.running[name] = true
		checks := 0
		driver.processAlive = func(pid int) (bool, error) {
			if pid != fixtureTartPID {
				t.Fatalf("checked Tart PID = %d, want %d", pid, fixtureTartPID)
			}
			checks++
			return checks < 3, nil
		}
		r := Runner{ID: id, Handle: Handle{VM: name, PID: fixtureTartPID}, Phase: Busy}
		if err := driver.Stop(context.Background(), c, r, s.View()); err != nil {
			t.Fatalf("stop failed while the detached process was exiting: %v", err)
		}
		if checks != 3 {
			t.Fatalf("Tart process liveness checks = %d, want 3", checks)
		}
		if present, err := tartRunAliasPresent(c, tartRunAlias(id)); err != nil || present {
			t.Fatalf("run alias remained after process exit: present=%t err=%v", present, err)
		}
	})

	t.Run("deadline retains run alias for retry", func(t *testing.T) {
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
		if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(snapshot *Snapshot) error {
			snapshot.RunnerTartStarts = map[string]string{id: "fixture-process-start"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		driver, fixture := fakeTart(c)
		fixture.running[name] = true
		driver.processAlive = func(pid int) (bool, error) { return true, nil }
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		r := Runner{ID: id, Handle: Handle{VM: name, PID: fixtureTartPID}, Phase: Busy}
		err := driver.Stop(ctx, c, r, s.View())
		requireCode(t, err, ErrCleanup)
		if present, aliasErr := tartRunAliasPresent(c, tartRunAlias(id)); aliasErr != nil || !present {
			t.Fatalf("cleanup deadline lost the run alias: present=%t err=%v", present, aliasErr)
		}
	})
}

func TestTartMissingCanonicalVMRetainsActiveRun(t *testing.T) {
	for _, action := range []string{"inspect", "stop", "cleanup"} {
		for _, aliasPresent := range []bool{true, false} {
			nameSuffix := "without-alias"
			if aliasPresent {
				nameSuffix = "with-alias"
			}
			t.Run(action+"/"+nameSuffix, func(t *testing.T) {
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
				if aliasPresent {
					if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
						t.Fatal(err)
					}
				}
				driver, fixture := fakeTart(c)
				checkedPID := 0
				driver.processAlive = func(pid int) (bool, error) {
					checkedPID = pid
					return pid == 4242, nil
				}
				if err := os.RemoveAll(vmPath(c, name)); err != nil {
					t.Fatal(err)
				}
				r := Runner{ID: id, Handle: Handle{VM: name, PID: 4242}, Phase: Busy}
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
				if checkedPID != r.Handle.PID {
					t.Fatalf("checked Tart PID = %d, want %d", checkedPID, r.Handle.PID)
				}
				if err = verifyVMOwnerRecord(c, name, s.View().Installation, id); err != nil {
					t.Fatal("live Tart run lost its durable owner record", err)
				}
				aliasPath := filepath.Join(c.Storage.Data, "tart", "vms", tartRunAlias(id))
				_, aliasErr := os.Lstat(aliasPath)
				if aliasPresent && aliasErr != nil {
					t.Fatal("live Tart run lost its liveness alias", aliasErr)
				}
				if !aliasPresent && !os.IsNotExist(aliasErr) {
					t.Fatalf("unexpected Tart run alias after inspection: %v", aliasErr)
				}
				if len(fixture.commands) != 0 {
					t.Fatalf("missing canonical VM caused Tart commands: %q", fixture.commands)
				}
			})
		}
	}
}

func TestTartInspectionQuarantinesMissingCanonicalLiveRun(t *testing.T) {
	m, c, _, _, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	name := "rm-" + id
	if err := claimVM(c, name, m.Store.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishVMOwnerMarker(c, name, m.Store.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		r.Backend = Tart
		r.Handle = Handle{VM: name, PID: 4242}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(vmPath(c, name)); err != nil {
		t.Fatal(err)
	}
	driver, _ := fakeTart(c)
	driver.processAlive = func(pid int) (bool, error) { return pid == 4242, nil }
	m.Drivers = func(Backend) (Driver, error) { return driver, nil }
	m.inspect(context.Background(), id)

	r := m.Store.View().Runners[id]
	if r.Phase != Quarantined || r.Problem == nil || r.Problem.Code != ErrOwnership {
		t.Fatalf("missing canonical live run was not quarantined: %#v", r)
	}
	_, count, _ := usage(m.Store.View())
	if count != 1 {
		t.Fatalf("live Tart execution reservation count = %d, want 1", count)
	}
	if err := verifyVMOwnerRecord(c, name, m.Store.View().Installation, id); err != nil {
		t.Fatal("live Tart run lost its durable owner record", err)
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
		if !tartConfigLockHeldByOtherProcess(t, filepath.Join(vmPath(c, deletionName), "config.json")) {
			t.Error("Tart config lock was released before the staged VM delete")
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
	fixture.beforeDelete = func(target string) {
		if target != deletionName {
			t.Errorf("Tart delete target = %q, want staged name %q", target, deletionName)
		}
		if !tartConfigLockHeldByOtherProcess(t, filepath.Join(vmPath(c, deletionName), "config.json")) {
			t.Error("recovered staged VM config lock was released before delete")
		}
	}
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

func TestTartCleanupRecognizesReusedPIDAsExited(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(snapshot *Snapshot) error {
		snapshot.RunnerTartStarts = map[string]string{id: "darwin:100:10"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driver, _ := fakeTart(c)
	driver.processAlive = func(pid int) (bool, error) {
		if pid != 4242 {
			t.Fatalf("checked Tart PID = %d, want 4242", pid)
		}
		return true, nil
	}
	driver.processIdentity = func(int) (string, error) { return "darwin:200:20", nil }
	runner := Runner{ID: id, Handle: Handle{VM: name, PID: 4242}}
	if err := driver.Cleanup(context.Background(), c, runner, s.View()); err != nil {
		t.Fatalf("PID reuse prevented cleanup of the confirmed-exited Tart process: %v", err)
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
		t.Fatalf("completed cleanup retained the owner record: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(c.Storage.Data, "tart", "vms", tartRunAlias(id))); !os.IsNotExist(err) {
		t.Fatalf("completed cleanup retained the stale Tart alias: %v", err)
	}
}

func TestTartProcessStartIdentityParser(t *testing.T) {
	stat := "123 (fixture ) process) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 987654 20"
	got, err := parseLinuxProcessStartIdentity(stat, "boot-id\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "linux:boot-id:987654" {
		t.Fatalf("process start identity = %q, want linux:boot-id:987654", got)
	}
	if _, err := parseLinuxProcessStartIdentity("123 (missing fields) S", "boot-id"); err == nil {
		t.Fatal("malformed process stat was accepted")
	}
}

func TestImageRemovalRetainsReservationWhileDetachedTartRunLives(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(context.Background(), c, ImageRequest{Action: "open", ID: im.ID}); err != nil {
		t.Fatal(err)
	}
	if got := s.View().ImageTartPIDs[im.ID]; got != fixtureTartPID {
		t.Fatalf("persisted setup Tart PID = %d, want %d", got, fixtureTartPID)
	}
	if err = os.RemoveAll(vmPath(c, im.VM)); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(c.Storage.Data, "tart", "vms", tartRunAlias(im.ID))); err != nil {
		t.Fatal(err)
	}
	var checkedPID int
	driver.processAlive = func(pid int) (bool, error) {
		checkedPID = pid
		return true, nil
	}
	_, err = images.Operate(context.Background(), c, ImageRequest{Action: "remove", ID: im.ID})
	requireCode(t, err, ErrOwnership)
	if checkedPID != fixtureTartPID {
		t.Fatalf("checked detached Tart PID = %d, want %d", checkedPID, fixtureTartPID)
	}
	current := s.View()
	if current.Images[im.ID] == nil || current.Images[im.ID].Phase != ImageOpen {
		t.Fatal("ambiguous removal discarded its image record")
	}
	if current.ImageTartPIDs[im.ID] != fixtureTartPID {
		t.Fatal("ambiguous removal discarded its detached Tart PID")
	}
	resources, count, vms := usage(current)
	if resources != im.Resources || count != 1 || vms != 1 {
		t.Fatalf("live image reservation was released: resources=%+v count=%d vms=%d", resources, count, vms)
	}
	if err = verifyVMOwnerRecord(c, im.VM, current.Installation, im.ID); err != nil {
		t.Fatal("ambiguous removal discarded the durable image owner record", err)
	}
}

func TestImageOpenDoesNotRelaunchWhileRecordedTartProcessLives(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-image-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(snapshot *Snapshot) error {
		snapshot.Images[id] = &Image{ID: id, VM: name, Phase: ImageOpen, Resources: Resources{CPU: 1, MemoryMiB: 512}}
		snapshot.ImageTartPIDs = map[string]int{}
		snapshot.ImageTartPIDs[id] = 123
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driver, fixture := fakeTart(c)
	driver.processAlive = func(pid int) (bool, error) {
		if pid != 123 {
			t.Fatalf("checked Tart PID = %d, want 123", pid)
		}
		return true, nil
	}
	images := &ImageManager{Store: s, Tart: driver}
	_, err := images.Operate(context.Background(), c, ImageRequest{Action: "open", ID: id})
	requireCode(t, err, ErrOwnership)
	for _, command := range fixture.commands {
		if len(command) > 0 && command[0] == "run" {
			t.Fatal("image open relaunched Tart while its recorded process was still alive")
		}
	}
	current := s.View()
	if current.Images[id].Phase != ImageOpen || current.ImageTartPIDs[id] != 123 {
		t.Fatal("retry changed the image phase or discarded its live Tart PID", current.Images[id], current.ImageTartPIDs[id])
	}
}

func TestImageSealDoesNotRelaunchWhilePreviousTartProcessLives(t *testing.T) {
	c, s := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{CPU: 1, MemoryMiB: 512}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = createTartCommandAlias(c, tartRunAlias(im.ID)); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(snapshot *Snapshot) error {
		if snapshot.ImageTartPIDs == nil {
			snapshot.ImageTartPIDs = map[string]int{}
		}
		if snapshot.ImageTartStarts == nil {
			snapshot.ImageTartStarts = map[string]string{}
		}
		snapshot.ImageTartPIDs[im.ID] = 123
		snapshot.ImageTartStarts[im.ID] = "fixture-process-start"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driver.processAlive = func(pid int) (bool, error) { return pid == 123, nil }
	_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
	requireCode(t, err, ErrOwnership)
	for _, command := range fixture.commands {
		if len(command) > 0 && command[0] == "run" {
			t.Fatal("image seal relaunched Tart while its recorded process was still alive")
		}
	}
	current := s.View()
	if current.ImageTartPIDs[im.ID] != 123 || current.ImageTartStarts[im.ID] != "fixture-process-start" {
		t.Fatal("image seal discarded the previous Tart process identity", current.ImageTartPIDs[im.ID], current.ImageTartStarts[im.ID])
	}
}

func TestImageSealStopsWithCurrentTartProcessIdentity(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{CPU: 1, MemoryMiB: 512}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(snapshot *Snapshot) error {
		if snapshot.ImageTartPIDs == nil {
			snapshot.ImageTartPIDs = map[string]int{}
		}
		if snapshot.ImageTartStarts == nil {
			snapshot.ImageTartStarts = map[string]string{}
		}
		snapshot.ImageTartPIDs[im.ID] = 122
		snapshot.ImageTartStarts[im.ID] = "previous-process-start"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checks := 0
	driver.processAlive = func(pid int) (bool, error) {
		if pid == 122 {
			return false, nil
		}
		if pid != fixtureTartPID {
			t.Fatalf("checked Tart PID = %d, want %d", pid, fixtureTartPID)
		}
		checks++
		return checks == 1, nil
	}
	_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
	if err != nil {
		t.Fatalf("image seal failed while the current Tart process exited: %v", err)
	}
	current := s.View()
	if checks != 2 {
		t.Fatalf("Tart process liveness checks = %d, want 2 to confirm its exit", checks)
	}
	if current.Images[im.ID].Phase != ImageSealed || current.ImageTartPIDs[im.ID] != 0 || current.ImageTartStarts[im.ID] != "" {
		t.Fatal("image seal did not clear the confirmed-exited Tart process state", current.Images[im.ID], current.ImageTartPIDs[im.ID], current.ImageTartStarts[im.ID])
	}
	if present, aliasErr := tartRunAliasPresent(c, tartRunAlias(im.ID)); aliasErr != nil || present {
		t.Fatal("image seal retained the alias after the Tart process exited", present, aliasErr)
	}
}

func TestImageReconcileOwnershipFailureReplacesPreparationProblem(t *testing.T) {
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-image-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmPath(c, name), "stale-preparation-state"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(snapshot *Snapshot) error {
		snapshot.Images[id] = &Image{
			ID:      id,
			VM:      name,
			Phase:   ImageOpen,
			Problem: problem(ErrPreparation, "Old preparation failure.", "Retry preparation."),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driver, _ := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	if err := images.Reconcile(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := s.View().Images[id].Problem; got == nil || got.Code != ErrOwnership {
		t.Fatalf("image reconciliation retained a lower-priority problem instead of ownership ambiguity: %#v", got)
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
