// Package process owns a command tree independently of its calling CLI.
// Callers must retain their durable cache claim until Run or Reconcile proves
// completion. Losing the CLI or supervisor is never completion evidence.
package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

const supervisorArgument = "__cowc_process_supervisor_v1"
const GracePeriod = 5 * time.Second

var ErrReconciliation = errors.New("cannot prove the owned process tree has exited; retain the cache claim and retry recovery")
var ErrUnavailable = errors.New("native process ownership is unavailable; command was not started")

// Command uses an already resolved executable path. Args includes argv[0].
// Env is explicit: an empty slice means an empty environment.
type Command struct {
	// Noop runs supervisor hooks without launching user code (prepare and clean).
	Noop bool
	Path string
	Args []string
	Dir  string
	Env  []string
}

// Streams are real inherited descriptors, preserving terminal behavior and
// independent stdout/stderr. Nil fields inherit the corresponding CLI stream.
type Streams struct{ Stdin, Stdout, Stderr *os.File }

// Scope contains no command arguments, environment, or captured child output.
// Directory is a unique private journal directory beneath the supplied state root.
type Scope struct {
	Context   context.Context `json:"-"`
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Directory string          `json:"directory"`
	OwnerPID  int             `json:"owner_pid"`
	NativeID  uint64          `json:"native_id,omitempty"`
	Started   bool            `json:"started"`
	Complete  bool            `json:"complete"`
	ExitCode  int             `json:"exit_code"`
}

// Hooks integrate durable cache ownership. Owned MUST durably associate the
// scope with all held cache claims before returning. Drained is called only
// after durable whole-tree completion. Neither callback may release a claim
// after an error merely because the root or supervisor PID is absent.
type Hooks struct {
	Owned   func(Scope) error
	Drained func(Scope) error
}

type Result struct {
	ExitCode int
	Scope    Scope
}

type frame struct {
	Scope   *Scope
	Command *Command
}

// Run returns the exact child exit status separately from infrastructure errors.
// On any error after Owned, retain the claim and call Reconcile before release.
// The state root must be current-user-private, persistent storage, not a cache.
func Run(ctx context.Context, stateRoot string, command Command, streams Streams, hooks Hooks) (Result, error) {
	result := Result{ExitCode: 125}
	if hooks.Owned == nil || hooks.Drained == nil {
		return result, errors.New("durable ownership and drained callbacks are required")
	}
	if !filepath.IsAbs(stateRoot) {
		return result, errors.New("absolute private process state root is required")
	}
	if !command.Noop && (command.Path == "" || !filepath.IsAbs(command.Path) || len(command.Args) == 0 || !filepath.IsAbs(command.Dir)) {
		return result, errors.New("resolved absolute executable, cwd, and argv are required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := os.Lstat(stateRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("private process state directory is required")
	}
	if err = privateRoot(stateRoot, info); err != nil {
		return result, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return result, err
	}
	dir := filepath.Join(stateRoot, id.String())
	if err = os.Mkdir(dir, 0700); err != nil {
		return result, err
	}
	scope := Scope{Version: 1, ID: id.String(), Directory: dir, ExitCode: 125}
	result.Scope = scope
	if err = save(scope); err != nil {
		return result, err
	}
	listener, endpoint, cleanup, err := listenControl()
	if err != nil {
		return result, err
	}
	defer cleanup()
	defer listener.Close()
	streams = defaultStreams(streams)
	cleanupLaunch, err := launch(scope, endpoint, streams)
	if err != nil {
		return result, ErrUnavailable
	}
	defer cleanupLaunch()
	// Closing the listener also bounds startup when launchd cannot start the helper.
	timer := time.AfterFunc(15*time.Second, func() { _ = listener.Close() })
	defer timer.Stop()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	conn, err := listener.Accept()
	if err != nil {
		return result, ErrReconciliation
	}
	defer conn.Close()
	if err = sendStreams(conn, streams); err != nil {
		return result, ErrReconciliation
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	dec := json.NewDecoder(conn)
	var ready frame
	if err = dec.Decode(&ready); err != nil || ready.Scope == nil {
		return result, ErrUnavailable
	}
	saved, err := read(scope)
	if err != nil || saved.OwnerPID != ready.Scope.OwnerPID || saved.OwnerPID <= 0 {
		return result, ErrReconciliation
	}
	scope = saved
	result.Scope = scope
	_ = conn.SetDeadline(time.Time{})
	if err = hooks.Owned(scope); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if command.Env == nil {
		command.Env = []string{}
	}
	if err = json.NewEncoder(conn).Encode(frame{Command: &command}); err != nil {
		return result, ErrReconciliation
	}
	_ = conn.SetDeadline(time.Time{})
	// EOF requests cancellation but keeps the helper alive to drain the tree.
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	var finished frame
	err = dec.Decode(&finished)
	if err != nil {
		// Cancellation closes control immediately; the helper independently grants
		// five seconds, escalates, and writes completion. Bound the caller's wait,
		// retaining ownership if the kernel cannot yet prove completion.
		if ctx.Err() == nil {
			return result, ErrReconciliation
		}
		deadline := time.Now().Add(GracePeriod + 5*time.Second)
		for time.Now().Before(deadline) {
			if Reconcile(scope) == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err = Reconcile(scope); err != nil {
		return result, err
	}
	scope, err = read(scope)
	if err != nil {
		return result, ErrReconciliation
	}
	result = Result{ExitCode: scope.ExitCode, Scope: scope}
	if err = hooks.Drained(scope); err != nil {
		return result, err
	}
	return result, nil
}

// Reconcile deliberately fails closed for every unfinished journal, including
// a dead supervisor. A live supervisor can finish after CLI death. A killed
// supervisor needs external native recovery; PID absence alone cannot unlock.
// Unknown versions are read-only and blocked.
// LoadScope reads recovery metadata from an ownership directory saved in a
// durable lease. This is verification metadata only, never session authority.
func LoadScope(directory string) (Scope, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return Scope{}, ErrReconciliation
	}
	return read(Scope{Version: 1, ID: filepath.Base(directory), Directory: directory})
}

func Reconcile(scope Scope) error {
	s, err := read(scope)
	if err != nil || !s.Complete || !s.Started || s.OwnerPID <= 0 {
		return ErrReconciliation
	}
	return nil
}

func defaultStreams(s Streams) Streams {
	if s.Stdin == nil {
		s.Stdin = os.Stdin
	}
	if s.Stdout == nil {
		s.Stdout = os.Stdout
	}
	if s.Stderr == nil {
		s.Stderr = os.Stderr
	}
	return s
}
func read(expected Scope) (Scope, error) {
	var s Scope
	b, err := os.ReadFile(filepath.Join(expected.Directory, "scope.json"))
	if err != nil {
		return s, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&s); err != nil {
		return s, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return s, ErrReconciliation
	}
	if s.Version != 1 || expected.Version != 1 || s.ID != expected.ID || s.Directory != expected.Directory {
		return s, ErrReconciliation
	}
	return s, nil
}
func save(s Scope) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Directory, ".scope-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, filepath.Join(s.Directory, "scope.json")); err != nil {
		return err
	}
	return syncDirectory(s.Directory)
}

// SupervisorHooks run in the separate supervisor, not in the calling CLI.
// Ready can establish a lease registry/session and authenticated nesting IPC.
// BeforeStart can acquire resources and inject transient IPC credentials into
// the command environment. Never persist that environment or trust a bare
// session ID as authority. Hooks must not spawn subprocesses; use Go IPC and storage operations.
// Quiesce must stop new IPC requests and await in-flight requests; it runs
// after native tree drain and before the durable completion proof is written.
// Drained can release durable leases using Reconcile after that proof exists.
// The embedding application owns the IPC protocol and authenticates clients.
type SupervisorHooks struct {
	Ready       func(Scope) error
	BeforeStart func(Scope, *Command) error
	Quiesce     func(Scope) error
	Drained     func(Scope) error
}

// SupervisorMain must be called at the start of the embedding executable's
// main (or TestMain), before parsing normal CLI flags or spawning any workers.
// If handled is true, the caller must immediately os.Exit(code). Explicit
// dispatch permits the embedding application to install supervisor-side lease
// and authenticated IPC hooks; package init cannot provide that integration.
func SupervisorMain(hooks SupervisorHooks) (code int, handled bool) {
	if len(os.Args) < 2 || os.Args[1] != supervisorArgument {
		return 0, false
	}
	if len(os.Args) != 4 {
		return 125, true
	}
	return supervise(os.Args[2], os.Args[3], hooks), true
}

func supervise(dir, endpoint string, hooks SupervisorHooks) int {
	scope := Scope{Version: 1, ID: filepath.Base(dir), Directory: dir}
	var err error
	if scope, err = read(scope); err != nil || scope.Started || scope.Complete {
		return 125
	}
	conn, err := dialControl(endpoint)
	if err != nil {
		return 125
	}
	defer conn.Close()
	streams, err := receiveStreams(conn)
	if err != nil {
		return 125
	}
	backend, err := newBackend(&scope)
	if err != nil {
		return 125
	}
	defer backend.close()
	scope.OwnerPID = os.Getpid()
	slog.Debug("process scope ready", "scope_id", scope.ID, "owner_pid", scope.OwnerPID)
	if save(scope) != nil {
		return 125
	}
	if hooks.Ready != nil {
		if hooks.Ready(scope) != nil {
			return 125
		}
	}
	if json.NewEncoder(conn).Encode(frame{Scope: &scope}) != nil {
		return 125
	}
	var request frame
	if json.NewDecoder(conn).Decode(&request) != nil || request.Command == nil {
		return 125
	}
	cancelled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope.Context = ctx
	go func() { var ignored [1]byte; _, _ = conn.Read(ignored[:]); cancel(); close(cancelled) }()
	signals, stopSignals := terminationSignals()
	defer stopSignals()
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()
	finish := func() int {
		cancel()
		if hooks.Quiesce != nil {
			if hooks.Quiesce(scope) != nil {
				return 125
			}
		}
		scope.Complete = true
		if save(scope) != nil {
			return 125
		}
		if hooks.Drained != nil {
			if hooks.Drained(scope) != nil {
				return 125
			}
		}
		_ = json.NewEncoder(conn).Encode(frame{Scope: &scope})
		return 0
	}
	scope.Started = true
	if save(scope) != nil {
		return 125
	}
	if hooks.BeforeStart != nil {
		if hooks.BeforeStart(scope, request.Command) != nil {
			scope.ExitCode = 125
			return finish()
		}
	}
	if ctx.Err() != nil {
		scope.ExitCode = 125
		return finish()
	}
	if request.Command.Noop {
		scope.ExitCode = 0
		return finish()
	}
	cmd := exec.Command(request.Command.Path)
	cmd.Args = request.Command.Args
	cmd.Dir = request.Command.Dir
	cmd.Env = request.Command.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdin = streams.Stdin
	cmd.Stdout = streams.Stdout
	cmd.Stderr = streams.Stderr
	request = frame{}
	waited, err := backend.start(cmd)
	if err != nil {
		// start is responsible for proving cleanup on partial native launches.
		if errors.Is(err, ErrReconciliation) {
			return 125
		}
		scope.ExitCode = 126
		if errors.Is(err, os.ErrNotExist) {
			scope.ExitCode = 127
		}
		return finish()
	}
	rootDone := false
	select {
	case scope.ExitCode = <-waited:
		rootDone = true
	case <-cancelled:
	case <-ctx.Done():
	}
	slog.Debug("process scope draining", "scope_id", scope.ID, "grace_seconds", 5)
	deadline := time.Now().Add(GracePeriod)
	for {
		if !rootDone {
			select {
			case scope.ExitCode = <-waited:
				rootDone = true
			default:
			}
		}
		empty, err := backend.empty(rootDone)
		if err == nil && rootDone && empty {
			break
		}
		// Each new descendant gets a graceful signal once. Native ownership, not
		// this signalling enumeration, determines when the tree is empty.
		_ = backend.signal(time.Now().After(deadline))
		time.Sleep(20 * time.Millisecond)
	}
	slog.Debug("process scope drained", "scope_id", scope.ID, "exit_code", scope.ExitCode)
	return finish()
}

type nativeBackend interface {
	start(*exec.Cmd) (<-chan int, error)
	signal(bool) error
	empty(bool) (bool, error)
	close()
}

func infrastructureError(detail string) error { return fmt.Errorf("%w: %s", ErrUnavailable, detail) }
