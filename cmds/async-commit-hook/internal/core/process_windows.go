//go:build windows

package core

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"unsafe"
)

type managedProcess struct {
	cmd      *exec.Cmd
	identity Process
	job      windows.Handle
}

func ProcessIdentity(pid int) (Process, error) {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return Process{}, e
	}
	defer windows.CloseHandle(h)
	var create, exit, kernel, user windows.Filetime
	if e = windows.GetProcessTimes(h, &create, &exit, &kernel, &user); e != nil {
		return Process{}, e
	}
	return Process{PID: pid, Group: pid, Birth: fmt.Sprintf("%d:%d", create.HighDateTime, create.LowDateTime)}, nil
}
func ProcessAlive(p Process) bool {
	current, e := ProcessIdentity(p.PID)
	if e != nil || current.Birth != p.Birth {
		return false
	}
	h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(p.PID))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	v, e := windows.WaitForSingleObject(h, 0)
	return e == nil && v == windows.WAIT_TIMEOUT
}
func startProcess(c *exec.Cmd) (*managedProcess, error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	// Suspension closes the fork-before-job-assignment race; breakaway is not permitted.
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if e = c.Start(); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	h, e := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(c.Process.Pid))
	if e != nil {
		_ = c.Process.Kill()
		windows.CloseHandle(job)
		return nil, e
	}
	defer windows.CloseHandle(h)
	if e = windows.AssignProcessToJobObject(job, h); e != nil {
		_ = c.Process.Kill()
		windows.CloseHandle(job)
		return nil, e
	}
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(h))
	if status != 0 {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(job)
		return nil, E("process-resume-failed", "cannot resume owned check", 3)
	}
	p, e := ProcessIdentity(c.Process.Pid)
	if e != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(job)
		return nil, e
	}
	return &managedProcess{c, p, job}, nil
}
func (p *managedProcess) sample() error { return nil }
func (p *managedProcess) terminate() error {
	if e := windows.TerminateJobObject(p.job, 1); e != nil {
		return e
	}
	var info windows.JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
	for i := 0; i < 200; i++ {
		e := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if e != nil {
			return e
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		windows.SleepEx(10, false)
	}
	return E("process-reconciliation-failed", "job descendants are still active", 3)
}
func (p *managedProcess) close() { _ = windows.CloseHandle(p.job) }
func ReconcileProcess(p Process) error {
	if !ProcessAlive(p) {
		return nil
	}
	return E("process-reconciliation-failed", "previous worker still owns this Windows job; stop it before recovery", 3)
}
func Detached(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
}
