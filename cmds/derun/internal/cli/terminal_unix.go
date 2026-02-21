//go:build unix

package cli

import (
	"os"

	"github.com/creack/pty"
)

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	_, _, err := pty.Getsize(file)
	return err == nil
}
