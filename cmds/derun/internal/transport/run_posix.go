//go:build !windows

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
	"syscall"

	"github.com/creack/pty"
)

func RunPosixPTY(
	ctx context.Context,
	command []string,
	workingDir string,
	onStart func(pid int) error,
	ptyOutput io.Writer,
) (RunResult, error) {
	if len(command) == 0 {
		return RunResult{}, fmt.Errorf("command is empty")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workingDir

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return RunResult{}, fmt.Errorf("start pty process: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	if onStart != nil {
		if err := onStart(cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			return RunResult{}, err
		}
	}

	if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
		return RunResult{}, fmt.Errorf("inherit pty size: %w", err)
	}

	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	resizeDone := make(chan struct{})
	defer close(resizeDone)
	defer signal.Stop(resize)
	go func() {
		for {
			select {
			case <-resize:
				_ = pty.InheritSize(os.Stdin, ptmx)
			case <-resizeDone:
				return
			}
		}
	}()

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

	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()

	if _, err := io.Copy(ptyOutput, ptmx); err != nil && !isBenignPTYOutputErr(err) {
		return RunResult{}, fmt.Errorf("copy pty output: %w", err)
	}
	result, err := decodeExit(cmd.Wait())
	if err != nil {
		return RunResult{}, fmt.Errorf("wait for pty process: %w", err)
	}
	return result, nil
}

func isBenignPTYOutputErr(err error) bool {
	if err == nil {
		return false
	}
	if isBenignCopyErr(err) {
		return true
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	if pathErr.Op != "read" {
		return false
	}
	if !errors.Is(pathErr.Err, syscall.EIO) {
		return false
	}
	// Linux PTYs can return EIO specifically when reading from /dev/ptmx after
	// the slave side is closed. Restrict suppression to this read-close case.
	return strings.Contains(strings.ToLower(pathErr.Path), "ptmx")
}
