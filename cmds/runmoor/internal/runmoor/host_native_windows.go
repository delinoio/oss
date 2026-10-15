package runmoor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hostPlatform(ctx context.Context) error          { return windowsPlatform(ctx) }
func (nativeHost) Platform(ctx context.Context) error { return hostPlatform(ctx) }
func (nativeHost) Alive(p HostProcess) (bool, error) {
	if p.PID <= 0 || p.Start == "" {
		return false, hostOwnership()
	}
	q, e := windowsProcess(p.PID)
	if os.IsNotExist(e) {
		return false, nil
	}
	return e == nil && sameHostProcess(p, q), e
}
func hostJobName(p HostProcess) *uint16 {
	n, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\Runmoor-%x`, sha256.Sum256([]byte(fmt.Sprint(p.PID)+p.Start))))
	return n
}
func openHostJob(p HostProcess) (windows.Handle, error) {
	r, _, e := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW").Call(0x0004|0x0008, 0, uintptr(unsafe.Pointer(hostJobName(p))))
	if r == 0 {
		return 0, e
	}
	return windows.Handle(r), nil
}
func (nativeHost) Group(p HostProcess) ([]HostProcess, error) {
	if p.PID <= 0 || p.Start == "" || p.Group != p.PID {
		return nil, hostOwnership()
	}
	job, e := openHostJob(p)
	if e != nil {
		alive, err := (nativeHost{}).Alive(p)
		if err == nil && !alive && e == windows.ERROR_FILE_NOT_FOUND {
			return nil, nil
		}
		return nil, hostPending()
	}
	defer windows.CloseHandle(job)
	var accounting struct {
		TotalUser, TotalKernel, PeriodUser, PeriodKernel int64
		Faults, Total, Active, Terminated                uint32
	}
	e = windows.QueryInformationJobObject(job, 1, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
	if e != nil {
		return nil, e
	}
	if accounting.Active == 0 {
		return nil, nil
	}
	return []HostProcess{p}, nil
}
func (nativeHost) Stop(p HostProcess) error {
	alive, e := (nativeHost{}).Alive(p)
	if e != nil || !alive {
		return e
	}
	h, e := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	var c, x, k, u windows.Filetime
	if e = windows.GetProcessTimes(h, &c, &x, &k, &u); e != nil {
		return e
	}
	if p.Start != fmt.Sprintf("windows:%d:%d", c.HighDateTime, c.LowDateTime) {
		return hostOwnership()
	}
	// Closing the supervisor's sole job handle terminates the entire worker job.
	return windows.TerminateProcess(h, 1)
}
func windowsBootstrap(root *os.Root, in HostBootstrap) (*os.Root, error) {
	p := hostDirectoryPath(Config{Storage: in.Storage}, in.Directory)
	if e := rejectReparsePath(p); e != nil {
		return nil, e
	}
	selected, e := openHostDirectory(Config{Storage: in.Storage}, in.Directory, in.Directory.Installation, false)
	if e != nil {
		return nil, e
	}
	if root != nil {
		a, ae := root.Stat(".")
		b, be := selected.Stat(".")
		if ae != nil || be != nil || !os.SameFile(a, b) {
			selected.Close()
			return nil, hostOwnership()
		}
	}
	return selected, nil
}
func windowsHostCommand(executable, entry string, root *os.Root, in HostBootstrap) (*exec.Cmd, io.WriteCloser, error) {
	if executable == "" {
		var e error
		executable, e = os.Executable()
		if e != nil {
			return nil, nil, e
		}
	}
	cmd := exec.Command(executable, entry)
	cmd.Dir = root.Name()
	cmd.Env = hostEnvironment(root.Name())
	detach(cmd)
	pipe, e := cmd.StdinPipe()
	return cmd, pipe, e
}
func (h nativeHost) Version(ctx context.Context, root *os.Root, version string) error {
	// Execute only the verified distribution, with fixed argv and a bounded
	// output sink. Runner diagnostics and upstream stderr never enter logs.
	if e := rejectReparsePath(filepath.Join(root.Name(), "runner", "bin", "Runner.Listener.exe")); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, filepath.Join(root.Name(), "runner", "bin", "Runner.Listener.exe"), "--version")
	cmd.Dir = filepath.Join(root.Name(), "runner")
	cmd.Env = hostEnvironment(root.Name())
	detach(cmd)
	out := &boundedBuffer{Limit: 4096}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		return problem(ErrPreparation, "Cannot inspect the Windows runner version.", "Check the official Windows x64 runner and its runtime dependencies.")
	}
	for _, line := range strings.Split(string(out.Bytes()), "\n") {
		if strings.TrimSpace(line) == version {
			return nil
		}
	}
	return problem(ErrRunnerVersion, "Windows runner version does not match.", "Request a verified runner update.")
}
func (h nativeHost) Launch(ctx context.Context, root *os.Root, in HostBootstrap, publish func(HostProcess) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	cmd, pipe, e := windowsHostCommand(h.executable, "__host-supervisor", root, in)
	if e != nil {
		return e
	}
	defer pipe.Close()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	p, e := windowsProcess(cmd.Process.Pid)
	if e != nil {
		return hostPending()
	}
	if e = publish(p); e != nil {
		_ = (nativeHost{}).Stop(p)
		return e
	}
	if e = ctx.Err(); e != nil {
		_ = (nativeHost{}).Stop(p)
		return e
	}
	if e = json.NewEncoder(pipe).Encode(in); e != nil {
		_ = (nativeHost{}).Stop(p)
		return hostPending()
	}
	return nil
}

type nativeHostWorker struct{ job windows.Handle }

func (w *nativeHostWorker) Start(ctx context.Context, root *os.Root, in HostBootstrap) (HostProcess, <-chan int, error) {
	if e := hostWorkerAdmission(ctx, in); e != nil {
		return HostProcess{}, nil, e
	}
	cmd, pipe, e := windowsHostCommand("", "__host-exec", root, in)
	if e != nil {
		return HostProcess{}, nil, e
	}
	cmd.Args = append(cmd.Args, "worker")
	defer pipe.Close()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return HostProcess{}, nil, e
	}
	p, e := windowsProcess(cmd.Process.Pid)
	if e != nil {
		pipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return HostProcess{}, nil, hostPending()
	}
	sd, e := privateSecurity()
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return p, nil, e
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	job, e := windows.CreateJobObject(sa, hostJobName(p))
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return p, nil, e
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	var process windows.Handle
	if e == nil {
		process, e = windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.PID))
	}
	if e == nil {
		e = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if e != nil {
		windows.CloseHandle(job)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return p, nil, e
	}
	w.job = job
	// The helper blocks on stdin until assignment. Runner code and descendants
	// cannot start before the kernel has installed the job ownership boundary.
	if e = json.NewEncoder(pipe).Encode(in); e != nil {
		windows.TerminateJobObject(job, 1)
	}
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
	return p, done, nil
}
func (*nativeHostWorker) Members(p HostProcess) ([]HostProcess, error) {
	return (nativeHost{}).Group(p)
}
func (w *nativeHostWorker) Terminate(_ HostProcess, _ bool) error {
	if w.job == 0 {
		return hostPending()
	}
	return windows.TerminateJobObject(w.job, 1)
}
func (*nativeHostWorker) Deadline(in HostBootstrap, previous time.Time) time.Time {
	s, e := ReadSnapshot(Config{Storage: in.Storage})
	if e != nil {
		return previous
	}
	return hostJobDeadline(in, previous, s)
}
func readWindowsBootstrap() (HostBootstrap, error) {
	var in HostBootstrap
	b, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+8193))
	if e != nil || len(b) > (1<<20)+8192 || json.Unmarshal(b, &in) != nil || !validHostDirectory(in.Directory, in.Directory.Installation) || !validHostJIT(in.JIT) || in.Deadline.IsZero() || time.Until(in.Deadline) > 7*24*time.Hour {
		return in, hostOwnership()
	}
	return in, nil
}
func hostSupervise() int {
	in, e := readWindowsBootstrap()
	if e != nil {
		return 1
	}
	root, e := windowsBootstrap(nil, in)
	if e != nil {
		return 1
	}
	defer root.Close()
	p, e := windowsProcess(os.Getpid())
	if e != nil {
		return 1
	}
	status := HostExecutionStatus{ID: in.Directory.ID, Token: in.Directory.Token, Supervisor: p, Phase: HostStarting}
	if e = hostRootWrite(root, "status.json", status); e != nil {
		return 1
	}
	worker := &nativeHostWorker{}
	defer func() {
		if worker.job != 0 {
			windows.CloseHandle(worker.job)
		}
	}()
	return superviseHost(context.Background(), root, in, status, worker)
}
func hostExecute(mode string) int {
	if mode != "worker" {
		return 1
	}
	in, e := readWindowsBootstrap()
	if e != nil || in.Directory.Kind != HostWorkspace {
		return 1
	}
	root, e := windowsBootstrap(nil, in)
	if e != nil {
		return 1
	}
	defer root.Close()
	if hostWorkerAdmission(context.Background(), in) != nil {
		return 1
	}
	entry := filepath.Join(root.Name(), "runner", "bin", "Runner.Listener.exe")
	if rejectReparsePath(entry) != nil {
		return 1
	}
	cmd := exec.Command(entry, "run", "--jitconfig", in.JIT)
	cmd.Dir = filepath.Join(root.Name(), "runner")
	cmd.Env = hostEnvironment(root.Name())
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode()
		}
		return 1
	}
	return 0
}
