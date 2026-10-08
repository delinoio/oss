//go:build darwin

package runmoor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTartDarwinProcessIdentityResults(t *testing.T) {
	valid := unix.KinfoProc{}
	valid.Proc.P_pid = 4242
	valid.Proc.P_starttime.Sec = 100
	valid.Proc.P_starttime.Usec = 10
	for _, tc := range []struct {
		name    string
		entries []unix.KinfoProc
		err     error
		absent  bool
	}{
		{name: "empty successful result", absent: true},
		{name: "explicit ESRCH", err: unix.ESRCH, absent: true},
		{name: "real EIO", err: unix.EIO},
		{name: "permission failure", err: unix.EPERM},
		{name: "multiple records", entries: []unix.KinfoProc{valid, valid}},
		{name: "wrong PID", entries: []unix.KinfoProc{{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := tartDarwinProcessStartIdentity(4242, tc.entries, tc.err)
			if identity != "" || err == nil || os.IsNotExist(err) != tc.absent {
				t.Fatalf("identity=%q err=%v absent=%t", identity, err, tc.absent)
			}
			if tc.err != nil && !tc.absent && !errors.Is(err, tc.err) {
				t.Fatal("real query error was discarded")
			}
		})
	}
	if identity, err := tartDarwinProcessStartIdentity(4242, []unix.KinfoProc{valid}, nil); err != nil || identity != "darwin:100:10" {
		t.Fatalf("valid identity=%q err=%v", identity, err)
	}
	for _, started := range []unix.Timeval{{}, {Sec: -1}, {Sec: 100, Usec: -1}, {Sec: 100, Usec: 1_000_000}} {
		entry := valid
		entry.Proc.P_starttime = started
		if identity, err := tartDarwinProcessStartIdentity(4242, []unix.KinfoProc{entry}, nil); identity != "" || err == nil || os.IsNotExist(err) {
			t.Fatalf("malformed starttime=%v identity=%q err=%v", started, identity, err)
		}
	}
}

func TestTartDarwinProcessIdentityOwnedChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := exec.CommandContext(ctx, "/usr/bin/true")
	if err := finished.Run(); err != nil {
		t.Fatal(err)
	}
	if identity, err := tartRunProcessStartIdentity(finished.Process.Pid); identity != "" || !os.IsNotExist(err) {
		t.Fatalf("reaped child identity=%q err=%v", identity, err)
	}
	live := exec.CommandContext(ctx, "/bin/sleep", "10")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Process.Kill(); _ = live.Wait() }()
	if identity, err := tartRunProcessStartIdentity(live.Process.Pid); err != nil || identity == "" {
		t.Fatalf("live child identity=%q err=%v", identity, err)
	}
}

func TestTartDarwinCleanupProcessLookupInterleaving(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  error
		legacy bool
	}{
		{name: "exit after liveness"},
		{name: "real query failure", query: unix.EIO},
		{name: "legacy numeric PID remains conservative", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store := fixtureStore(t)
			id := newID()
			name := "rm-" + id
			if err := claimVM(c, name, store.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			if _, _, err := createTartCommandAlias(c, tartRunAlias(id)); err != nil {
				t.Fatal(err)
			}
			if !tc.legacy {
				if err := store.Update(func(s *Snapshot) error {
					s.RunnerTartStarts = map[string]string{id: "darwin:100:10"}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			driver, _ := fakeTart(c)
			driver.processAlive = func(int) (bool, error) { return true, nil }
			queries := 0
			driver.processIdentity = func(pid int) (string, error) {
				queries++
				return tartDarwinProcessStartIdentity(pid, nil, tc.query)
			}
			runner := Runner{ID: id, Handle: Handle{VM: name, PID: 4242}}
			err := driver.Cleanup(context.Background(), c, runner, store.View())
			if tc.query == nil && !tc.legacy {
				if err != nil {
					t.Fatal("confirmed exit blocked cleanup", err)
				}
				if queries == 0 {
					t.Fatal("fixture never looked up identity after liveness")
				}
				if _, err := os.Lstat(vmOwnerPath(c, name)); !os.IsNotExist(err) {
					t.Fatal("confirmed exit retained ownership record", err)
				}
				if _, err := os.Lstat(filepath.Join(c.Storage.Data, "tart", "vms", tartRunAlias(id))); !os.IsNotExist(err) {
					t.Fatal("confirmed exit retained alias", err)
				}
			} else {
				requireCode(t, err, ErrOwnership)
				if err := verifyVMOwnerRecord(c, name, store.View().Installation, id); err != nil {
					t.Fatal("uncertain process lost ownership", err)
				}
				if present, err := tartRunAliasPresent(c, tartRunAlias(id)); err != nil || !present {
					t.Fatal("uncertain process lost alias", err)
				}
				if tc.legacy && queries != 0 {
					t.Fatal("legacy PID unexpectedly gained identity authority")
				}
			}
		})
	}
}
