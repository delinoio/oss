package runmoor

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestLinuxReloadRetainsProcessAcrossDispatchReplacement(t *testing.T) {
	for _, scenario := range []string{"normal", "changed MainPID", "foreign MainPID", "original exited and PID reused", "original exited at acquisition"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			original := f.pid
			pool := sortedPools(f.m.Store.View())[0].ID
			worker := seedRunner(t, f.m, pool, Busy)
			before := *f.m.Store.View().Runners[worker]
			if scenario == "original exited at acquisition" {
				f.onOpenManager = func(pid int) error { f.replaceManager(); return syscall.ESRCH }
			} else {
				f.onSignalManager = func(pid int) error {
					j, err := readReloadJournal(f.r.Unit)
					if err != nil || j == nil || j.PID != original || j.ProcessStart != "fixture:"+strconv.Itoa(original) || j.Stage != reloadRestartPending || f.pidfdClosed != 0 {
						t.Fatal("descriptor did not retain journaled generation through dispatch", err)
					}
					if pid != original {
						t.Fatal("signal target lost original process")
					}
					switch scenario {
					case "changed MainPID":
						f.replaceManager()
					case "foreign MainPID":
						f.pid, f.peer = 909, 909
						f.args[909] = []string{"/fixture/foreign", "run", "--config", f.path}
					case "original exited and PID reused":
						body, err := os.ReadFile(f.r.Unit)
						if err != nil {
							t.Fatal(err)
						}
						f.args[original], err = reloadArguments("linux", body)
						if err != nil {
							t.Fatal(err)
						}
						f.version = f.r.Version
						return syscall.ESRCH
					}
					return nil
				}
			}
			err := f.reload()
			if (err != nil) != (scenario == "foreign MainPID") {
				t.Fatalf("reload=%v scenario=%s", err, scenario)
			}
			if !slices.Equal(f.pidfdOpened, []int{original}) {
				t.Fatal("descriptor opened another manager", f.pidfdOpened)
			}
			for _, pid := range f.pidfdSignaled {
				if pid != original {
					t.Fatal("signal redirected to a replacement", pid)
				}
			}
			for _, command := range f.commands {
				if strings.Contains(command, "systemctl --user kill") {
					t.Fatal("unit-name fallback regained authority")
				}
			}
			wantClosed := 1
			if scenario == "original exited at acquisition" {
				wantClosed = 0
			}
			if f.pidfdClosed != wantClosed {
				t.Fatal("retained handle leaked", f.pidfdClosed)
			}
			if !reflect.DeepEqual(*f.m.Store.View().Runners[worker], before) {
				t.Fatal("manager replacement modified independent execution")
			}
			if scenario == "foreign MainPID" {
				j, err := readReloadJournal(f.r.Unit)
				if err != nil || j == nil || j.Stage != reloadRestartPending || j.PID != original {
					t.Fatal("uncertain replacement discarded original authority", err)
				}
			}
		})
	}
}

func TestLinuxReloadHandleAcquisitionAndIdentityFailuresRetainJournal(t *testing.T) {
	for _, scenario := range []string{"acquisition denied", "unsupported pidfd", "PID reused before proof", "MainPID changed before proof", "concurrent Stop", "Stop after final proof", "signal denied"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			original := f.pid
			pool := sortedPools(f.m.Store.View())[0].ID
			worker := seedRunner(t, f.m, pool, Busy)
			before := *f.m.Store.View().Runners[worker]
			processStart := f.r.ProcessStart
			f.onOpenManager = func(pid int) error {
				if pid != original {
					t.Fatal("opened stale or unrelated generation")
				}
				switch scenario {
				case "acquisition denied":
					return syscall.EPERM
				case "unsupported pidfd":
					return syscall.ENOSYS
				case "PID reused before proof":
					f.r.ProcessStart = func(pid int) (string, error) {
						if pid == original {
							return "reused", nil
						}
						return processStart(pid)
					}
				case "MainPID changed before proof":
					f.pid = 909
					f.args[909] = []string{"/foreign"}
				case "concurrent Stop":
					if err := f.m.Stop(false); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if scenario == "Stop after final proof" {
				proofReads := 0
				f.r.ProcessStart = func(pid int) (string, error) {
					if pid == original && len(f.pidfdOpened) > 0 {
						proofReads++
						if proofReads == 2 {
							if err := f.m.Stop(false); err != nil {
								t.Fatal(err)
							}
						}
					}
					return processStart(pid)
				}
			}
			if scenario == "signal denied" {
				f.onSignalManager = func(int) error { return syscall.EPERM }
			}
			if err := f.reload(); err == nil {
				t.Fatal("uncertain process authority claimed completion")
			}
			if len(f.pidfdSignaled) != 0 {
				t.Fatal("uncertain generation received a signal")
			}
			j, err := readReloadJournal(f.r.Unit)
			if err != nil || j == nil || j.PID != original || j.ProcessStart != "fixture:"+strconv.Itoa(original) {
				t.Fatal("original journal identity lost", err)
			}
			want := reloadPublished
			if scenario == "signal denied" {
				want = reloadRestartPending
			}
			if j.Stage != want {
				t.Fatalf("stage=%s want=%s", j.Stage, want)
			}
			if !reflect.DeepEqual(*f.m.Store.View().Runners[worker], before) {
				t.Fatal("process authority failure changed a worker")
			}
			if (scenario == "concurrent Stop" || scenario == "Stop after final proof") && !f.m.Store.View().Stopping {
				t.Fatal("replacement cleared Stop")
			}
			for _, command := range f.commands {
				if strings.Contains(command, "systemctl --user kill") {
					t.Fatal("process failure used unit-name fallback")
				}
			}
			closed := 1
			if scenario == "acquisition denied" || scenario == "unsupported pidfd" {
				closed = 0
			}
			if f.pidfdClosed != closed {
				t.Fatal("process handle was not released")
			}
		})
	}
}

func TestLinuxReloadCheckpointsAdmittedOlderManagerGeneration(t *testing.T) {
	f := newReloadFixture(t, "linux")
	f.onCommand = func(command string) error {
		if strings.Contains(command, " daemon-reload") {
			return errors.New("fixture interruption")
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("fixture did not retain published journal")
	}
	j, err := readReloadJournal(f.r.Unit)
	if err != nil || j == nil || j.Stage != reloadPublished {
		t.Fatal("missing old authority")
	}
	oldPID := j.PID
	f.onCommand = nil
	f.pid, f.peer = 404, 404
	f.version = j.PreviousVersion
	f.args[404] = f.args[oldPID]
	f.onSignalManager = func(pid int) error {
		current, err := readReloadJournal(f.r.Unit)
		if err != nil || current == nil || current.PID != 404 || current.ProcessStart != "fixture:404" || current.Stage != reloadRestartPending || pid != 404 {
			t.Fatal("stale journal PID used after older-manager restart", err)
		}
		return nil
	}
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.pidfdOpened, []int{404}) || !slices.Equal(f.pidfdSignaled, []int{404}) {
		t.Fatal("retained stale process instead of admitted older manager", f.pidfdOpened, f.pidfdSignaled)
	}
}

func TestLinuxReloadRejectsRestartedPIDReuseAtAcquisition(t *testing.T) {
	f := newReloadFixture(t, "linux")
	f.onCommand = func(command string) error {
		if strings.Contains(command, " daemon-reload") {
			return errors.New("fixture interruption")
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("fixture did not retain intent")
	}
	original, err := readReloadJournal(f.r.Unit)
	if err != nil || original == nil {
		t.Fatal("missing original journal", err)
	}
	f.onCommand = nil
	f.pid, f.peer = 404, 404
	f.version = original.PreviousVersion
	f.args[404] = f.args[original.PID]
	processStart := f.r.ProcessStart
	f.onOpenManager = func(pid int) error {
		if pid != 404 {
			t.Fatal("did not acquire admitted older manager")
		}
		f.r.ProcessStart = func(pid int) (string, error) {
			if pid == 404 {
				return "recycled-at-acquisition", nil
			}
			return processStart(pid)
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("recycled generation gained original authority")
	}
	current, err := readReloadJournal(f.r.Unit)
	if err != nil || current == nil || current.PID != original.PID || current.ProcessStart != original.ProcessStart || current.Stage != reloadPublished {
		t.Fatal("recycled identity was checkpointed", err)
	}
	if len(f.pidfdSignaled) != 0 || f.pidfdClosed != 1 {
		t.Fatal("uncertain restart was signaled or its descriptor leaked")
	}
}
