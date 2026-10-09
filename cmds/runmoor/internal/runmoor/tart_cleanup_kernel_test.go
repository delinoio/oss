//go:build darwin || linux

package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type kernelCleanupProof struct {
	PID, ForeignPID, InheritedFD int
	OwnLock                      bool
}

// A test-owned executable implements Tart's real F_GETLK/F_SETLK behavior.
// It runs only through a generated script with a stable staged delete argument.
func TestTartCleanupExecProbe(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "delete" || !strings.HasPrefix(args[len(args)-1], "rm-delete-") {
		return
	}
	target := args[len(args)-1]
	home := os.Getenv("TART_HOME")
	if unix.Fchdir(6) != nil {
		os.Exit(20)
	}
	home, _ = os.Getwd()
	config := filepath.Join(home, "vms", target, "config.json")
	expected, err := os.Stat(config)
	if err != nil {
		os.Exit(21)
	}
	inherited := -1
	for fd := 3; fd < 64; fd++ {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
			continue
		}
		// FileInfo uses stat only; no config descriptor is opened or closed here.
		var targetStat unix.Stat_t
		if unix.Stat(config, &targetStat) == nil && st.Dev == targetStat.Dev && st.Ino == targetStat.Ino {
			flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			if e == nil && flags&unix.FD_CLOEXEC == 0 {
				inherited = fd
				break
			}
		}
	}
	if inherited < 0 || !expected.Mode().IsRegular() {
		os.Exit(22)
	}
	binary, _ := os.Executable()
	foreign := exec.Command(binary, "-test.run=^TestTartCleanupForeignProbe$", "--", config)
	foreign.Env = minimalEnv()
	output, err := foreign.Output()
	if err != nil {
		os.Exit(23)
	}
	holder, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || holder != os.Getpid() {
		os.Exit(24)
	}
	// Tart queries running() and acquires deletion ownership in this same PID.
	own, err := os.OpenFile(config, os.O_RDWR, 0)
	if err != nil {
		os.Exit(25)
	}
	query := unix.Flock_t{Type: unix.F_RDLCK, Whence: 0}
	if unix.FcntlFlock(own.Fd(), unix.F_GETLK, &query) != nil || query.Type != unix.F_UNLCK || query.Pid != 0 {
		os.Exit(26)
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0}
	if unix.FcntlFlock(own.Fd(), unix.F_SETLK, &lock) != nil {
		os.Exit(27)
	}
	proof, _ := json.Marshal(kernelCleanupProof{os.Getpid(), holder, inherited, true})
	if os.WriteFile(filepath.Join(home, "cleanup-proof.json"), proof, 0600) != nil {
		os.Exit(28)
	}
	if _, err := os.Stat(filepath.Join(home, "block-delete")); err == nil {
		time.Sleep(30 * time.Second)
	}
	// Preserve replacement at the original name: only the claimed stage is removed.
	if os.RemoveAll(filepath.Join(home, "vms", target)) != nil {
		os.Exit(29)
	}
	own.Close()
	os.Exit(0)
}
func TestTartCleanupForeignProbe(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	config := os.Args[len(os.Args)-1]
	f, err := os.OpenFile(config, os.O_RDWR, 0)
	if err != nil {
		os.Exit(31)
	}
	defer f.Close()
	query := unix.Flock_t{Type: unix.F_RDLCK, Whence: 0}
	if unix.FcntlFlock(f.Fd(), unix.F_GETLK, &query) != nil || query.Type != unix.F_WRLCK || query.Pid <= 0 {
		os.Exit(32)
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0}
	err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock)
	if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EACCES) {
		os.Exit(33)
	}
	fmt.Print(query.Pid)
	os.Exit(0)
}
func TestTartCleanupForeignHolder(t *testing.T) {
	if os.Getenv("RUNMOOR_TART_LOCK_HOLDER") != "1" {
		return
	}
	dir := os.NewFile(3, "fixture-vm")
	lock, err := lockTartVMConfigAt(dir)
	if err != nil {
		os.Exit(41)
	}
	defer lock.Close()
	ready := os.NewFile(4, "fixture-ready")
	ready.Write([]byte("ready"))
	ready.Close()
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

type kernelCleanupExecutor struct {
	*tartFixture
	helper string
	before func(tartCleanupRequest, []*os.File)
	after  func(tartCleanupRequest)
}

func (e *kernelCleanupExecutor) RunTartCleanup(ctx context.Context, req tartCleanupRequest, files []*os.File) error {
	if e.before != nil {
		e.before(req, files)
	}
	err := runTartCleanupCommand(ctx, e.helper, []string{"__tart-cleanup"}, minimalEnv(), req, files)
	if err == nil && e.after != nil {
		e.after(req)
	}
	return err
}
func buildCleanupHelper(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runmoor")
	cmd := exec.Command("go", "build", "-o", path, "../..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build private cleanup helper: %v %s", err, out)
	}
	return path
}
func kernelCleanupFixture(t *testing.T, helper string) (Config, *Store, *TartDriver, Runner, *kernelCleanupExecutor) {
	t.Helper()
	c, s := fixtureStore(t)
	id := newID()
	name := "rm-" + id
	if err := claimVM(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath(c, name), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"config.json", "disk.img", "nvram.bin"} {
		if err := os.WriteFile(filepath.Join(vmPath(c, name), file), []byte("test-owned stopped VM"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishVMOwnerMarker(c, name, s.View().Installation, id); err != nil {
		t.Fatal(err)
	}
	binary, _ := os.Executable()
	script := filepath.Join(t.TempDir(), "fixture-tart")
	// All text is a generated absolute test executable path, shell-quoted literally.
	quoted := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestTartCleanupExecProbe$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.TartExecutable = script
	driver, fixture := fakeTart(c)
	executor := &kernelCleanupExecutor{tartFixture: fixture, helper: helper}
	driver.Exec = executor
	return c, s, driver, Runner{ID: id, Handle: Handle{VM: name}}, executor
}
func TestTartCleanupSamePIDKernelExec(t *testing.T) {
	helper := buildCleanupHelper(t)
	for _, staged := range []bool{false, true} {
		t.Run(fmt.Sprintf("staged=%t", staged), func(t *testing.T) {
			c, s, driver, r, _ := kernelCleanupFixture(t, helper)
			if staged {
				if err := renameTartVMNoReplace(vmPath(c, r.Handle.VM), vmPath(c, deletionVMName(r.ID))); err != nil {
					t.Fatal(err)
				}
			}
			if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(tartHome(c), "cleanup-proof.json"))
			var proof kernelCleanupProof
			if err != nil || json.Unmarshal(b, &proof) != nil || proof.PID != proof.ForeignPID || !proof.OwnLock || proof.InheritedFD < 3 {
				t.Fatalf("lock/exec proof missing: %+v %v", proof, err)
			}
			if _, err := os.Stat(vmOwnerPath(c, r.Handle.VM)); !os.IsNotExist(err) {
				t.Fatal("successful cleanup retained record")
			}
		})
	}
}
func TestTartCleanupChildRejectsForeignLockAndRetainsRecovery(t *testing.T) {
	helper := buildCleanupHelper(t)
	c, s, driver, r, e := kernelCleanupFixture(t, helper)
	vm, err := openTartVMDirectory(vmPath(c, r.Handle.VM))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	read, write, _ := os.Pipe()
	wait, release, _ := os.Pipe()
	binary, _ := os.Executable()
	holder := exec.Command(binary, "-test.run=^TestTartCleanupForeignHolder$")
	holder.Env = append(minimalEnv(), "RUNMOOR_TART_LOCK_HOLDER=1")
	holder.ExtraFiles = []*os.File{vm, write}
	holder.Stdin = wait
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	wait.Close()
	t.Cleanup(func() { release.Close(); holder.Process.Kill(); holder.Wait() })
	ready := make([]byte, 5)
	if _, err := io.ReadFull(read, ready); err != nil {
		t.Fatal(err)
	}
	read.Close()
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err == nil {
		t.Fatal("foreign lock authorized deletion")
	}
	if _, err := os.Stat(vmPath(c, r.Handle.VM)); err != nil {
		t.Fatal("foreign-held original was moved")
	}
	if _, err := os.Stat(vmOwnerPath(c, r.Handle.VM)); err != nil {
		t.Fatal("foreign lock lost owner record")
	}
	release.Close()
	if err := holder.Wait(); err != nil {
		t.Fatal(err)
	}
	e.before = nil
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal("cleanup did not recover after foreign exit", err)
	}
}
func TestTartCleanupChildRejectsReplacedAuthority(t *testing.T) {
	helper := buildCleanupHelper(t)
	for _, kind := range []string{"directory", "marker", "record", "namespace"} {
		t.Run(kind, func(t *testing.T) {
			c, s, driver, r, e := kernelCleanupFixture(t, helper)
			e.before = func(req tartCleanupRequest, _ []*os.File) {
				switch kind {
				case "directory":
					old := vmPath(c, r.Handle.VM)
					if err := os.Rename(old, old+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(old, 0700); err != nil {
						t.Fatal(err)
					}
				case "marker":
					if err := os.WriteFile(vmOwnerMarkerPath(c, r.Handle.VM), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				case "record":
					path := vmOwnerPath(c, r.Handle.VM)
					b, _ := os.ReadFile(path)
					if err := os.Rename(path, path+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, b, 0600); err != nil {
						t.Fatal(err)
					}
				case "namespace":
					path := filepath.Join(tartHome(c), "vms")
					if err := os.Rename(path, path+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := driver.Cleanup(context.Background(), c, r, s.View())
			p, ok := err.(*Problem)
			if !ok || p.Code != ErrOwnership {
				t.Fatalf("replaced authority error=%v", err)
			}
			if _, err := os.Stat(vmOwnerPath(c, r.Handle.VM)); err != nil {
				t.Fatal("rejection removed owner record")
			}
		})
	}
}
func TestTartCleanupChildCancellationRetainsStagedOwnership(t *testing.T) {
	helper := buildCleanupHelper(t)
	c, s, driver, r, _ := kernelCleanupFixture(t, helper)
	if err := os.WriteFile(filepath.Join(tartHome(c), "block-delete"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- driver.Cleanup(ctx, c, r, s.View()) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("child ended before locked exec: %v", err)
		default:
		}
		if _, err := os.Stat(filepath.Join(tartHome(c), "cleanup-proof.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not reach locked exec")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation result=%v", err)
	}
	target := deletionVMName(r.ID)
	if err := verifyVMOwnerAt(c, target, r.Handle.VM, s.View().Installation, r.ID); err != nil {
		t.Fatal("cancellation lost staged ownership", err)
	}
	if err := os.Remove(filepath.Join(tartHome(c), "block-delete")); err != nil {
		t.Fatal(err)
	}
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal("retry did not finish staged cleanup", err)
	}
}

// Negative controls prove that the exec fixture detects both ways to lose a
// POSIX lock: closing another descriptor for that inode and CLOEXEC on exec.
func TestTartCleanupDescriptorFaultChild(t *testing.T) {
	fault := os.Getenv("RUNMOOR_TART_LOCK_FAULT")
	if fault == "" {
		return
	}
	var req tartCleanupRequest
	if json.NewDecoder(os.Stdin).Decode(&req) != nil {
		os.Exit(51)
	}
	files := []*os.File{os.NewFile(3, "vm"), os.NewFile(4, "namespace"), os.NewFile(5, "record"), os.NewFile(6, "home")}
	lock, err := prepareTartCleanup(req, files)
	if err != nil {
		os.Exit(52)
	}
	if fault == "close" {
		if _, err := unix.FcntlInt(lock.Fd(), unix.F_SETFD, 0); err != nil {
			os.Exit(53)
		}
		other, err := openTartVMFileAt(files[0], "config.json")
		if err != nil {
			os.Exit(54)
		}
		other.Close()
	}
	if unix.Exec(req.Executable, []string{req.Executable, "delete", deletionVMName(req.Owner.Entity)}, tartEnvAt(req.config(), "/dev/fd/6")) != nil {
		os.Exit(55)
	}
	os.Exit(56)
}
func TestTartCleanupKernelDetectsCloseAndCLOEXEC(t *testing.T) {
	helper := buildCleanupHelper(t)
	for _, fault := range []string{"close", "cloexec"} {
		t.Run(fault, func(t *testing.T) {
			c, s, driver, r, _ := kernelCleanupFixture(t, helper)
			files, err := openTartCleanupFiles(c, r.Handle.VM, r.Handle.VM, s.View().Installation, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer closeTartCleanupFiles(files)
			req := tartCleanupRequest{Data: c.Storage.Data, Executable: c.TartExecutable, Source: r.Handle.VM, Owner: vmOwner{s.View().Installation, r.ID, r.Handle.VM}}
			binary, _ := os.Executable()
			if err := runTartCleanupCommand(context.Background(), binary, []string{"-test.run=^TestTartCleanupDescriptorFaultChild$"}, append(minimalEnv(), "RUNMOOR_TART_LOCK_FAULT="+fault), req, files); err == nil {
				t.Fatal("lost lock was accepted by kernel exec fixture")
			}
			if err := verifyVMOwnerAt(c, deletionVMName(r.ID), r.Handle.VM, s.View().Installation, r.ID); err != nil {
				t.Fatal("failed exec lost recoverable stage", err)
			}
			if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
				t.Fatal("correct inherited lock did not recover", err)
			}
		})
	}
}
func TestTartCleanupInterruptedExecRetainsOriginalStage(t *testing.T) {
	helper := buildCleanupHelper(t)
	for _, status := range []string{"7", "64"} {
		t.Run(status, func(t *testing.T) {
			c, s, driver, r, _ := kernelCleanupFixture(t, helper)
			original := c.TartExecutable
			failing := filepath.Join(t.TempDir(), "failed-tart")
			if err := os.WriteFile(failing, []byte("#!/bin/sh\nexit "+status+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			c.TartExecutable = failing
			err := driver.Cleanup(context.Background(), c, r, s.View())
			p, ok := err.(*Problem)
			if !ok || p.Code != ErrCleanup {
				t.Fatalf("native exit was misclassified: %v", err)
			}
			if err := verifyVMOwnerAt(c, deletionVMName(r.ID), r.Handle.VM, s.View().Installation, r.ID); err != nil {
				t.Fatal("interrupted exec lost stage", err)
			}
			c.TartExecutable = original
			if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
				t.Fatal("failed exec was not recoverable", err)
			}
		})
	}
}
func TestTartCleanupCancelledBeforeChildPreservesOriginal(t *testing.T) {
	helper := buildCleanupHelper(t)
	c, s, driver, r, _ := kernelCleanupFixture(t, helper)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := driver.Cleanup(ctx, c, r, s.View()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled result=%v", err)
	}
	if err := verifyVMOwner(c, r.Handle.VM, s.View().Installation, r.ID); err != nil {
		t.Fatal("pre-child cancellation changed original", err)
	}
	if _, err := os.Stat(vmPath(c, deletionVMName(r.ID))); !os.IsNotExist(err) {
		t.Fatal("pre-child cancellation staged a VM")
	}
}

func TestTartCleanupRetainedNamespaceRejectsFalseAbsence(t *testing.T) {
	helper := buildCleanupHelper(t)
	c, s, driver, r, e := kernelCleanupFixture(t, helper)
	noop := filepath.Join(t.TempDir(), "noop-tart")
	if err := os.WriteFile(noop, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.TartExecutable = noop
	oldHome := tartHome(c) + "-original"
	e.after = func(tartCleanupRequest) {
		if err := os.Rename(tartHome(c), oldHome); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tartHome(c), "vms"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	err := driver.Cleanup(context.Background(), c, r, s.View())
	p, ok := err.(*Problem)
	if !ok || p.Code != ErrCleanup {
		t.Fatalf("replacement home substituted absence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldHome, "vms", deletionVMName(r.ID), vmOwnerMarkerName)); err != nil {
		t.Fatal("false receipt lost original stage", err)
	}
	if _, err := os.Stat(vmOwnerPath(c, r.Handle.VM)); err != nil {
		t.Fatal("false receipt removed owner record")
	}
}
func TestTartCleanupRejectsSameInodeAuthorityAliases(t *testing.T) {
	helper := buildCleanupHelper(t)
	for _, alias := range []string{"marker", "record"} {
		t.Run(alias, func(t *testing.T) {
			c, s, driver, r, _ := kernelCleanupFixture(t, helper)
			config := filepath.Join(vmPath(c, r.Handle.VM), "config.json")
			if err := os.Remove(config); err != nil {
				t.Fatal(err)
			}
			source := vmOwnerMarkerPath(c, r.Handle.VM)
			if alias == "record" {
				source = vmOwnerPath(c, r.Handle.VM)
			}
			if err := os.Link(source, config); err != nil {
				t.Fatal(err)
			}
			err := driver.Cleanup(context.Background(), c, r, s.View())
			p, ok := err.(*Problem)
			if !ok || p.Code != ErrOwnership {
				t.Fatalf("same-inode authority alias accepted: %v", err)
			}
			if _, err := os.Stat(vmPath(c, r.Handle.VM)); err != nil {
				t.Fatal("alias rejection staged original VM")
			}
		})
	}
}

func TestTartCleanupGetKernelProbe(t *testing.T) {
	if os.Getenv("RUNMOOR_TART_GET_KERNEL") != "1" {
		return
	}
	dir := os.NewFile(3, "pinned-vm")
	fd, err := unix.Openat(3, "config.json", unix.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		os.Exit(61)
	}
	defer unix.Close(fd)
	defer dir.Close()
	query := unix.Flock_t{Type: unix.F_RDLCK, Whence: 0}
	if unix.FcntlFlock(uintptr(fd), unix.F_GETLK, &query) != nil {
		os.Exit(62)
	}
	running := query.Type != unix.F_UNLCK && query.Pid > 0
	attempt := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0}
	err = unix.FcntlFlock(uintptr(fd), unix.F_SETLK, &attempt)
	if running && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EACCES) || !running && err != nil {
		os.Exit(63)
	}
	json.NewEncoder(os.Stdout).Encode(vmInfo{Running: running, State: "stopped", OS: tartGuestOSDarwin, CPU: 1, Memory: 512})
	os.Exit(0)
}

type kernelGetCleanupExecutor struct{ *kernelCleanupExecutor }

func (e *kernelGetCleanupExecutor) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	if len(args) == 0 || args[0] != "get" {
		return e.kernelCleanupExecutor.RunPinned(ctx, name, args, env, in, dir)
	}
	binary, _ := os.Executable()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestTartCleanupGetKernelProbe$")
	cmd.Env = append(minimalEnv(), "RUNMOOR_TART_GET_KERNEL=1")
	cmd.ExtraFiles = []*os.File{dir}
	return cmd.Output()
}
func TestTartCleanupDoesNotSelfReportRunning(t *testing.T) {
	helper := buildCleanupHelper(t)
	c, s, driver, r, e := kernelCleanupFixture(t, helper)
	driver.Exec = &kernelGetCleanupExecutor{e}
	driver.GuestExecutable = helper
	if err := driver.Cleanup(context.Background(), c, r, s.View()); err != nil {
		t.Fatal("stopped owned VM cleanup conflicted with its own lock", err)
	}
	if _, err := os.Stat(vmPath(c, deletionVMName(r.ID))); !os.IsNotExist(err) {
		t.Fatal("cleanup left claimed stage")
	}
}

func (e *kernelGetCleanupExecutor) RunTartCleanup(ctx context.Context, req tartCleanupRequest, files []*os.File) error {
	return (OSCommand{}).RunTartCleanup(ctx, req, files)
}
