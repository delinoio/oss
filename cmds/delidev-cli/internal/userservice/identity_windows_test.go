package userservice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"golang.org/x/sys/windows"
)

func TestWindowsProcessImageOwnershipUsesCanonicalIdentity(t *testing.T) {
	m, _ := fixture(t)
	control(t, m, Install, 0)
	r, err := m.load()
	if err != nil {
		t.Fatal("installed identity unavailable")
	}
	p, err := process.ProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal("current process identity unavailable")
	}
	live := runtimeRecord{PID: p.PID, Birth: p.Birth}
	if verifyProcess(r.Spec, live) != nil {
		t.Fatal("current original executable rejected")
	}
	path, err := windows.UTF16PtrFromString(r.Spec.Binary)
	if err != nil {
		t.Fatal("installed image path invalid")
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(path, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatal("short image path observation unavailable")
	}
	if verifyProcessImage(r.Spec, windows.UTF16ToString(buffer[:n])) != nil {
		t.Fatal("original executable's short path rejected")
	}
	foreign := filepath.Join(m.Root, "foreign.exe")
	if os.WriteFile(foreign, []byte("foreign image"), 0600) != nil {
		t.Fatal("foreign image fixture unavailable")
	}
	if verifyProcessImage(r.Spec, foreign) == nil {
		t.Fatal("foreign executable path accepted")
	}
	changed := r.Spec
	changed.BinaryIdentity = "foreign"
	if verifyProcess(changed, live) == nil {
		t.Fatal("changed executable file identity accepted")
	}
	changed = r.Spec
	changed.User = "S-1-5-21-0-0-0-0"
	if verifyProcess(changed, live) == nil {
		t.Fatal("foreign process user accepted")
	}
	live.Birth = "foreign"
	if verifyProcess(r.Spec, live) == nil {
		t.Fatal("changed process birth accepted")
	}
}
