//go:build !windows

package transport

import (
	"context"
	"errors"
	"io"
)

func RunWindowsConPTY(
	_ context.Context,
	_ []string,
	_ string,
	_ func(pid int) error,
	_ io.Writer,
) (RunResult, error) {
	return RunResult{}, errors.New("failed to run windows conpty mode: unsupported on non-windows platforms")
}
