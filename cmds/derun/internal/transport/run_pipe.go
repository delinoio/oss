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
	"time"
)

const pipeOutputDrainTimeout = time.Second

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

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return RunResult{}, commandRuntimeError("create stdout pipe", command, workingDir, err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return RunResult{}, commandRuntimeError("create stderr pipe", command, workingDir, err)
	}

	// Pass files to exec.Cmd so Wait only waits for the requested process. If a
	// descendant inherits the descriptors, our copy goroutines are closed after
	// the requested process exits instead of extending cmd.Wait indefinitely.
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return RunResult{}, commandRuntimeError("start process", command, workingDir, err)
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	copyDone := make(chan error, 2)
	go copyPipeOutput(stdoutReader, stdout, copyDone)
	go copyPipeOutput(stderrReader, stderr, copyDone)
	outputClosed := false
	closeOutput := func() error {
		defer func() {
			_ = stdoutReader.Close()
			_ = stderrReader.Close()
		}()

		var copyErr error
		completed := 0
		drainTimer := time.NewTimer(pipeOutputDrainTimeout)
		defer drainTimer.Stop()
		for completed < 2 {
			select {
			case err := <-copyDone:
				completed++
				if err != nil && !isBenignCopyErr(err) {
					if copyErr == nil {
						copyErr = err
					}
					// A failed output sink cannot drain the other reader. Close both
					// readers so cleanup remains bounded and the error is returned.
					_ = stdoutReader.Close()
					_ = stderrReader.Close()
				}
			case <-drainTimer.C:
				// A descendant may inherit the pipe descriptors and keep them open.
				// Give direct-child output time to drain, then break that wait.
				_ = stdoutReader.Close()
				_ = stderrReader.Close()
			}
		}
		return copyErr
	}
	defer func() {
		if !outputClosed {
			_ = closeOutput()
		}
	}()

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
	copyErr := closeOutput()
	outputClosed = true
	if copyErr != nil {
		return RunResult{}, commandRuntimeError("copy pipe output", command, workingDir, copyErr)
	}
	return result, nil
}

func copyPipeOutput(reader *os.File, writer io.Writer, done chan<- error) {
	_, err := io.Copy(writer, reader)
	_ = reader.Close()
	done <- err
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
