//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package process

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (p *managedProcess) resize(size TerminalSize) error {
	if !p.terminal {
		return domain.Fail(domain.Unsupported, "This process is not a terminal.", "Select a session terminal.")
	}
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if err := p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := json.NewEncoder(p.conn).Encode(processFrame{Kind: processResize, Size: &size}); err != nil {
		return err
	}
	select {
	case <-p.inputAck:
		return nil
	case <-p.done:
		return domain.Fail(domain.Unavailable, "The terminal exited during resize.", "Inspect the original terminal.")
	case <-time.After(5 * time.Second):
		return ownershipError()
	}
}

// PTY children stay inside the original subreaper/coalition scope, including
// children that detach from the controlling terminal. A process group alone
// cannot prove that a daemonized descendant has stopped.
func superviseTerminal(dir string, scope processScope, command processCommand, decoder *wireDecoder, writer *frameWriter, _ func() (*os.File, *os.File, error)) int {
	complete := func(exit int, failure domain.Code) int {
		scope.Complete = true
		if saveScope(dir, scope) != nil {
			return 3
		}
		_ = writer.frame(processFrame{Kind: processExit, Exit: exit, Failure: failure})
		return 0
	}
	if command.Terminal.Validate() != nil {
		return complete(127, domain.InvalidArgument)
	}
	master, slave, err := pty.Open()
	if err != nil {
		return complete(127, domain.Unavailable)
	}
	defer master.Close()
	defer slave.Close()
	if pty.Setsize(master, &pty.Winsize{Rows: command.Terminal.Rows, Cols: command.Terminal.Columns}) != nil {
		return complete(127, domain.Unavailable)
	}
	cmd := exec.Command(command.Path)
	cmd.Args, cmd.Env, cmd.Dir = command.Args, command.Env, command.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	scope.Started = true
	if saveScope(dir, scope) != nil {
		return 3
	}
	if err = cmd.Start(); err != nil {
		code := domain.Unavailable
		switch {
		case errors.Is(err, os.ErrNotExist):
			code = domain.NotFound
		case errors.Is(err, os.ErrPermission):
			code = domain.PermissionDenied
		case errors.Is(err, syscall.ENOEXEC):
			code = domain.Unsupported
		}
		return complete(127, code)
	}
	command = processCommand{}
	_ = slave.Close()
	cancel := make(chan struct{})
	frames := make(chan processFrame, 8)
	inputDone := make(chan struct{})
	stopInput := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			select {
			case <-stopInput:
				return
			case f := <-frames:
				var err error
				switch f.Kind {
				case processInput:
					_, err = master.Write(f.Data)
				case processResize:
					err = pty.Setsize(master, &pty.Winsize{Rows: f.Size.Rows, Cols: f.Size.Columns})
				default:
					return
				}
				if err != nil || writer.frame(processFrame{Kind: processInputAck}) != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer close(cancel)
		for {
			var f processFrame
			if decoder.Decode(&f) != nil {
				return
			}
			if (f.Kind != processInput && f.Kind != processResize) || len(f.Data) > 32768 || (f.Kind == processResize && (f.Size == nil || f.Size.Validate() != nil)) {
				return
			}
			select {
			case frames <- f:
			default:
				return
			}
		}
	}()
	copied := make(chan struct{})
	go func() {
		// Linux returns EIO when the last slave closes; this is terminal EOF.
		_, _ = io.Copy(streamWriter{writer: writer, kind: processOutput}, master)
		close(copied)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	var waitErr error
	rootDone := false
	select {
	case waitErr = <-waited:
		rootDone = true
	case <-cancel:
	case <-signals:
	case <-copied:
	case <-inputDone:
	}
	for {
		if !rootDone {
			select {
			case waitErr = <-waited:
				rootDone = true
			default:
			}
		}
		empty, err := drainScope(scope, rootDone)
		if err == nil && rootDone && empty {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// All slaves are now independently proven closed. Join both pumps before
	// persisting completion; closing the master also releases a blocked writer.
	close(stopInput)
	// Keep the final kernel-buffered output reachable while the bounded socket
	// consumer applies backpressure. Closing after an arbitrary grace period
	// would silently truncate a normal exit. Socket writes remain deadline-bound
	// and owner cancellation closes the control connection to release the pump.
	<-copied
	_ = master.Close()
	<-inputDone
	exit := 0
	var exited *exec.ExitError
	if errors.As(waitErr, &exited) {
		exit = exited.ExitCode()
		if exit < 0 {
			exit = 128 + int(exited.Sys().(syscall.WaitStatus).Signal())
		}
	}
	return complete(exit, "")
}
