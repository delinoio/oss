//go:build !windows

package process

import (
	"bufio"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const supervisorArgument = "__delidev_process_supervisor_v1"

type processScope struct {
	Version   int       `json:"version"`
	OwnerID   domain.ID `json:"owner_id"`
	Boot      string    `json:"boot"`
	Owner     Process   `json:"owner"`
	Coalition uint64    `json:"coalition,omitempty"`
	Label     string    `json:"label,omitempty"`
	Domain    string    `json:"domain,omitempty"`
	Started   bool      `json:"started"`
	Complete  bool      `json:"complete"`
}

type processCommand struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type processFrameKind string

const (
	processReady    processFrameKind = "ready"
	processOutput   processFrameKind = "stdout"
	processError    processFrameKind = "stderr"
	processInput    processFrameKind = "stdin"
	processInputEnd processFrameKind = "stdin-end"
	processInputAck processFrameKind = "stdin-ack"
	processExit     processFrameKind = "exit"
)

type processFrame struct {
	Kind  processFrameKind `json:"kind"`
	Scope *processScope    `json:"scope,omitempty"`
	Data  []byte           `json:"data,omitempty"`
	Exit  int              `json:"exit,omitempty"`
}

type managedProcess struct {
	identity     Process
	command      processCommand
	conn         net.Conn
	sendMu       sync.Mutex
	inputAck     chan struct{}
	inputClosed  bool
	done         chan struct{}
	waitErr      error
	reconcileErr error
	cleanup      func() error
}

func scopeError() error {
	return ownershipError()
}
func scopePath(dir string) string { return filepath.Join(dir, "ownership.json") }
func saveScope(dir string, scope processScope) error {
	b, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	return security.WriteAtomic(scopePath(dir), b)
}
func readScope(dir string) (processScope, error) {
	var s processScope
	b, err := security.ReadPrivate(scopePath(dir), 1024*1024)
	if err != nil {
		return s, err
	}
	if err = domain.Decode(b, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Boot == "" || s.OwnerID.Validate() != nil {
		return s, scopeError()
	}
	return s, nil
}

// Re-exec the same binary, including Go test binaries. This entry point never
// reads user configuration or starts a daemon. The private socket supplies the
// command only after its durable ownership has been recorded by the worker.
func init() {
	if len(os.Args) == 4 && os.Args[1] == supervisorArgument {
		os.Exit(superviseProcess(os.Args[2], os.Args[3]))
	}
}

func startProcess(c *exec.Cmd, dir string, owner domain.ID) (_ *managedProcess, err error) {
	if err = security.PrivateDir(dir); err != nil {
		return nil, err
	}
	boot, err := bootIdentity()
	if err != nil {
		return nil, err
	}
	scope := processScope{Version: 1, Boot: boot, OwnerID: owner}
	if err = prepareScope(&scope, dir); err != nil {
		return nil, err
	}
	if err = saveScope(dir, scope); err != nil {
		return nil, err
	}
	// Unix socket names have a small platform limit independent of state paths.
	socketDir, err := os.MkdirTemp("/tmp", "delidev-p-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "control")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		return nil, err
	}
	cleanup, err := launchScope(scope, dir, socket)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = cleanup()
		}
	}()
	_ = listener.SetDeadline(time.Now().Add(15 * time.Second))
	conn, err := listener.AcceptUnix()
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	decoder := newWireDecoder(conn)
	var ready processFrame
	if err = decoder.Decode(&ready); err != nil {
		conn.Close()
		return nil, err
	}
	if ready.Kind != processReady || ready.Scope == nil {
		conn.Close()
		return nil, scopeError()
	}
	// Read the journal independently; do not accept a socket peer's claimed PID.
	saved, err := readScope(dir)
	if err != nil || saved.OwnerID != owner || ready.Scope.OwnerID != owner || saved.Owner.PID != ready.Scope.Owner.PID || saved.Owner.Birth != ready.Scope.Owner.Birth || !ProcessAlive(saved.Owner) {
		conn.Close()
		return nil, scopeError()
	}
	_ = conn.SetReadDeadline(time.Time{})
	identity := saved.Owner
	identity.ScopeDir = dir
	identity.OwnerID = owner
	p := &managedProcess{identity: identity, command: processCommand{c.Path, c.Args, c.Env, c.Dir}, conn: conn, done: make(chan struct{}), inputAck: make(chan struct{}, 1), cleanup: cleanup}
	success = true
	go func() {
		defer close(p.done)
		defer conn.Close()
		for {
			var f processFrame
			if err := decoder.Decode(&f); err != nil {
				p.reconcileErr = scopeError()
				p.waitErr = p.reconcileErr
				return
			}
			switch f.Kind {
			case processInputAck:
				select {
				case p.inputAck <- struct{}{}:
				default:
					p.reconcileErr = scopeError()
					p.waitErr = p.reconcileErr
					return
				}
			case processOutput, processError:
				if len(f.Data) > 32768 {
					p.reconcileErr = scopeError()
					p.waitErr = p.reconcileErr
					return
				}
				target := c.Stdout
				if f.Kind == processError {
					target = c.Stderr
				}
				if target != nil {
					if _, err := target.Write(f.Data); err != nil {
						p.reconcileErr = err
						p.waitErr = err
						return
					}
				}
			case processExit:
				s, err := readScope(dir)
				if err != nil || !s.Complete {
					p.reconcileErr = scopeError()
					p.waitErr = p.reconcileErr
					return
				}
				if err = cleanup(); err != nil {
					p.reconcileErr = err
					p.waitErr = err
					return
				}
				if f.Exit != 0 {
					p.waitErr = commandExitError(f.Exit)
				}
				return
			default:
				p.reconcileErr = scopeError()
				p.waitErr = p.reconcileErr
				return
			}
		}
	}()
	return p, nil
}
func (p *managedProcess) resume() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	err := json.NewEncoder(p.conn).Encode(p.command)
	// Drop the resolved environment as soon as it crosses the private channel.
	p.command = processCommand{}
	return err
}
func (p *managedProcess) wait() error       { <-p.done; return p.waitErr }
func (p *managedProcess) sample() error     { return nil }
func (p *managedProcess) snapshot() Process { return p.identity }
func (p *managedProcess) terminate() error {
	select {
	case <-p.done:
		return p.reconcileErr
	default:
	}
	// Half-close the control direction. The supervisor still streams output and
	// confirms cleanup through the other direction, including before resume.
	// CloseWrite is safe concurrently with an encoder. Cancellation must not
	// wait behind a blocked stdin acknowledgment holding sendMu.
	if c, ok := p.conn.(*net.UnixConn); ok {
		_ = c.CloseWrite()
	}
	select {
	case <-p.done:
		return p.reconcileErr
	case <-time.After(10 * time.Second):
		return scopeError()
	}
}
func (p *managedProcess) close() { _ = p.terminate(); _ = p.conn.Close() }

type frameWriter struct {
	mu   sync.Mutex
	enc  *json.Encoder
	conn net.Conn
}

func (w *frameWriter) frame(f processFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	return w.enc.Encode(f)
}

type streamWriter struct {
	writer *frameWriter
	kind   processFrameKind
}

func (w streamWriter) Write(b []byte) (int, error) {
	total := len(b)
	for len(b) > 0 {
		n := len(b)
		if n > 32768 {
			n = 32768
		}
		if err := w.writer.frame(processFrame{Kind: w.kind, Data: b[:n]}); err != nil {
			return total - len(b), err
		}
		b = b[n:]
	}
	return total, nil
}

func superviseProcess(dir, socket string) int {
	scope, err := readScope(dir)
	if err != nil {
		return 3
	}
	if err = initializeScope(&scope); err != nil {
		return 3
	}
	scope.Owner.OwnerID = scope.OwnerID
	scope.Owner.ScopeDir = dir
	if err = saveScope(dir, scope); err != nil {
		return 3
	}
	conn, err := net.DialTimeout("unix", socket, 10*time.Second)
	if err != nil {
		return 3
	}
	defer conn.Close()
	writer := &frameWriter{enc: json.NewEncoder(conn), conn: conn}
	if err = writer.frame(processFrame{Kind: processReady, Scope: &scope}); err != nil {
		return 3
	}
	var command processCommand
	decoder := newWireDecoder(conn)
	if err = decoder.Decode(&command); err != nil {
		scope.Complete = true
		if saveScope(dir, scope) != nil {
			return 3
		}
		_ = writer.frame(processFrame{Kind: processExit, Exit: 1})
		return 0
	}
	cancel := make(chan struct{})
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return 3
	}
	defer inputRead.Close()
	defer inputWrite.Close()

	inputFrames := make(chan processFrame, 8)
	go func() {
		defer inputWrite.Close()
		for {
			select {
			case <-cancel:
				return
			case frame := <-inputFrames:
				if frame.Kind == processInputEnd {
					if err := writer.frame(processFrame{Kind: processInputAck}); err != nil {
						return
					}
					_ = inputWrite.Close()
					continue
				}
				if _, err := inputWrite.Write(frame.Data); err != nil {
					return
				}
				if err := writer.frame(processFrame{Kind: processInputAck}); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer close(cancel)
		defer inputWrite.Close()
		for {
			var frame processFrame
			if err := decoder.Decode(&frame); err != nil {
				return
			}
			if (frame.Kind != processInput && frame.Kind != processInputEnd) || len(frame.Data) > 32768 {
				return
			}
			// Never let a blocked child stdin prevent the control reader from noticing
			// disconnect. Saturation cancels this execution instead of dropping bytes.
			select {
			case inputFrames <- frame:
			default:
				return
			}
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	scope.Started = true
	if err = saveScope(dir, scope); err != nil {
		return 3
	}
	// Use actual OS pipes so exec.Cmd.Wait observes shell exit independently of
	// inherited output descriptors held open by a daemonized descendant.
	read, write, err := os.Pipe()
	if err != nil {
		scope.Complete = true
		_ = saveScope(dir, scope)
		return 3
	}
	defer read.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		write.Close()
		return 3
	}
	defer stderrRead.Close()
	cmd := exec.Command(command.Path)
	cmd.Args = command.Args
	cmd.Env = command.Env
	cmd.Dir = command.Dir
	cmd.Stdout = write
	cmd.Stderr = stderrWrite
	cmd.Stdin = inputRead
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		write.Close()
		stderrWrite.Close()
		scope.Complete = true
		_ = saveScope(dir, scope)
		_ = writer.frame(processFrame{Kind: processExit, Exit: 127})
		return 0
	}
	command = processCommand{}
	_ = write.Close()
	_ = stderrWrite.Close()
	_ = inputRead.Close()
	copied := make(chan struct{})
	copiedError := make(chan struct{})
	outputFailure := make(chan struct{}, 1)
	go func() {
		if _, err := io.Copy(streamWriter{writer: writer, kind: processOutput}, read); err != nil {
			select {
			case outputFailure <- struct{}{}:
			default:
			}
		}
		close(copied)
	}()
	go func() {
		if _, err := io.Copy(streamWriter{writer: writer, kind: processError}, stderrRead); err != nil {
			select {
			case outputFailure <- struct{}{}:
			default:
			}
		}
		close(copiedError)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var waitErr error
	rootDone := false
	select {
	case waitErr = <-waited:
		rootDone = true
	case <-cancel:
	case <-signals:
	case <-outputFailure:
	}
	// A kernel ownership scope, not a sampled ancestry snapshot, determines
	// membership. An empty scope is permanent because no remaining task can fork.
	for {
		if !rootDone {
			select {
			case waitErr = <-waited:
				rootDone = true
			default:
			}
		}
		empty, err := drainScope(scope, rootDone)
		if err != nil {
			// Preserve kernel ownership while inspection is unavailable. The
			// worker times out cancellation and retains the scheduling claim.
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if rootDone && empty {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = inputWrite.Close()
	<-copied
	<-copiedError
	scope.Complete = true
	if err = saveScope(dir, scope); err != nil {
		return 3
	}
	exit := 0
	if waitErr != nil {
		exit = 1
		var e *exec.ExitError
		if errors.As(waitErr, &e) {
			exit = e.ExitCode()
			if exit < 0 {
				exit = 128 + int(e.Sys().(syscall.WaitStatus).Signal())
			}
		}
	}
	_ = writer.frame(processFrame{Kind: processExit, Exit: exit})
	return 0
}

func reconcileScope(dir string, started bool) error {
	scope, err := readScope(dir)
	if os.IsNotExist(err) && !started {
		// The worker records the scope path before any launch. No journal means
		// that launch was never allowed to begin.
		return nil
	}
	if err != nil {
		return scopeError()
	}
	boot, err := bootIdentity()
	if err != nil {
		return err
	}
	if boot != scope.Boot {
		return nil
	}
	return recoverScope(scope, dir)
}

// A dead supervisor is not proof of dead descendants. Account lifecycle guards
// retain this lease until the backend or a changed boot identity proves it safe.
func supervisorLeaseActive(p Process) (bool, error) {
	scope, err := readScope(p.ScopeDir)
	if err != nil {
		return false, scopeError()
	}
	boot, err := bootIdentity()
	if err != nil {
		return false, err
	}
	if boot != scope.Boot {
		return false, nil
	}
	if ProcessAlive(p) {
		return true, nil
	}
	if scope.Complete || !scope.Started {
		return false, nil
	}
	return scopeHasSurvivors(scope)
}

type wireDecoder struct{ scanner *bufio.Scanner }

func newWireDecoder(reader io.Reader) *wireDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 32<<10), 256<<10)
	return &wireDecoder{scanner: scanner}
}
func (d *wireDecoder) Decode(value any) error {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return domain.Decode(d.scanner.Bytes(), value)
}

func (p *managedProcess) ack() error {
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-p.inputAck:
		return nil
	case <-p.done:
		return io.ErrClosedPipe
	case <-timer.C:
		return ownershipError()
	}
}
func (p *managedProcess) write(data []byte) (int, error) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if p.inputClosed {
		return 0, io.ErrClosedPipe
	}
	total := len(data)
	for len(data) > 0 {
		n := min(len(data), 32768)
		_ = p.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if err := json.NewEncoder(p.conn).Encode(processFrame{Kind: processInput, Data: data[:n]}); err != nil {
			return total - len(data), err
		}
		if err := p.ack(); err != nil {
			return total - len(data), err
		}
		data = data[n:]
	}
	return total, nil
}
func (p *managedProcess) closeInput() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if p.inputClosed {
		return nil
	}
	p.inputClosed = true
	_ = p.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(p.conn).Encode(processFrame{Kind: processInputEnd}); err != nil {
		return err
	}
	return p.ack()
}
