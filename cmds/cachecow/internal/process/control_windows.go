package process

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func privateRoot(path string, _ os.FileInfo) error {
	// Require an owner-only DACL on the supplied storage root. Do not silently
	// rewrite a caller's ACL or accept Go's synthetic Windows permission bits.
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !windows.EqualSid(owner, tokenUser.User.Sid) {
		return infrastructureError("state directory has a different owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return infrastructureError("state directory needs a private DACL")
	}
	// The parent storage layer must establish an owner-only ACL. Conservatively
	// reject every allow ACE for a different SID, including inherited grants.
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return infrastructureError("state directory must use simple owner-only allow ACEs")
		}
		if !windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), owner) {
			return infrastructureError("state directory grants another principal access")
		}
	}
	return nil
}
func syncDirectory(string) error { return nil } // File.Sync flushes the journal; directory fsync is unavailable on Windows.
func listenControl() (net.Listener, string, func(), error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, "", func() {}, err
	}
	endpoint := `\\.\pipe\ccow-` + id.String()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", func() {}, err
	}
	l, err := winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	return l, endpoint, func() {}, err
}
func dialControl(endpoint string) (net.Conn, error) {
	timeout := 10 * time.Second
	return winio.DialPipe(endpoint, &timeout)
}
func sendStreams(net.Conn, Streams) error      { return nil }
func receiveStreams(net.Conn) (Streams, error) { return defaultStreams(Streams{}), nil }
func terminationSignals() (<-chan os.Signal, func()) {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	return c, func() { signal.Stop(c) }
}
func launch(s Scope, endpoint string, streams Streams) (func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return func() {}, err
	}
	cmd := exec.Command(executable, supervisorArgument, s.Directory, endpoint)
	cmd.Env = []string{}
	cmd.Stdin = streams.Stdin
	cmd.Stdout = streams.Stdout
	cmd.Stderr = streams.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err = cmd.Start(); err != nil {
		return func() {}, err
	}
	go func() { _ = cmd.Wait() }()
	return func() {}, nil
}
