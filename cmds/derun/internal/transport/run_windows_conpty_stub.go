//go:build !windows

package transport

import (
	"context"
	"errors"
	"io"
)

func RunWindowsConPTY(
	_ context.Context,
	command []string,
	workingDir string,
	_ func(pid int) error,
	_ io.Writer,
) (RunResult, error) {
	return RunResult{}, commandRuntimeError("run windows conpty mode", command, workingDir, errors.New("unsupported on non-windows platforms"))
}
