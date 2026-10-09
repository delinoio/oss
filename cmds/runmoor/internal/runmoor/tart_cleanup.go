package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	tartCleanupOwnershipExit      = 64
	tartCleanupPendingExit        = 65
	tartCleanupExecReady     byte = 1
)

type tartCleanupRequest struct {
	// Reuse the trusted Runmoor helper override used by native guest fixtures.
	HelperExecutable string    `json:"-"`
	Data             string    `json:"data"`
	Executable       string    `json:"executable"`
	Source           string    `json:"source"`
	Owner            vmOwner   `json:"owner"`
	Deadline         time.Time `json:"deadline"`
}

// Cleanup executors keep the POSIX lock owner and Tart delete in one PID.
// Files are the verified VM, VM namespace, owner record and Tart home, in order.
type tartCleanupExecutor interface {
	RunTartCleanup(context.Context, tartCleanupRequest, []*os.File) error
}

func tartCleanupFailure() error {
	return problem(ErrCleanup, "Cannot complete locked Tart VM cleanup.", "Confirm no Tart operation is using it, preserve the staged VM and retry; its reservation remains held.")
}
func openTartCleanupFiles(c Config, source, name, installation, entity string) ([]*os.File, error) {
	vm, err := openVerifiedVMOwnerAt(c, source, name, installation, entity)
	if err != nil {
		return nil, err
	}
	files := []*os.File{vm}
	for i, path := range []string{filepath.Join(tartHome(c), "vms"), vmOwnerPath(c, name), tartHome(c)} {
		var f *os.File
		if i == 1 {
			f, err = openPrivate(path, os.O_RDONLY)
		} else {
			f, err = openTartVMDirectory(path)
		}
		if err != nil {
			closeTartCleanupFiles(files)
			return nil, ambiguousVMOwnership()
		}
		files = append(files, f)
	}
	return files, nil
}
func closeTartCleanupFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
func (OSCommand) RunTartCleanup(ctx context.Context, req tartCleanupRequest, files []*os.File) error {
	binary := req.HelperExecutable
	var err error
	if binary == "" {
		binary, err = os.Executable()
	}
	if err != nil {
		return tartCleanupFailure()
	}
	req.Executable, err = exec.LookPath(req.Executable)
	if err != nil {
		return tartCleanupFailure()
	}
	req.Executable, err = filepath.Abs(req.Executable)
	if err != nil {
		return tartCleanupFailure()
	}
	return runTartCleanupCommand(ctx, binary, []string{"__tart-cleanup"}, minimalEnv(), req, files)
}
func runTartCleanupCommand(ctx context.Context, binary string, args, env []string, req tartCleanupRequest, files []*os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return tartCleanupFailure()
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return tartCleanupFailure()
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(body)
	cmd.ExtraFiles = append(append([]*os.File(nil), files...), readyWrite)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return tartCleanupFailure()
	}
	readyWrite.Close()
	err = cmd.Wait()
	ready, readyErr := io.ReadAll(io.LimitReader(readyRead, 2))
	executed := readyErr == nil && len(ready) == 1 && ready[0] == tartCleanupExecReady
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exited *exec.ExitError
		if errors.As(err, &exited) && !executed && exited.ExitCode() == tartCleanupOwnershipExit {
			return ambiguousVMOwnership()
		}
		return tartCleanupFailure()
	}
	if !executed {
		return tartCleanupFailure()
	}
	return nil
}
func (r tartCleanupRequest) config() Config { var c Config; c.Storage.Data = r.Data; return c }
func (r tartCleanupRequest) cancelled() bool {
	return !r.Deadline.IsZero() && !time.Now().Before(r.Deadline)
}
