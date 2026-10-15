package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func windowsPrivateFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private")
	if e := privateDir(path); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestWindowsPrivateLockAndState(t *testing.T) {
	dir := windowsPrivateFixture(t)
	path := filepath.Join(dir, "lock")
	first, e := lockState(path)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := lockState(path); e == nil {
		unlockState(second)
		t.Fatal("second lock succeeded")
	}
	unlockState(first)
	second, e := lockState(path)
	if e != nil {
		t.Fatal(e)
	}
	unlockState(second)
	c := Config{Storage: Storage{State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}}
	store, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	id := store.View().Installation
	store.Close()
	snapshot, e := ReadSnapshot(c)
	if e != nil || snapshot.Installation != id {
		t.Fatalf("snapshot: %v", e)
	}
}
func TestWindowsPrivateACLRejectsOtherUsers(t *testing.T) {
	dir := windowsPrivateFixture(t)
	path := filepath.Join(dir, "secret")
	f, e := openPrivate(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if f, e = openPrivate(path, os.O_RDONLY); e == nil {
		f.Close()
		t.Fatal("public ACL accepted")
	}
}
func TestWindowsControlNamedPipe(t *testing.T) {
	dir := windowsPrivateFixture(t)
	c := Config{Storage: Storage{State: dir}}
	listener, e := listenControl(c)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ControlResponse{SchemaVersion: 1})
	})}
	go server.Serve(listener)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, pid, e := sendControlPeer(ctx, c, ControlRequest{Action: "status"}, os.Getpid(), true)
	if e != nil || pid != os.Getpid() {
		t.Fatalf("pid=%d error=%v", pid, e)
	}
}
func TestWindowsHostDirectoryIdentity(t *testing.T) {
	dir := windowsPrivateFixture(t)
	c := Config{Storage: Storage{State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}}
	store, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	d, root, e := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
	if e != nil {
		t.Fatal(e)
	}
	root.Close()
	root, e = openHostDirectory(c, d, store.View().Installation, false)
	if e != nil {
		t.Fatal(e)
	}
	root.Close()
	if e = removeHostDirectory(context.Background(), store, c, d); e != nil {
		t.Fatal(e)
	}
}
func TestWindowsTaskDefinitionRejectsExternalEdits(t *testing.T) {
	sid, e := currentSID()
	if e != nil {
		t.Fatal(e)
	}
	m := windowsTaskManifest{Schema: 1, SID: sid.String(), Binary: `C:\Program Files\Runmoor\runmoor.exe`, Config: `C:\Users\test\Runmoor\config.toml`}
	m.XML = windowsTaskXML(m.Binary, m.Config, m.SID)
	if !validWindowsTaskXML([]byte(m.XML), m) {
		t.Fatal("own definition rejected")
	}
	other := windowsTaskXML(m.Binary, m.Config, "S-1-5-18")
	if validWindowsTaskXML([]byte(other), m) {
		t.Fatal("foreign principal accepted")
	}
}
func TestWindowsNativeProcessIdentity(t *testing.T) {
	p, e := windowsProcess(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	alive, e := (nativeHost{}).Alive(p)
	if e != nil || !alive {
		t.Fatal(e)
	}
	p.Start = "windows:reused"
	if alive, e = (nativeHost{}).Alive(p); e != nil || alive {
		t.Fatal("reused PID accepted")
	}
}
func TestWindowsJobKillsDescendants(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/d", "/c", "more")
	pipe, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer pipe.Close()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	detach(cmd)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	p, e := windowsProcess(cmd.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	job, e := windows.CreateJobObject(nil, hostJobName(p))
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(job)
	handle, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.PID))
	if e != nil {
		t.Fatal(e)
	}
	e = windows.AssignProcessToJobObject(job, handle)
	windows.CloseHandle(handle)
	if e != nil {
		t.Fatal(e)
	}
	if members, e := (nativeHost{}).Group(p); e != nil || len(members) == 0 {
		t.Fatalf("members=%v error=%v", members, e)
	}
	if e = windows.TerminateJobObject(job, 1); e != nil {
		t.Fatal(e)
	}
	until := time.Now().Add(5 * time.Second)
	for {
		members, e := (nativeHost{}).Group(p)
		if e == nil && len(members) == 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("job did not terminate: %v", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
