package runmoor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Real Task Scheduler acceptance is opt-in. It uses a unique disposable task,
// never the operator's installed task, credentials, configuration or state.
func TestWindowsScheduledTaskNativeLifecycle(t *testing.T) {
	if os.Getenv("RUNMOOR_WINDOWS_TASK_TEST") != "1" {
		t.Skip("set RUNMOOR_WINDOWS_TASK_TEST=1 for disposable Task Scheduler acceptance")
	}
	if e := windowsPlatform(context.Background()); e != nil {
		t.Skip("requires Windows 11 x64")
	}
	if _, err := os.Lstat(windowsReloadPath()); !os.IsNotExist(err) {
		t.Skip("preserve the operator's pending service replacement")
	}
	dir := windowsPrivateFixture(t)
	t.Setenv("LOCALAPPDATA", dir)
	old := windowsTaskName
	windowsTaskName = func() string { return "Runmoor-Test-" + filepath.Base(dir) + fmt.Sprint(os.Getpid()) }
	defer func() { windowsTaskName = old }()
	helper := filepath.Join(dir, "runmoor.exe")
	if out, e := exec.Command("go", "build", "-o", helper, "../..").CombinedOutput(); e != nil {
		t.Fatalf("build helper: %v %s", e, out)
	}
	path := filepath.Join(dir, "config.toml")
	body := fmt.Sprintf("schema_version=1\n[storage]\nstate='%s'\ndata='%s'\n[host]\nmax_runners=1\ncpu=1\nmemory_mib=1024\nmin_free_disk_mib=1\n", filepath.Join(dir, "state"), filepath.Join(dir, "data"))
	if e := writePrivateExclusive(path, []byte(body)); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig(path)
	if e != nil {
		t.Fatal(e)
	}
	sid, e := currentSID()
	if e != nil {
		t.Fatal(e)
	}
	m := windowsTaskManifest{Schema: 1, SID: sid.String(), Binary: helper, Config: path}
	m.XML = windowsTaskXML(helper, path, m.SID)
	if e = privateDir(filepath.Dir(windowsTaskPath())); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if e = registerWindowsTask(ctx, OSCommand{}, m, false); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = taskPS(context.Background(), OSCommand{}, `Stop-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\'; Unregister-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\' -Confirm:$false`)
	}()
	if e = inspectWindowsTask(ctx, OSCommand{}, m); e != nil {
		t.Fatal(e)
	}
	if _, e = taskPS(ctx, OSCommand{}, `Start-ScheduledTask -TaskName `+psQuote(windowsTaskName())+` -TaskPath '\'`); e != nil {
		t.Fatal(e)
	}
	for {
		_, e = SendControl(ctx, c, ControlRequest{Action: "status"})
		if e == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("task manager unavailable: %v", e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, peer, err := sendControlPeer(ctx, c, ControlRequest{Action: "status"}, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyWindowsManager(ctx, peer, m); err != nil {
		t.Fatal(err)
	}
	if _, e = SendControl(ctx, c, ControlRequest{Action: "stop"}); e != nil {
		t.Fatal(e)
	}
	if e = waitStopped(ctx, c, ""); e != nil {
		t.Fatal(e)
	}
	if e = inspectWindowsTask(ctx, OSCommand{}, m); e != nil {
		t.Fatal(e)
	}
}
