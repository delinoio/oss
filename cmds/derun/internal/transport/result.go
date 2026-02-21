package transport

import (
	"errors"
	"os/exec"
	"syscall"
)

type RunResult struct {
	ExitCode  *int
	Signal    string
	SignalNum int
}

func decodeExit(err error) (RunResult, error) {
	if err == nil {
		code := 0
		return RunResult{ExitCode: &code}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return RunResult{}, err
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return RunResult{}, err
	}
	if status.Signaled() {
		return RunResult{Signal: status.Signal().String(), SignalNum: int(status.Signal())}, nil
	}
	code := status.ExitStatus()
	return RunResult{ExitCode: &code}, nil
}
