package transport

import (
	"context"
	"errors"
	"fmt"
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
		return RunResult{}, fmt.Errorf("command is empty")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start process: %w", err)
	}

	// Install forwarding handlers immediately after process start so SIGINT/SIGTERM
	// cannot race with onStart metadata persistence in caller paths.
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	defer close(signals)
	go func() {
		for sig := range signals {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
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

	waitErr := cmd.Wait()
	wg.Wait()
	close(copyErr)
	for err := range copyErr {
		if err != nil {
			return RunResult{}, fmt.Errorf("copy process output: %w", err)
		}
	}

	result, err := decodeExit(waitErr)
	if err != nil {
		return RunResult{}, fmt.Errorf("wait for process: %w", err)
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
