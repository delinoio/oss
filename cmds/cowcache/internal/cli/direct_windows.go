package cli

import (
	"github.com/delinoio/oss/cmds/cowcache/internal/process"
	"os"
	"os/exec"
)

func direct(c process.Command) (int, error) {
	cmd := exec.Command(c.Path)
	cmd.Args = c.Args
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	e := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return executionCode(e), e
}

func childCode(s *os.ProcessState) int { return s.ExitCode() }
