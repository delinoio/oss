//go:build windows

package transport

import (
	"context"
	"errors"
	"io"
)

func RunPosixPTY(
	_ context.Context,
	_ []string,
	_ string,
	_ func(pid int) error,
	_ io.Writer,
) (RunResult, error) {
	return RunResult{}, errors.New("failed to run posix pty mode: unsupported on windows")
}
