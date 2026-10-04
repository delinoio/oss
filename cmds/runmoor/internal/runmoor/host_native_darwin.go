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

type nativeHost struct{}

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
func (nativeHost) Version(ctx context.Context, root *os.Root, version string) error {
	dir, err := root.Open(".")
	if err != nil {
		return hostOwnership()
	}
	defer dir.Close()
	cmd := exec.CommandContext(ctx, "/dev/fd/3/runner/bin/Runner.Listener", "--version")
	cmd.ExtraFiles = []*os.File{dir}
	// Go changes directory before remapping ExtraFiles, so use the parent
	// descriptor for cwd and child fd 3 only for the executable path.
	cmd.Dir = fmt.Sprintf("/dev/fd/%d/runner", dir.Fd())
	cmd.Env = hostEnvironment(root.Name())
	cmd.Stderr = io.Discard
	out := &boundedBuffer{Limit: 4096}
	cmd.Stdout = out
	if err := cmd.Run(); err == nil {
		for _, line := range strings.Split(string(out.Bytes()), "\n") {
			if strings.TrimSpace(line) == version {
				return nil
			}
		}
	}
	return problem(ErrRunnerVersion, "Host runner distribution version validation failed.", "Verify the official macOS arm64 runner and installed runtime dependencies, then retry the update.")
}
func nativeHostProcess(pid int) (HostProcess, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || err == nil && int(info.Proc.P_pid) != pid {
		return HostProcess{}, os.ErrNotExist
	}
	if err != nil {
		return HostProcess{}, err
	}
	if info.Eproc.Ucred.Uid != uint32(os.Geteuid()) {
		return HostProcess{}, hostOwnership()
	}
	start, err := tartRunProcessStartIdentity(pid)
	return HostProcess{PID: pid, Start: start, Group: int(info.Eproc.Pgid)}, err
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
func (nativeHost) Launch(ctx context.Context, root *os.Root, in HostBootstrap, publish func(HostProcess) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	exe, err := os.Executable()
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

type nativeHostWorker struct{}

func (nativeHostWorker) Start(ctx context.Context, root *os.Root, in HostBootstrap) (HostProcess, <-chan int, error) {
	// Read-only inspection does not migrate state. Reject a durable stop or later
	// lifecycle before spawning, including a force-stop during private bootstrap.
	s, err := ReadSnapshot(Config{Storage: in.Storage})
	if err != nil {
		return HostProcess{}, nil, err
	}
	r := s.Runners[in.Directory.ID]
	if r == nil || r.Forced || r.Phase != Preparing || s.HostExecutions[r.ID] == nil || s.Installation != in.Directory.Installation || s.HostDirectories[r.ID] == nil || *s.HostDirectories[r.ID] != in.Directory {
		return HostProcess{}, nil, staleUpdate()
	}
	dir, err := root.Open(".")
	if err != nil {
		return HostProcess{}, nil, err
	}
	defer dir.Close()
	cmd := exec.Command("/dev/fd/3/runner/run.sh", "--jitconfig", in.JIT)
	cmd.ExtraFiles = []*os.File{dir}
	// Go changes directory before remapping ExtraFiles, so use the parent
	// descriptor for cwd and child fd 3 only for the executable path.
	cmd.Dir = fmt.Sprintf("/dev/fd/%d/runner", dir.Fd())
	cmd.Env = hostEnvironment(hostDirectoryPath(Config{Storage: in.Storage}, in.Directory))
	detach(cmd)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return HostProcess{}, nil, err
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if ctx.Err() != nil {
		return HostProcess{}, nil, ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		return HostProcess{}, nil, err
	}
	process, identityErr := nativeHostProcess(cmd.Process.Pid)
	done := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = 1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
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
	r := s.Runners[in.Directory.ID]
	d := s.HostDirectories[in.Directory.ID]
	if r == nil || d == nil || *d != in.Directory || r.Phase != Busy || r.Deadline.IsZero() {
		return previous
	}
	return r.Deadline
}

func hostSupervise() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	root, err := os.OpenRoot("/dev/fd/3")
	if err != nil {
		return 1
	}
	defer root.Close()
	var d HostDirectory
	b, err := hostRootRead(root, hostOwnerFile, 4096)
	if err != nil || json.Unmarshal(b, &d) != nil || !validHostDirectory(d, d.Installation) || d.Kind != HostWorkspace {
		return 1
	}
	info, err := root.Stat(".")
	if err != nil || hostFileIdentity(info) != d.Identity {
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
