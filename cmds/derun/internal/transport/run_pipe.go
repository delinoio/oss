package transport

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func RunPipe(
	ctx context.Context,
	command []string,
	workingDir string,
	onStart func(pid int) error,
	stdout io.Writer,
	stderr io.Writer,
) (RunResult, error) {
	if len(command) == 0 {
		return RunResult{}, commandRuntimeError("run command", command, workingDir, errors.New("command is empty"))
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, commandRuntimeError("create stdout pipe", command, workingDir, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, commandRuntimeError("create stderr pipe", command, workingDir, err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, commandRuntimeError("start process", command, workingDir, err)
	}

	// Install forwarding handlers immediately after process start so SIGINT/SIGTERM
	// cannot race with onStart metadata persistence in caller paths.
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	signalForwardDone := make(chan struct{})
	defer close(signalForwardDone)
	defer signal.Stop(signals)
	go func() {
		for {
			select {
			case sig := <-signals:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
			case <-signalForwardDone:
				return
			}
		}
	}()

	if onStart != nil {
		if err := onStart(cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			return RunResult{}, err
		}
	}

	var wg sync.WaitGroup
	copyErr := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := io.Copy(stdout, stdoutPipe); err != nil && !isBenignCopyErr(err) {
			copyErr <- err
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := io.Copy(stderr, stderrPipe); err != nil && !isBenignCopyErr(err) {
			copyErr <- err
		}
	}()

	// Reap the direct child before waiting for pipe readers. A child can exit
	// while a background descendant still holds an inherited pipe writer open;
	// Cmd.Wait closes the parent pipe ends after reaping the child so the readers
	// can finish in that case.
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
	}()
	waitErr := <-waitErrCh
	wg.Wait()
	close(copyErr)
	for err := range copyErr {
		if err != nil {
			return RunResult{}, commandRuntimeError("copy process output", command, workingDir, err)
		}
	}

	result, err := decodeExit(waitErr)
	if err != nil {
		return RunResult{}, commandRuntimeError("wait for process", command, workingDir, err)
	}
	return result, nil
}

func isBenignCopyErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "file already closed")
}
