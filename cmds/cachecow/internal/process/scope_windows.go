package process

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsBackend struct {
	job       windows.Handle
	root      uint32
	signalled bool
}

func newBackend(_ *Scope) (nativeBackend, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &windowsBackend{job: job}, nil
}
func (b *windowsBackend) close() { _ = windows.CloseHandle(b.job) }
func (b *windowsBackend) start(cmd *exec.Cmd) (<-chan int, error) {
	// User code cannot fork before assignment. No breakaway flag is permitted;
	// the job handle belongs only to the detached supervisor and is not inherited.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	abort := func() (<-chan int, error) { _ = cmd.Process.Kill(); _ = cmd.Wait(); return nil, ErrReconciliation }
	h, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort()
	}
	defer windows.CloseHandle(h)
	if err = windows.AssignProcessToJobObject(b.job, h); err != nil {
		return abort()
	}
	// os/exec closes the primary thread handle. Resume the suspended process
	// through ntdll only after assignment; a failed call preserves the claim.
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(h))
	if status != 0 {
		return abort()
	}
	b.root = uint32(cmd.Process.Pid)
	done := make(chan int, 1)
	go func() { _ = cmd.Wait(); done <- cmd.ProcessState.ExitCode() }()
	return done, nil
}
func (b *windowsBackend) signal(force bool) error {
	if force {
		return windows.TerminateJobObject(b.job, 137)
	}
	if !b.signalled {
		b.signalled = true
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, b.root)
	}
	return nil
}
func (b *windowsBackend) empty(rootDone bool) (bool, error) {
	if !rootDone {
		return false, nil
	}
	var info struct {
		TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
	}
	err := windows.QueryInformationJobObject(b.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return err == nil && info.ActiveProcesses == 0, err
}
