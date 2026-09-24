//go:build windows

package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"golang.org/x/sys/windows"
)

// WinBase.h defines ProcThreadAttributeJobList=13 with the input attribute bit.
// JOB_LIST assigns ownership inside CreateProcess, closing the crash window
// between suspended creation and a later AssignProcessToJobObject call.
const processAttributeJobList = 0x0002000d

type windowsScope struct {
	Version  int       `json:"version"`
	OwnerID  domain.ID `json:"owner_id"`
	Owner    Process   `json:"owner"`
	Job      string    `json:"job"`
	Started  bool      `json:"started"`
	Complete bool      `json:"complete"`
}
type managedProcess struct {
	mu                        sync.Mutex
	scope                     windowsScope
	identity                  Process
	job, process, thread      windows.Handle
	input, output, diagnostic *os.File
	done                      chan struct{}
	result, reconcileErr      error
	closeOnce                 sync.Once
}

func scopePath(dir string) string { return filepath.Join(dir, "ownership.json") }
func saveWindowsScope(dir string, scope windowsScope) error {
	raw, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	return security.WriteAtomic(scopePath(dir), raw)
}
func readWindowsScope(dir string) (windowsScope, error) {
	var scope windowsScope
	raw, err := security.ReadPrivate(scopePath(dir), 1<<20)
	if err != nil {
		return scope, err
	}
	if err := domain.Decode(raw, &scope); err != nil {
		return scope, err
	}
	if scope.Version != 1 || scope.OwnerID.Validate() != nil || !strings.HasPrefix(scope.Job, "Local\\DeliDev-") {
		return scope, ownershipError()
	}
	return scope, nil
}
func processBirth(handle windows.Handle) (string, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", created.HighDateTime, created.LowDateTime), nil
}
func ProcessIdentity(pid int) (Process, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return Process{}, err
	}
	defer windows.CloseHandle(handle)
	birth, err := processBirth(handle)
	return Process{PID: pid, Birth: birth}, err
}
func ProcessAlive(p Process) bool {
	if p.PID <= 0 || p.Birth == "" {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(p.PID))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	birth, err := processBirth(handle)
	if err != nil || birth != p.Birth {
		return false
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}
func environmentBlock(env []string) ([]uint16, error) {
	values := append([]string{}, env...)
	seen := map[string]bool{}
	for _, entry := range values {
		key, _, ok := strings.Cut(entry, "=")
		fold := strings.ToUpper(key)
		if !ok || key == "" || strings.ContainsRune(entry, 0) || seen[fold] {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid or duplicate native environment entry.", "Use an explicit unique environment allowlist.")
		}
		seen[fold] = true
	}
	sort.Slice(values, func(i, j int) bool { return strings.ToUpper(values[i]) < strings.ToUpper(values[j]) })
	return utf16.Encode([]rune(strings.Join(values, "\x00") + "\x00\x00")), nil
}
func ownedJob(name string) (windows.Handle, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return 0, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return 0, err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	job, err := windows.CreateJobObject(&attributes, ptr)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
func startProcess(command *exec.Cmd, dir string, owner domain.ID) (_ *managedProcess, returned error) {
	if err := security.PrivateDir(dir); err != nil {
		return nil, err
	}
	scope := windowsScope{Version: 1, OwnerID: owner, Job: "Local\\DeliDev-" + string(domain.NewID())}
	if err := saveWindowsScope(dir, scope); err != nil {
		return nil, err
	}
	job, err := ownedJob(scope.Job)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = windows.TerminateJobObject(job, 1)
			_ = windows.CloseHandle(job)
		}
	}()
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer inputRead.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputWrite.Close()
		return nil, err
	}
	defer outputWrite.Close()
	errorRead, errorWrite, err := os.Pipe()
	if err != nil {
		inputWrite.Close()
		outputRead.Close()
		return nil, err
	}
	defer errorWrite.Close()
	defer func() {
		if !success {
			inputWrite.Close()
			outputRead.Close()
			errorRead.Close()
		}
	}()
	inherited := make([]windows.Handle, 3)
	defer func() {
		for _, handle := range inherited {
			if handle != 0 {
				windows.CloseHandle(handle)
			}
		}
	}()
	for i, file := range []*os.File{inputRead, outputWrite, errorWrite} {
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &inherited[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return nil, err
	}
	if err := attributes.Update(processAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, domain.Fail(domain.Unsupported, "Atomic native process ownership is unavailable on this Windows version.", "Use a supported Windows 10 or newer execution machine.")
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = inherited[0]
	startup.StdOutput = inherited[1]
	startup.StdErr = inherited[2]
	startup.ProcThreadAttributeList = attributes.List()
	executable, err := windows.UTF16PtrFromString(command.Path)
	if err != nil {
		return nil, err
	}
	arguments, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		return nil, err
	}
	cwd, err := windows.UTF16PtrFromString(command.Dir)
	if err != nil {
		return nil, err
	}
	env, err := environmentBlock(command.Env)
	if err != nil {
		return nil, err
	}
	var info windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW)
	if err := windows.CreateProcess(executable, arguments, nil, nil, true, flags, &env[0], cwd, &startup.StartupInfo, &info); err != nil {
		code := domain.Unavailable
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			code = domain.NotFound
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			code = domain.PermissionDenied
		case errors.Is(err, windows.ERROR_BAD_EXE_FORMAT):
			code = domain.Unsupported
		}
		return nil, launchFailure(code)
	}
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(inherited)
	runtime.KeepAlive(job)
	defer func() {
		if !success {
			_ = windows.TerminateJobObject(job, 1)
			_, _ = windows.WaitForSingleObject(info.Process, 5000)
			windows.CloseHandle(info.Thread)
			windows.CloseHandle(info.Process)
		}
	}()
	birth, err := processBirth(info.Process)
	if err != nil {
		return nil, err
	}
	identity := Process{PID: int(info.ProcessId), Birth: birth, OwnerID: owner, ScopeDir: dir}
	scope.Owner = identity
	if err := saveWindowsScope(dir, scope); err != nil {
		return nil, err
	}
	p := &managedProcess{scope: scope, identity: identity, job: job, process: info.Process, thread: info.Thread, input: inputWrite, output: outputRead, diagnostic: errorRead, done: make(chan struct{})}
	success = true
	// Close the parent's child-side handles before readers wait for EOF.
	inputRead.Close()
	outputWrite.Close()
	errorWrite.Close()
	for i, handle := range inherited {
		windows.CloseHandle(handle)
		inherited[i] = 0
	}
	go p.observe(command.Stdout, command.Stderr)
	return p, nil
}
func drainJob(job windows.Handle) error {
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		var info struct {
			TotalUser, TotalKernel, PeriodUser, PeriodKernel int64
			PageFaults, Total, Active, Terminated            uint32
		}
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			return err
		}
		if info.Active == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return ownershipError()
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (p *managedProcess) observe(stdout, stderr io.Writer) {
	var copied sync.WaitGroup
	copied.Add(2)
	outputErrors := make(chan error, 2)
	pump := func(reader *os.File, writer io.Writer) {
		defer copied.Done()
		if writer == nil {
			writer = io.Discard
		}
		_, err := io.Copy(writer, reader)
		outputErrors <- err
		if err != nil {
			_ = windows.TerminateJobObject(p.job, 1)
		}
	}
	go pump(p.output, stdout)
	go pump(p.diagnostic, stderr)
	_, waitErr := windows.WaitForSingleObject(p.process, windows.INFINITE)
	var code uint32
	if waitErr == nil {
		waitErr = windows.GetExitCodeProcess(p.process, &code)
	}
	p.mu.Lock()
	reconcile := drainJob(p.job)
	_ = p.input.Close()
	p.mu.Unlock()
	copied.Wait()
	outputFailed := false
	for range 2 {
		if <-outputErrors != nil {
			outputFailed = true
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if reconcile == nil {
		p.scope.Complete = true
		reconcile = saveWindowsScope(p.identity.ScopeDir, p.scope)
	}
	p.reconcileErr = reconcile
	if reconcile != nil {
		p.result = ownershipError()
	} else if waitErr != nil {
		p.result = waitErr
	} else if outputFailed {
		p.result = domain.Fail(domain.Unavailable, "Native output could not be delivered.", "Inspect this execution's accepted state before retrying.")
	} else if code != 0 {
		p.result = commandExitError(code)
	}
	close(p.done)
}
func (p *managedProcess) resume() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return ownershipError()
	default:
	}
	p.scope.Started = true
	if err := saveWindowsScope(p.identity.ScopeDir, p.scope); err != nil {
		return err
	}
	_, err := windows.ResumeThread(p.thread)
	return err
}
func (p *managedProcess) write(raw []byte) (int, error) { return p.input.Write(raw) }
func (p *managedProcess) closeInput() error {
	err := p.input.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
func (p *managedProcess) wait() error       { <-p.done; return p.result }
func (p *managedProcess) snapshot() Process { return p.identity }
func (p *managedProcess) terminate() error {
	select {
	case <-p.done:
		return p.reconcileErr
	default:
	}
	p.mu.Lock()
	err := drainJob(p.job)
	_ = p.input.Close()
	p.mu.Unlock()
	if err != nil {
		return ownershipError()
	}
	select {
	case <-p.done:
		return p.reconcileErr
	case <-time.After(10 * time.Second):
		return ownershipError()
	}
}
func (p *managedProcess) close() {
	p.closeOnce.Do(func() {
		_ = p.terminate()
		p.input.Close()
		p.output.Close()
		p.diagnostic.Close()
		windows.CloseHandle(p.thread)
		windows.CloseHandle(p.process)
		windows.CloseHandle(p.job)
	})
}
func ReconcileProcess(identity Process) error {
	scope, err := readWindowsScope(identity.ScopeDir)
	if err != nil || scope.OwnerID != identity.OwnerID || identity.OwnerID.Validate() != nil {
		return ownershipError()
	}
	if scope.Owner.PID != identity.PID || scope.Owner.Birth != identity.Birth {
		return ownershipError()
	}
	if scope.Complete {
		return nil
	}
	name, err := windows.UTF16PtrFromString(scope.Job)
	if err != nil {
		return ownershipError()
	}
	open := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")
	handle, _, failure := open.Call(uintptr(0x0004|0x0008), 0, uintptr(unsafe.Pointer(name)))
	runtime.KeepAlive(name)
	if handle == 0 {
		if !errors.Is(failure, windows.ERROR_FILE_NOT_FOUND) {
			return ownershipError()
		}
		// This unique named kill-on-close job cannot disappear while any owned
		// process survives. No PID lookup is used as a substitute for that proof.
	} else {
		defer windows.CloseHandle(windows.Handle(handle))
		if err := drainJob(windows.Handle(handle)); err != nil {
			return ownershipError()
		}
	}
	scope.Complete = true
	return saveWindowsScope(identity.ScopeDir, scope)
}
