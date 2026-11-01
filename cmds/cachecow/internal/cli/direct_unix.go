//go:build linux || darwin

package cli

import (
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"os"
	"syscall"
)

func direct(c process.Command) (int, error) {
	if e := os.Chdir(c.Dir); e != nil {
		return 125, e
	}
	e := syscall.Exec(c.Path, c.Args, c.Env)
	return executionCode(e), e
}

func childCode(s *os.ProcessState) int {
	if v, ok := s.Sys().(syscall.WaitStatus); ok && v.Signaled() {
		return 128 + int(v.Signal())
	}
	return s.ExitCode()
}
