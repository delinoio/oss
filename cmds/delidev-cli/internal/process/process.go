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
	"strings"
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
	native     *managedProcess
	mu         sync.Mutex
	resumed    bool
	done       chan struct{}
	result     error
	logger     *slog.Logger
	controller *security.Lock
	closeOnce  sync.Once
	closeErr   error
}
type commandExitError int

func (e commandExitError) Error() string {
	return fmt.Sprintf("owned process exited with status %d", int(e))
}
func (e commandExitError) ExitCode() int { return int(e) }
func ownershipError() error {
	return domain.Fail(domain.RecoveryRequired, "Owned process descendants could not be confirmed stopped.", "Retain the execution journal and reconcile its native ownership before retrying or releasing resources.")
}

func launchFailure(code domain.Code) *domain.Error {
	switch code {
	case domain.NotFound:
		return domain.Fail(code, "The selected executable or its interpreter is missing.", "Check the selected Worker's native installation and refresh discovery.")
	case domain.PermissionDenied:
		return domain.Fail(code, "The operating system refused to launch the executable.", "Check file permissions and native execution policy on the selected Worker.")
	case domain.Unsupported:
		return domain.Fail(code, "The operating system cannot load this executable format.", "Select a native executable for this Worker's operating system and architecture.")
	default:
		return domain.Fail(domain.Unavailable, "The native executable could not be started.", "Inspect the selected Worker's installation and native resource availability.")
	}
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
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	logger = logger.With("owner_id", config.OwnerID, "process_scope_id", filepath.Base(scope))
	controller, err := prepareController(scope, security.TryLock, security.SyncParent)
	if err != nil {
		logger.WarnContext(ctx, "native process controller preparation failed", "code", domain.SafeError(err).Code)
		return nil, err
	}
	command := exec.Command(config.Executable, config.Args...)
	command.Env = config.Env
	command.Dir = config.Cwd
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	p, err := startProcess(command, scope, config.OwnerID)
	if err != nil {
		_ = controller.Close()
		return nil, err
	}
	h := &Handle{native: p, done: make(chan struct{}), logger: logger, controller: controller}
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

// The parent is already private, including its inheritable Windows ACL. Create
// this final component exclusively so rollback never adopts an existing scope.
func prepareController(scope string, lock func(string) (*security.Lock, error), syncParent func(string) error) (*security.Lock, error) {
	if err := os.Mkdir(scope, 0700); err != nil {
		return nil, err
	}
	// Stat an open handle to capture Windows file identity immediately. Lstat
	// may defer identity lookup until SameFile, after the path was replaced.
	file, err := os.Open(scope)
	var original os.FileInfo
	if err == nil {
		original, err = file.Stat()
		err = errors.Join(err, file.Close())
	}
	if err == nil {
		err = security.CheckPrivateDir(scope)
	}
	if err == nil {
		var controller *security.Lock
		controller, err = lock(filepath.Join(scope, "controller.lock"))
		if err == nil {
			return controller, nil
		}
	}
	// Native startup has not been attempted. Remove only an empty directory;
	// a partial lock, journal or any unexpected evidence must remain intact.
	current, statErr := os.Lstat(scope)
	if statErr == nil && original != nil && current.IsDir() && current.Mode()&os.ModeSymlink == 0 && os.SameFile(original, current) {
		if cleanupErr := os.Remove(scope); cleanupErr == nil {
			if syncParent(scope) == nil {
				return nil, err
			}
		}
	}
	return nil, domain.Fail(domain.RecoveryRequired, "The unlaunched process scope could not be removed durably.", "Preserve the process owner index and inspect its retained evidence before retrying.")
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
func (h *Handle) Close() error {
	h.closeOnce.Do(func() {
		h.closeErr = h.Stop()
		h.native.close()
		h.closeErr = errors.Join(h.closeErr, h.controller.Close())
	})
	return h.closeErr
}
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
	return ReconcileOwnerContext(context.Background(), root, owner)
}

// Cancellation interrupts between bounded native ownership checks; an in-flight
// termination still finishes its confirmation before yielding.
func ReconcileOwnerContext(ctx context.Context, root string, owner domain.ID) error {
	return reconcileOwnerContext(ctx, root, owner, 10000)
}

func reconcileOwnerContext(ctx context.Context, root string, owner domain.ID, limit int) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	directory := filepath.Join(root, string(owner))
	if err := security.CheckPrivateDir(directory); err != nil {
		return ownershipError()
	}
	// Serialize retirement independently of native handle ownership. The lock
	// lives outside the owner index so a retained empty index stays meaningful.
	maintenance, err := security.TryLock(filepath.Join(root, string(owner)+".recovery.lock"))
	if err != nil {
		return err
	}
	defer maintenance.Close()
	index, err := os.Open(directory)
	if err != nil {
		return ownershipError()
	}
	defer index.Close()
	var retained []Process
	for {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		entries, err := index.ReadDir(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return ownershipError()
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return domain.SafeError(err)
			}
			name := entry.Name()
			retired := strings.HasPrefix(name, ".retired-")
			if !entry.IsDir() || domain.ID(strings.TrimPrefix(name, ".retired-")).Validate() != nil {
				return ownershipError()
			}
			path := filepath.Join(directory, name)
			if err := security.CheckPrivateDir(path); err != nil {
				return ownershipError()
			}
			if retired {
				// This name is published only after completed ownership and released
				// controller authority are verified. Retry interrupted removal without
				// interpreting a partially removed journal as new completion proof.
				if err := os.RemoveAll(path); err != nil {
					return ownershipError()
				}
				if err := security.SyncParent(path); err != nil {
					return ownershipError()
				}
				continue
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
			if scope.Complete {
				controller, err := security.TryLock(filepath.Join(path, "controller.lock"))
				if err == nil {
					// Reuse the platform reader to validate this completion, never PID
					// absence. A live Handle retains the journal until it has closed.
					checked := ReconcileProcess(identity)
					closed := controller.Close()
					if checked != nil || closed != nil {
						return ownershipError()
					}
					retiredPath := filepath.Join(directory, ".retired-"+name)
					if err := os.Rename(path, retiredPath); err != nil {
						return ownershipError()
					}
					if err := security.SyncParent(retiredPath); err != nil {
						return ownershipError()
					}
					if err := os.RemoveAll(retiredPath); err != nil {
						return ownershipError()
					}
					if err := security.SyncParent(retiredPath); err != nil {
						return ownershipError()
					}
					continue
				}
				if domain.SafeError(err).Code != domain.Conflict {
					return ownershipError()
				}
			}
			retained = append(retained, identity)
			if len(retained) > limit {
				return domain.Fail(domain.ResourceExhausted, "Process recovery exceeds its bounded unresolved owner scope.", "Inspect the retained ownership journals before retrying.")
			}
		}
	}
	for _, identity := range retained {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if err := ReconcileProcess(identity); err != nil {
			return err
		}
	}
	return nil
}
