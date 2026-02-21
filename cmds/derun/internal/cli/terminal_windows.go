//go:build windows

package cli

import (
	"os"
	"syscall"
)

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}
