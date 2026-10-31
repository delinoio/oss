package cli

import (
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"os"
)

func direct(c process.Command) (int, error) {
	cmd, e := process.NewExecCommand(c)
	if e != nil {
		return executionCode(e), e
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	e = cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return executionCode(e), e
}

func childCode(s *os.ProcessState) int { return s.ExitCode() }
