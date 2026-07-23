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
	copyStates := [2]*pipeCopyState{newPipeCopyState(), newPipeCopyState()}
	go copyPipeOutput(stdoutReader, stdout, copyStates[0], copyDone)
	go copyPipeOutput(stderrReader, stderr, copyStates[1], copyDone)
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
			case <-copyStates[0].activity:
				if copyStates[0].isPending() || copyStates[1].isPending() {
					if !drainTimer.Stop() {
						select {
						case <-drainTimer.C:
						default:
						}
					}
				} else {
					drainTimer.Reset(pipeOutputDrainTimeout)
				}
			case <-copyStates[1].activity:
				if copyStates[0].isPending() || copyStates[1].isPending() {
					if !drainTimer.Stop() {
						select {
						case <-drainTimer.C:
						default:
						}
					}
				} else {
					drainTimer.Reset(pipeOutputDrainTimeout)
				}
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
				// Only force-close after both copy loops have been idle. A pending
				// write means direct-child output has already been read and must be
				// allowed to reach the sink, even when that sink is slow.
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

type pipeCopyState struct {
	mu       sync.Mutex
	pending  bool
	activity chan struct{}
}

func newPipeCopyState() *pipeCopyState {
	return &pipeCopyState{activity: make(chan struct{}, 1)}
}

func (s *pipeCopyState) setPending(pending bool) {
	s.mu.Lock()
	s.pending = pending
	s.mu.Unlock()
	select {
	case s.activity <- struct{}{}:
	default:
	}
}

func (s *pipeCopyState) isPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

func copyPipeOutput(reader *os.File, writer io.Writer, state *pipeCopyState, done chan<- error) {
	trackingWriter := pipeCopyWriter{writer: writer, state: state}
	_, err := io.Copy(trackingWriter, reader)
	_ = reader.Close()
	done <- err
}

type pipeCopyWriter struct {
	writer io.Writer
	state  *pipeCopyState
}

func (w pipeCopyWriter) Write(p []byte) (int, error) {
	w.state.setPending(true)
	n, err := w.writer.Write(p)
	w.state.setPending(false)
	return n, err
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
