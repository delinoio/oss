//go:build windows

package transport

import (
	"context"
	"errors"
	"io"
)

func RunPosixPTY(
	_ context.Context,
	command []string,
	workingDir string,
	_ func(pid int) error,
	_ io.Writer,
) (RunResult, error) {
	return RunResult{}, commandRuntimeError("run posix pty mode", command, workingDir, errors.New("unsupported on windows"))
}
