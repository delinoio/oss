package presentation

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
	"os/exec"
	"runtime"
	"syscall"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// Run only in the short-lived helper process with a clean environment. The
// parent bounds and joins it; ShellExecute can otherwise block in shell hooks.
func DispatchGitHub(raw string) error {
	if err := domain.ValidateGitHubPresentationURL(raw); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// S_FALSE (1) also succeeds and must be balanced by CoUninitialize.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && err != syscall.Errno(1) {
		return domain.Fail(domain.Unavailable, "The Windows browser launcher could not initialize.", "Open the prepared address manually.")
	}
	defer windows.CoUninitialize()
	verb, _ := windows.UTF16PtrFromString("open")
	address, err := windows.UTF16PtrFromString(raw)
	if err == nil {
		err = windows.ShellExecute(0, verb, address, nil, nil, windows.SW_SHOWNORMAL)
	}
	if err != nil {
		return domain.Fail(domain.Unavailable, "Windows did not confirm the browser launch.", "Inspect your default browser configuration.")
	}
	return nil
}
