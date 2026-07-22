package transport

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

	cmd.Stdout = stdout
	cmd.Stderr = stderr

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

	waitErr := cmd.Wait()
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
