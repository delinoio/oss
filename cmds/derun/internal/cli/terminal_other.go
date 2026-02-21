//go:build !unix && !windows

package cli

import "os"

func isTerminal(_ *os.File) bool {
	return false
}
