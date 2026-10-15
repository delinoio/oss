package runmoor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsSupervisorSurvivesManagerRestartAndStopsOwnedWorker(t *testing.T) {
	dir := windowsPrivateFixture(t)
	helper := filepath.Join(dir, "runmoor.exe")
	if out, e := exec.Command("go", "build", "-o", helper, "../..").CombinedOutput(); e != nil {
		t.Fatalf("build helper: %v %s", e, out)
	}
	source := filepath.Join(dir, "runner.go")
	code := `package main
import("os";"time")
func main(){if len(os.Args)!=4||os.Args[1]!="run"||os.Args[2]!="--jitconfig"||os.Args[3]!="private-fixture"{os.Exit(2)};for{time.Sleep(time.Second)}}`
	if e := os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	c := Config{Storage: Storage{State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}}
	store, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	d, root, e := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = root.MkdirAll("runner/bin", 0700); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"home", "tmp"} {
		if e = root.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if out, e := exec.Command("go", "build", "-o", filepath.Join(root.Name(), "runner/bin/Runner.Listener.exe"), source).CombinedOutput(); e != nil {
		t.Fatalf("build runner: %v %s", e, out)
	}
	if e = store.Update(func(s *Snapshot) error {
		s.Runners[d.ID] = &Runner{ID: d.ID, Backend: Host, Phase: Preparing}
		s.HostExecutions[d.ID] = &HostExecution{LaunchPending: true}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	in := HostBootstrap{Directory: d, Storage: c.Storage, JIT: "private-fixture", Deadline: time.Now().Add(20 * time.Second)}
	var supervisor HostProcess
	if e = (nativeHost{executable: helper}).Launch(ctx, root, in, func(p HostProcess) error { supervisor = p; return nil }); e != nil {
		t.Fatal(e)
	}
	defer (nativeHost{}).Stop(supervisor)
	var status HostExecutionStatus
	for {
		status, e = hostReadStatus(root, d)
		if e == nil && status.Phase == HostRunning {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("startup: %v %v", status, e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	store.Close()
	store = nil
	store, e = OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	if alive, e := (nativeHost{}).Alive(supervisor); e != nil || !alive {
		t.Fatalf("supervisor lost on manager close: %v", e)
	}
	if members, e := (nativeHost{}).Group(status.Worker); e != nil || len(members) == 0 {
		t.Fatalf("worker lost: %v", e)
	}
	if e = (nativeHost{}).Stop(supervisor); e != nil {
		t.Fatal(e)
	}
	for {
		members, e := (nativeHost{}).Group(status.Worker)
		if e == nil && len(members) == 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("owned worker survived supervisor termination: %v", e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	// Kernel image sections can outlive the final job accounting transition.
	// Production cleanup retains its journal and retries the same sharing failure.
	for {
		e = os.Remove(filepath.Join(root.Name(), "runner/bin/Runner.Listener.exe"))
		if e == nil || os.IsNotExist(e) {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("image cleanup: %v", e)
		}
		time.Sleep(30 * time.Millisecond)
	}
}
