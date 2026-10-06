//go:build darwin || linux

package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary supplies bounded fixture processes only. No actual user
// service, GitHub registration, runner job, or account configuration is used.
func TestServiceReloadNativeProcess(t *testing.T) {
	mode := os.Getenv("RUNMOOR_RELOAD_PROCESS_FIXTURE")
	if mode == "" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	out := os.NewFile(3, "fixture-ready")
	switch mode {
	case "worker":
		out.Close()
		<-ctx.Done()
	case "handoff":
		out.Write([]byte("ready\n"))
		out.Close()
		os.Exit(waitServiceReloadHandoff(ctx, os.Getenv("RUNMOOR_RELOAD_FIXTURE_JOURNAL"), os.Getenv("RUNMOOR_RELOAD_FIXTURE_TOKEN")))
	case "manager":
		binary, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		worker := exec.Command(binary, "-test.run=^TestServiceReloadNativeProcess$")
		worker.Env = append(minimalEnv(), "RUNMOOR_RELOAD_PROCESS_FIXTURE=worker")
		null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		worker.Stdin = null
		worker.Stdout = null
		worker.Stderr = null
		worker.ExtraFiles = []*os.File{null}
		detach(worker)
		if worker.Start() != nil {
			os.Exit(2)
		}
		null.Close()
		start, err := tartRunProcessStartIdentity(worker.Process.Pid)
		if err != nil {
			worker.Process.Kill()
			os.Exit(2)
		}
		json.NewEncoder(out).Encode(HostProcess{PID: worker.Process.Pid, Start: start})
		out.Close()
		<-ctx.Done()
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func reloadNativeChild(t *testing.T, mode string, extra ...string) (*exec.Cmd, *os.File) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestServiceReloadNativeProcess$")
	cmd.Env = append(minimalEnv(), "RUNMOOR_RELOAD_PROCESS_FIXTURE="+mode)
	cmd.Env = append(cmd.Env, extra...)
	cmd.ExtraFiles = []*os.File{write}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		read.Close()
		write.Close()
		t.Fatal(err)
	}
	write.Close()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); read.Close() })
	if err := read.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return cmd, read
}

func TestNativeManagerOnlyExitPreservesDetachedExecution(t *testing.T) {
	manager, ready := reloadNativeChild(t, "manager")
	var worker HostProcess
	if err := json.NewDecoder(ready).Decode(&worker); err != nil || worker.PID <= 0 || worker.Start == "" {
		t.Fatalf("fixture readiness failed: %v", err)
	}
	t.Cleanup(func() {
		if alive, _ := tartRunProcessAlive(worker.PID, worker.Start); alive {
			process, _ := os.FindProcess(worker.PID)
			process.Signal(syscall.SIGTERM)
		}
	})
	if err := manager.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Wait(); err == nil {
		t.Fatal("manager was not force-replaced")
	}
	alive, err := tartRunProcessAlive(worker.PID, worker.Start)
	if err != nil || !alive {
		t.Fatalf("detached execution died with manager: %v", err)
	}
}

func TestNativeServiceHandoffExitsWithoutChangingState(t *testing.T) {
	f := newReloadFixture(t, "darwin")
	f.onCommand = func(command string) error {
		if strings.Contains(command, " bootout ") {
			return errors.New("retain helper intent")
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("expected pending helper")
	}
	j, err := readReloadJournal(f.r.Unit)
	if err != nil || j == nil {
		t.Fatal("missing fixture intent")
	}
	before, _ := json.Marshal(f.m.Store.View())
	journal, _ := os.ReadFile(reloadJournalPath(f.r.Unit))
	helper, ready := reloadNativeChild(t, "handoff", "RUNMOOR_RELOAD_FIXTURE_JOURNAL="+reloadJournalPath(f.r.Unit), "RUNMOOR_RELOAD_FIXTURE_TOKEN="+j.Token)
	buffer := make([]byte, 6)
	if n, err := ready.Read(buffer); err != nil || string(buffer[:n]) != "ready\n" {
		t.Fatalf("helper readiness: %v", err)
	}
	if err := helper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := helper.Wait(); err != nil {
		t.Fatal("helper did not exit cleanly", err)
	}
	after, _ := json.Marshal(f.m.Store.View())
	current, _ := os.ReadFile(reloadJournalPath(f.r.Unit))
	if string(before) != string(after) || string(journal) != string(current) {
		t.Fatal("helper modified manager state or intent")
	}
	if code := serviceReloadHandoff(reloadJournalPath(f.r.Unit), strconv.Itoa(os.Getpid())); code != 2 {
		t.Fatal("invalid helper authority accepted")
	}
}
