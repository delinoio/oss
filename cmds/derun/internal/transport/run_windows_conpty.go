//go:build windows

package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	defaultConPTYWidth  = 120
	defaultConPTYHeight = 30
)

func RunWindowsConPTY(
	ctx context.Context,
	command []string,
	workingDir string,
	onStart func(pid int) error,
	ptyOutput io.Writer,
) (RunResult, error) {
	if len(command) == 0 {
		return RunResult{}, fmt.Errorf("command is empty")
	}

	conPTYSize := detectConPTYSize()

	inRead, inWrite, err := createInheritedPipePair()
	if err != nil {
		return RunResult{}, fmt.Errorf("create conpty input pipe: %w", err)
	}
	defer closeHandle(&inRead)
	defer closeHandle(&inWrite)

	outRead, outWrite, err := createInheritedPipePair()
	if err != nil {
		return RunResult{}, fmt.Errorf("create conpty output pipe: %w", err)
	}
	defer closeHandle(&outRead)
	defer closeHandle(&outWrite)

	if err := windows.SetHandleInformation(inWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return RunResult{}, fmt.Errorf("mark conpty input writer non-inheritable: %w", err)
	}
	if err := windows.SetHandleInformation(outRead, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return RunResult{}, fmt.Errorf("mark conpty output reader non-inheritable: %w", err)
	}

	var pseudoConsole windows.Handle
	if err := windows.CreatePseudoConsole(conPTYSize, inRead, outWrite, 0, &pseudoConsole); err != nil {
		return RunResult{}, fmt.Errorf("create pseudo console: %w", err)
	}
	defer windows.ClosePseudoConsole(pseudoConsole)

	attributeList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return RunResult{}, fmt.Errorf("create proc thread attribute list: %w", err)
	}
	defer attributeList.Delete()

	if err := attributeList.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(&pseudoConsole),
		unsafe.Sizeof(pseudoConsole),
	); err != nil {
		return RunResult{}, fmt.Errorf("set pseudoconsole attribute: %w", err)
	}

	startupInfo := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
		},
		ProcThreadAttributeList: attributeList.List(),
	}

	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command))
	if err != nil {
		return RunResult{}, fmt.Errorf("compose command line: %w", err)
	}

	var workingDirUTF16 *uint16
	if workingDir != "" {
		workingDirUTF16, err = windows.UTF16PtrFromString(workingDir)
		if err != nil {
			return RunResult{}, fmt.Errorf("encode working directory: %w", err)
		}
	}

	processInfo := windows.ProcessInformation{}
	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if err := windows.CreateProcess(
		nil,
		commandLine,
		nil,
		nil,
		true,
		creationFlags,
		nil,
		workingDirUTF16,
		&startupInfo.StartupInfo,
		&processInfo,
	); err != nil {
		return RunResult{}, fmt.Errorf("start conpty process: %w", err)
	}
	defer closeHandle(&processInfo.Thread)
	defer closeHandle(&processInfo.Process)

	if onStart != nil {
		if err := onStart(int(processInfo.ProcessId)); err != nil {
			_ = windows.TerminateProcess(processInfo.Process, 1)
			_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
			return RunResult{}, err
		}
	}

	// Child process is started, so parent can release these inherited-side pipe handles.
	closeHandle(&inRead)
	closeHandle(&outWrite)

	stdinWriter := os.NewFile(uintptr(inWrite), "conpty-stdin-write")
	if stdinWriter == nil {
		return RunResult{}, fmt.Errorf("wrap conpty input writer")
	}
	inWrite = 0
	defer stdinWriter.Close()

	stdoutReader := os.NewFile(uintptr(outRead), "conpty-stdout-read")
	if stdoutReader == nil {
		return RunResult{}, fmt.Errorf("wrap conpty output reader")
	}
	outRead = 0
	defer stdoutReader.Close()

	signals := make(chan os.Signal, 8)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	defer close(signals)

	go func() {
		for sig := range signals {
			switch sig {
			case os.Interrupt:
				_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, processInfo.ProcessId)
			default:
				_ = windows.TerminateProcess(processInfo.Process, 1)
			}
		}
	}()

	copyStdoutErr := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(ptyOutput, stdoutReader)
		if isBenignConPTYCopyErr(copyErr) {
			copyErr = nil
		}
		copyStdoutErr <- copyErr
	}()

	go func() {
		_, copyErr := io.Copy(stdinWriter, os.Stdin)
		if copyErr != nil && !isBenignConPTYCopyErr(copyErr) {
			_ = windows.TerminateProcess(processInfo.Process, 1)
		}
		_ = stdinWriter.Close()
	}()

	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = windows.TerminateProcess(processInfo.Process, 1)
		case <-waitDone:
		}
	}()

	if _, err := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE); err != nil {
		close(waitDone)
		return RunResult{}, fmt.Errorf("wait for conpty process: %w", err)
	}
	close(waitDone)

	if err := <-copyStdoutErr; err != nil {
		return RunResult{}, fmt.Errorf("copy conpty output: %w", err)
	}

	var exitCode uint32
	if err := windows.GetExitCodeProcess(processInfo.Process, &exitCode); err != nil {
		return RunResult{}, fmt.Errorf("get conpty exit code: %w", err)
	}
	resultCode := int(exitCode)
	return RunResult{ExitCode: &resultCode}, nil
}

func createInheritedPipePair() (windows.Handle, windows.Handle, error) {
	securityAttributes := windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
	}
	securityAttributes.InheritHandle = 1

	var readHandle windows.Handle
	var writeHandle windows.Handle
	if err := windows.CreatePipe(&readHandle, &writeHandle, &securityAttributes, 0); err != nil {
		return 0, 0, err
	}
	return readHandle, writeHandle, nil
}

func closeHandle(handle *windows.Handle) {
	if handle == nil || *handle == 0 {
		return
	}
	_ = windows.CloseHandle(*handle)
	*handle = 0
}

func detectConPTYSize() windows.Coord {
	outputHandle := windows.Handle(os.Stdout.Fd())
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(outputHandle, &info); err != nil {
		return windows.Coord{X: defaultConPTYWidth, Y: defaultConPTYHeight}
	}
	width := info.Window.Right - info.Window.Left + 1
	height := info.Window.Bottom - info.Window.Top + 1
	if width <= 0 {
		width = defaultConPTYWidth
	}
	if height <= 0 {
		height = defaultConPTYHeight
	}
	return windows.Coord{X: width, Y: height}
}

func isBenignConPTYCopyErr(err error) bool {
	if err == nil {
		return false
	}
	if isBenignCopyErr(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "pipe is being closed") ||
		strings.Contains(message, "pipe has been ended")
}
