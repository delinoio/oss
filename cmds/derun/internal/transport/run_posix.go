//go:build !windows

package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
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
	defer signal.Stop(resize)
	defer close(resize)
	go func() {
		for range resize {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	resize <- syscall.SIGWINCH

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

	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()

	if _, err := io.Copy(ptyOutput, ptmx); err != nil {
		return RunResult{}, fmt.Errorf("copy pty output: %w", err)
	}
	result, err := decodeExit(cmd.Wait())
	if err != nil {
		return RunResult{}, fmt.Errorf("wait for pty process: %w", err)
	}
	return result, nil
}
