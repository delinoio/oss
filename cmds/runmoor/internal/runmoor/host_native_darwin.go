//go:build darwin

package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func hostPlatform(ctx context.Context) error {
	if runtime.GOARCH != "arm64" {
		return problem(ErrPlatform, "Host execution requires Apple Silicon.", "Use macOS 14+ arm64.")
	}
	return platformCheck(ctx)
}
func (nativeHost) Platform(ctx context.Context) error { return hostPlatform(ctx) }
func hostPrivateInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
func hostFileIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
func hostRenameNoReplace(root *os.Root, from, to string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return unix.RenameatxNp(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_EXCL)
}
func (h nativeHost) Version(ctx context.Context, root *os.Root, version string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d, err := hostDirectoryMarker(root)
	if err != nil {
		return hostOwnership()
	}
	cmd, outcome, closeFiles, err := hostRunnerCommand(ctx, h.executable, hostExecVersion, root, HostBootstrap{Directory: d})
	if err != nil {
		return hostExecProblem(hostExecSpawn)
	}
	defer closeFiles()
	cmd.Env = hostEnvironment(root.Name())
	cmd.Stderr = io.Discard
	out := &boundedBuffer{Limit: 4096}
	cmd.Stdout = out
	startErr := cmd.Start()
	closeHostChildFiles(cmd)
	err = startErr
	if startErr == nil {
		err = cmd.Wait()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reason := hostReadExecOutcome(outcome)
	if err != nil || reason != hostExecOK {
		if startErr != nil {
			reason = hostExecSpawn
		} else if reason == hostExecOK {
			reason = hostExecRun
		}
		if h.log != nil {
			h.log.Warn("host_runner_execution_failed", "stage", hostExecVersion, "reason", reason.String())
		}
		return hostExecProblem(reason)
	}
	for _, line := range strings.Split(string(out.Bytes()), "\n") {
		if strings.TrimSpace(line) == version {
			return nil
		}
	}
	if h.log != nil {
		h.log.Warn("host_runner_version_mismatch", "stage", hostExecVersion)
	}
	return problem(ErrRunnerVersion, "Host runner reported an unexpected distribution version.", "Retry the managed runner update with the selected official macOS arm64 release.")
}
func nativeHostProcess(pid int) (HostProcess, error) {
	// Darwin returns an empty successful sysctl result for an exited PID.
	// SysctlKinfoProc turns that into EIO; the slice API preserves absence without
	// conflating a real inspection failure with confirmed process termination.
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || err == nil && len(entries) == 0 {
		return HostProcess{}, os.ErrNotExist
	}
	if err != nil {
		return HostProcess{}, err
	}
	if len(entries) != 1 || int(entries[0].Proc.P_pid) != pid {
		return HostProcess{}, hostOwnership()
	}
	info := entries[0]
	if info.Eproc.Ucred.Uid != uint32(os.Geteuid()) {
		return HostProcess{}, hostOwnership()
	}
	started := info.Proc.P_starttime
	if started.Sec < 0 || started.Usec < 0 || started.Usec >= 1_000_000 || started.Sec == 0 && started.Usec == 0 {
		return HostProcess{}, hostOwnership()
	}
	start := fmt.Sprintf("darwin:%d:%d", started.Sec, started.Usec)
	return HostProcess{PID: pid, Start: start, Group: int(info.Eproc.Pgid)}, nil
}
func (nativeHost) Alive(p HostProcess) (bool, error) {
	if p.PID <= 0 || p.Start == "" {
		return false, hostOwnership()
	}
	current, err := nativeHostProcess(p.PID)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return sameHostProcess(p, current), nil
}
func (nativeHost) Group(p HostProcess) ([]HostProcess, error) {
	if p.PID <= 0 || p.Group != p.PID || p.Start == "" {
		return nil, hostOwnership()
	}
	current, err := nativeHostProcess(p.PID)
	if err == nil && !sameHostProcess(p, current) {
		return nil, hostOwnership()
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var members []HostProcess
	for _, info := range all {
		if int(info.Eproc.Pgid) != p.Group {
			continue
		}
		candidate, err := nativeHostProcess(int(info.Proc.P_pid))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if candidate.Group != p.Group {
			return nil, hostOwnership()
		}
		members = append(members, candidate)
	}
	return members, nil
}
func signalHostProcess(p HostProcess, force bool) error {
	alive, err := (nativeHost{}).Alive(p)
	if err != nil || !alive {
		return err
	}
	sig := unix.SIGTERM
	if force {
		sig = unix.SIGKILL
	}
	err = unix.Kill(p.PID, sig)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
func (nativeHost) Stop(p HostProcess) error { return signalHostProcess(p, false) }
func (h nativeHost) Launch(ctx context.Context, root *os.Root, in HostBootstrap, publish func(HostProcess) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	exe, err := hostExecutable(h.executable)
	if err != nil {
		return hostPending()
	}
	dir, err := root.Open(".")
	if err != nil {
		return hostOwnership()
	}
	defer dir.Close()
	cmd := exec.Command(exe, "__host-supervisor")
	cmd.ExtraFiles = []*os.File{dir}
	cmd.Env = hostEnvironment(hostDirectoryPath(Config{Storage: in.Storage}, in.Directory))
	detach(cmd)
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return hostPending()
	}
	defer null.Close()
	cmd.Stdout = null
	cmd.Stderr = null
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return hostPending()
	}
	defer pipe.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		return problem(ErrPreparation, "Cannot start the host supervisor.", "Check the Runmoor executable and process limits, then resume the pool.")
	}
	process, err := nativeHostProcess(cmd.Process.Pid)
	go func() { _ = cmd.Wait() }()
	if err != nil {
		return hostPending()
	}
	if err = publish(process); err != nil {
		_ = signalHostProcess(process, false)
		return err
	}
	if ctx.Err() != nil {
		_ = signalHostProcess(process, false)
		return ctx.Err()
	}
	// This owner-only pipe is the sole bootstrap transport. JIT data never enters
	// the manager database, filesystem, diagnostics, or supervisor arguments.
	if err = json.NewEncoder(pipe).Encode(in); err != nil {
		_ = signalHostProcess(process, false)
		return hostPending()
	}
	return nil
}

type nativeHostWorker struct {
	executable string
}

func (h nativeHostWorker) Start(ctx context.Context, root *os.Root, in HostBootstrap) (HostProcess, <-chan int, error) {
	// Read-only inspection does not migrate state. Reject a durable stop or later
	// lifecycle before spawning, including a force-stop during private bootstrap.
	if err := hostWorkerAdmission(ctx, in); err != nil {
		return HostProcess{}, nil, err
	}
	// The detached supervisor owns cancellation and cleanup after Start succeeds.
	cmd, outcome, closeFiles, err := hostRunnerCommand(context.Background(), h.executable, hostExecWorker, root, in)
	if err != nil {
		return HostProcess{}, nil, hostExecProblem(hostExecSpawn)
	}
	cmd.Env = hostEnvironment(hostDirectoryPath(Config{Storage: in.Storage}, in.Directory))
	detach(cmd)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		closeFiles()
		return HostProcess{}, nil, err
	}
	defer null.Close()
	cmd.Stdout = null
	cmd.Stderr = null
	if ctx.Err() != nil {
		closeFiles()
		return HostProcess{}, nil, ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		closeFiles()
		return HostProcess{}, nil, hostExecProblem(hostExecSpawn)
	}
	closeHostChildFiles(cmd)
	process, identityErr := nativeHostProcess(cmd.Process.Pid)
	done := make(chan int, 1)
	go func() {
		defer closeFiles()
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = 1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
		}
		if reason := hostReadExecOutcome(outcome); reason != hostExecOK {
			// Private status retains a bounded failure stage, never raw stderr.
			code = 100 + int(reason)
		}
		done <- code
	}()
	if identityErr != nil {
		// Start succeeded but its identity is unavailable. Do not guess a PID owner;
		// retain an unresolved supervisor rather than abandon a potentially live job.
		return HostProcess{PID: cmd.Process.Pid, Group: cmd.Process.Pid}, done, nil
	}
	return process, done, nil
}
func (nativeHostWorker) Members(p HostProcess) ([]HostProcess, error) { return (nativeHost{}).Group(p) }
func (nativeHostWorker) Terminate(p HostProcess, force bool) error {
	return signalHostProcess(p, force)
}
func (nativeHostWorker) Deadline(in HostBootstrap, previous time.Time) time.Time {
	s, err := ReadSnapshot(Config{Storage: in.Storage})
	if err != nil {
		return previous
	}
	return hostJobDeadline(in, previous, s)
}

func hostSupervise() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	root, d, err := hostInheritedRoot()
	if err != nil {
		return 1
	}
	defer root.Close()
	if d.Kind != HostWorkspace {
		return 1
	}
	process, err := nativeHostProcess(os.Getpid())
	if err != nil {
		return 1
	}
	status := HostExecutionStatus{ID: d.ID, Token: d.Token, Supervisor: process, Phase: HostStarting}
	if hostRootWrite(root, "status.json", status) != nil {
		return 1
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+8193))
	var in HostBootstrap
	if err != nil || len(data) > (1<<20)+8192 || json.Unmarshal(data, &in) != nil || in.Directory != d || !validHostJIT(in.JIT) || in.Deadline.IsZero() || time.Until(in.Deadline) > 7*24*time.Hour || ctx.Err() != nil {
		status.Phase = HostFailed
		status.ExitCode = 1
		_ = hostRootWrite(root, "status.json", status)
		return 1
	}
	return superviseHost(ctx, root, in, status, nativeHostWorker{})
}
