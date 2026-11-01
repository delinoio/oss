package process

import (
	"errors"
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

// ownerCreation returns the creation time (FILETIME) of the current process.
func ownerCreation() (int64, error) {
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return int64(created.HighDateTime)<<32 | int64(created.LowDateTime), nil
}

func newBackend(s *Scope) (nativeBackend, error) {
	created, err := ownerCreation()
	if err != nil || created == 0 {
		return nil, infrastructureError("supervisor creation time is unavailable")
	}
	s.OwnerCreated = created
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
	activeJob = job
	return &windowsBackend{job: job}, nil
}
func (b *windowsBackend) close() { _ = windows.CloseHandle(b.job) }
func (b *windowsBackend) start(cmd *exec.Cmd) (<-chan int, error) {
	// User code cannot fork before assignment. No breakaway flag is permitted;
	// the job handle belongs only to the detached supervisor and is not inherited.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
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

// probeOwner is replaceable in tests. ERROR_INVALID_PARAMETER from OpenProcess
// means no process has this PID; every other failure (for example access
// denied) is an error and therefore not evidence.
var probeOwner = func(pid int) (ownerProbe, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return ownerProbe{}, nil
		}
		return ownerProbe{}, err
	}
	defer windows.CloseHandle(h)
	var created, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return ownerProbe{}, err
	}
	probe := ownerProbe{Found: true, Created: int64(created.HighDateTime)<<32 | int64(created.LowDateTime)}
	switch event, err := windows.WaitForSingleObject(h, 0); {
	case err != nil:
		return ownerProbe{}, err
	case event == windows.WAIT_OBJECT_0:
		probe.Exited = true
	case event != uint32(windows.WAIT_TIMEOUT):
		return ownerProbe{}, errors.New("unexpected process wait result")
	}
	return probe, nil
}

// The supervisor holds the only handle to its unnamed, non-inheritable job,
// and the job has JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: when the supervisor
// process ends, Windows terminates every process in the job. A supervisor
// identity (PID plus creation time) that no longer names a running process is
// therefore whole-tree proof, with or without a reboot. A bare missing PID is
// not enough because the journal must also carry the creation time. Windows has
// no documented user-mode boot identity, so none is used.
func completionEvidence(s Scope) (bool, error) { return ownerIdentityGone(s, probeOwner) }

func ownerRunning(s Scope) bool {
	p, err := probeOwner(s.OwnerPID)
	return err == nil && p.Found && !p.Exited && p.Created == s.OwnerCreated
}
