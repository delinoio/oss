// Package process owns native child lifecycles independently of harness
// protocols. Journals prove execution ownership, never persist command content.
package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type Process struct {
	ScopeDir string    `json:"scope_dir"`
	OwnerID  domain.ID `json:"owner_id"`
	PID      int       `json:"pid"`
	Birth    string    `json:"birth"`
	Group    int       `json:"group,omitempty"`
}
type Config struct {
	Directory  string
	OwnerID    domain.ID
	Executable string
	Args       []string
	Env        []string
	Cwd        string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *slog.Logger
}
type Handle struct {
	native  *managedProcess
	mu      sync.Mutex
	resumed bool
	done    chan struct{}
	result  error
	logger  *slog.Logger
}
type commandExitError int

func (e commandExitError) Error() string {
	return fmt.Sprintf("owned process exited with status %d", int(e))
}
func (e commandExitError) ExitCode() int { return int(e) }
func ownershipError() error {
	return domain.Fail(domain.RecoveryRequired, "Owned process descendants could not be confirmed stopped.", "Retain the execution journal and reconcile its native ownership before retrying or releasing resources.")
}
func Start(ctx context.Context, config Config) (*Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := config.OwnerID.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(config.Executable) || !filepath.IsAbs(config.Cwd) || config.Env == nil {
		return nil, domain.Fail(domain.InvalidArgument, "A process requires an absolute executable, working directory and explicit environment.", "Resolve the selected Worker's native executable and isolated runtime before launch.")
	}
	// Only bound this transient value; it is sent through private IPC and never
	// written to the ownership journal or diagnostics.
	raw, err := json.Marshal(struct {
		Path      string
		Args, Env []string
		Dir       string
	}{config.Executable, config.Args, config.Env, config.Cwd})
	if err != nil || len(raw) > 128<<10 {
		return nil, domain.Fail(domain.ResourceExhausted, "Native launch parameters exceed their bound.", "Send streamed input separately from native launch configuration.")
	}
	if err := security.PrivateDir(config.Directory); err != nil {
		return nil, err
	}
	directory, err := filepath.EvalSymlinks(config.Directory)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	directory = filepath.Join(directory, string(config.OwnerID))
	if err := security.PrivateDir(directory); err != nil {
		return nil, err
	}
	scope := filepath.Join(directory, string(domain.NewID()))
	command := exec.Command(config.Executable, config.Args...)
	command.Env = config.Env
	command.Dir = config.Cwd
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	logger = logger.With("owner_id", config.OwnerID, "process_scope_id", filepath.Base(scope))
	p, err := startProcess(command, scope, config.OwnerID)
	if err != nil {
		return nil, err
	}
	h := &Handle{native: p, done: make(chan struct{}), logger: logger}
	logger.InfoContext(ctx, "native process prepared", "pid", p.snapshot().PID)
	go func() {
		h.result = p.wait()
		if h.result != nil {
			logger.Warn("native process exited", "code", domain.SafeError(h.result).Code)
		} else {
			logger.Info("native process exited")
		}
		close(h.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = h.Stop()
		case <-h.done:
		}
	}()
	return h, nil
}
func (h *Handle) Identity() Process { return h.native.snapshot() }
func (h *Handle) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.resumed {
		return domain.Fail(domain.Conflict, "The native start barrier was already released.", "Continue the existing execution instead of restarting it.")
	}
	select {
	case <-h.done:
		return ownershipError()
	default:
	}
	if err := h.native.resume(); err != nil {
		return err
	}
	h.resumed = true
	h.logger.Info("native process resumed")
	return nil
}
func (h *Handle) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.resumed {
		return 0, domain.Fail(domain.Conflict, "The native process has not crossed its start barrier.", "Persist dispatch ownership before resuming execution.")
	}
	return h.native.write(p)
}
func (h *Handle) CloseInput() error     { return h.native.closeInput() }
func (h *Handle) Wait() error           { <-h.done; return h.result }
func (h *Handle) Done() <-chan struct{} { return h.done }
func (h *Handle) Stop() error           { return h.native.terminate() }
func (h *Handle) Close() error          { err := h.Stop(); h.native.close(); return err }
func Run(ctx context.Context, config Config) error {
	h, err := Start(ctx, config)
	if err != nil {
		return err
	}
	if err = h.Resume(); err == nil {
		err = h.CloseInput()
	}
	if err == nil {
		err = h.Wait()
	}
	// Cleanup proof has precedence over cancellation or command status. Callers
	// must not remove a workspace while an uncertain descendant may still write.
	stopped := h.Close()
	if stopped != nil {
		return ownershipError()
	}
	if ctx.Err() != nil {
		return errors.Join(domain.SafeError(ctx.Err()), err)
	}
	return err
}

// ReconcileOwner uses the owner's dedicated index, never scans other sessions'
// process history and never interprets a missing index as proof of completion.
func ReconcileOwner(root string, owner domain.ID) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	directory := filepath.Join(root, string(owner))
	if err := security.CheckPrivateDir(directory); err != nil {
		return ownershipError()
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ownershipError()
	}
	if len(entries) > 10000 {
		return domain.Fail(domain.ResourceExhausted, "Process recovery exceeds its bounded owner scope.", "Inspect and prune confirmed completed ownership journals before retrying.")
	}
	for _, entry := range entries {
		if !entry.IsDir() || domain.ID(entry.Name()).Validate() != nil {
			return ownershipError()
		}
		path := filepath.Join(directory, entry.Name())
		if err := security.CheckPrivateDir(path); err != nil {
			return ownershipError()
		}
		raw, err := security.ReadPrivate(filepath.Join(path, "ownership.json"), 1<<20)
		if err != nil {
			return ownershipError()
		}
		// This superset contains metadata only and supports each native journal.
		var scope struct {
			Version   int       `json:"version"`
			OwnerID   domain.ID `json:"owner_id"`
			Owner     Process   `json:"owner"`
			Boot      string    `json:"boot,omitempty"`
			Coalition uint64    `json:"coalition,omitempty"`
			Label     string    `json:"label,omitempty"`
			Domain    string    `json:"domain,omitempty"`
			Job       string    `json:"job,omitempty"`
			Started   bool      `json:"started"`
			Complete  bool      `json:"complete"`
		}
		if err := domain.Decode(raw, &scope); err != nil || scope.Version != 1 || scope.OwnerID != owner {
			return ownershipError()
		}
		identity := scope.Owner
		identity.ScopeDir = path
		identity.OwnerID = owner
		if err := ReconcileProcess(identity); err != nil {
			return err
		}
	}
	return nil
}
