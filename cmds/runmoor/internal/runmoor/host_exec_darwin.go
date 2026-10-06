//go:build darwin

package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type hostExecMode string

const (
	hostExecVersion hostExecMode = "version"
	hostExecWorker  hostExecMode = "worker"
)

type hostExecFailure byte

const (
	hostExecOK hostExecFailure = iota
	hostExecOwnership
	hostExecBootstrap
	hostExecStale
	hostExecDirectory
	hostExecRun
	hostExecSpawn
)

func (f hostExecFailure) String() string {
	switch f {
	case hostExecOwnership:
		return "ownership"
	case hostExecBootstrap:
		return "bootstrap"
	case hostExecStale:
		return "stale"
	case hostExecDirectory:
		return "directory"
	case hostExecRun:
		return "execution"
	case hostExecSpawn:
		return "spawn"
	default:
		return "unknown"
	}
}

func hostExecProblem(reason hostExecFailure) error {
	if reason == hostExecOwnership {
		return hostOwnership()
	}
	if reason == hostExecStale {
		return staleUpdate()
	}
	return problem(ErrPreparation, "Host runner could not be executed ("+reason.String()+").", "Check the Runmoor executable, runner installation and host execution permissions, then retry the managed runner update. This failure does not establish a runner version mismatch.")
}

func hostExecutable(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return os.Executable()
}

func hostDirectoryMarker(root *os.Root) (HostDirectory, error) {
	var d HostDirectory
	body, err := hostRootRead(root, hostOwnerFile, 4096)
	if err != nil || json.Unmarshal(body, &d) != nil || !validHostDirectory(d, d.Installation) || d.RemovalCommitted {
		return d, hostOwnership()
	}
	info, err := root.Stat(".")
	if err != nil || !hostPrivateInfo(info, true) || hostFileIdentity(info) != d.Identity {
		return d, hostOwnership()
	}
	return d, nil
}

// Darwin's /dev/fd entries can duplicate a descriptor, but cannot serve as
// directory traversal or chdir paths. Only private child entrypoints call this
// function: fchdir must never change the manager's process-wide cwd. Retain this
// helper until Go supports a descriptor-based child working directory on Darwin.
func hostInheritedRoot() (*os.Root, HostDirectory, error) {
	var d HostDirectory
	inherited := os.NewFile(3, "host-directory")
	if inherited == nil {
		return nil, d, hostOwnership()
	}
	defer inherited.Close()
	info, err := inherited.Stat()
	if err != nil || !hostPrivateInfo(info, true) || unix.Fchdir(3) != nil {
		return nil, d, hostOwnership()
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, d, hostOwnership()
	}
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(info, current) {
		root.Close()
		return nil, d, hostOwnership()
	}
	d, err = hostDirectoryMarker(root)
	if err != nil {
		root.Close()
		return nil, d, err
	}
	return root, d, nil
}

func hostRunnerCommand(ctx context.Context, executable string, mode hostExecMode, root *os.Root, in HostBootstrap) (*exec.Cmd, *os.File, func(), error) {
	exe, err := hostExecutable(executable)
	if err != nil {
		return nil, nil, nil, err
	}
	dir, err := root.Open(".")
	if err != nil {
		return nil, nil, nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		dir.Close()
		return nil, nil, nil, err
	}
	closeFiles := func() { dir.Close(); read.Close(); write.Close() }
	body, err := json.Marshal(in)
	if err != nil {
		closeFiles()
		return nil, nil, nil, err
	}
	cmd := exec.CommandContext(ctx, exe, "__host-exec", string(mode))
	cmd.ExtraFiles = []*os.File{dir, write}
	// JIT stays on an anonymous pipe, including between supervisor and exec
	// helper. The helper replaces itself, preserving PID/start/group identity.
	cmd.Stdin = bytes.NewReader(body)
	return cmd, read, closeFiles, nil
}

func closeHostChildFiles(cmd *exec.Cmd) {
	for _, file := range cmd.ExtraFiles {
		_ = file.Close()
	}
}

func hostReadExecOutcome(file *os.File) hostExecFailure {
	body, err := io.ReadAll(io.LimitReader(file, 2))
	if err != nil || len(body) > 1 {
		return hostExecBootstrap
	}
	if len(body) == 0 {
		return hostExecOK
	}
	reason := hostExecFailure(body[0])
	if reason < hostExecOwnership || reason > hostExecSpawn {
		return hostExecBootstrap
	}
	return reason
}

func hostWorkerAdmission(ctx context.Context, in HostBootstrap) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s, err := ReadSnapshot(Config{Storage: in.Storage})
	if err != nil {
		return err
	}
	r := s.Runners[in.Directory.ID]
	if r == nil || r.Forced || r.Phase != Preparing || s.HostExecutions[r.ID] == nil || s.Installation != in.Directory.Installation || s.HostDirectories[r.ID] == nil || *s.HostDirectories[r.ID] != in.Directory {
		return staleUpdate()
	}
	return nil
}

func hostExecute(action string) int {
	mode := hostExecMode(action)
	if mode != hostExecVersion && mode != hostExecWorker {
		return 2
	}
	outcome := os.NewFile(4, "host-exec-outcome")
	if outcome == nil {
		return 1
	}
	defer outcome.Close()
	info, err := outcome.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 1
	}
	unix.CloseOnExec(4)
	fail := func(reason hostExecFailure) int {
		_, _ = outcome.Write([]byte{byte(reason)})
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	root, d, err := hostInheritedRoot()
	if err != nil {
		return fail(hostExecOwnership)
	}
	defer root.Close()
	// Resolve HOME/temp from the descriptor-selected cwd, not from the original
	// name that may have been renamed or replaced while the handle stayed open.
	physicalRoot, err := os.Getwd()
	if err != nil {
		return fail(hostExecDirectory)
	}
	env := hostEnvironment(physicalRoot)
	body, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+8193))
	var in HostBootstrap
	if err != nil || len(body) > (1<<20)+8192 || json.Unmarshal(body, &in) != nil || in.Directory != d {
		return fail(hostExecBootstrap)
	}
	path := "./bin/Runner.Listener"
	args := []string{path, "--version"}
	if mode == hostExecVersion {
		if d.Kind != HostDistribution || in.JIT != "" {
			return fail(hostExecBootstrap)
		}
	} else {
		if d.Kind != HostWorkspace || !validHostJIT(in.JIT) || in.Deadline.IsZero() || time.Until(in.Deadline) > 7*24*time.Hour || !time.Now().Before(in.Deadline) {
			return fail(hostExecBootstrap)
		}
		path = "./run.sh"
		args = []string{path, "--jitconfig", in.JIT}
	}
	// O_NOFOLLOW rejects a swapped runner-directory symlink at the actual open,
	// rather than relying only on an earlier path inspection.
	parent, err := root.Open(".")
	if err != nil {
		return fail(hostExecOwnership)
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), "runner", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(hostExecOwnership)
	}
	runner := os.NewFile(uintptr(fd), "host-runner-directory")
	defer runner.Close()
	info, err = runner.Stat()
	if err != nil || !hostPrivateInfo(info, true) {
		return fail(hostExecOwnership)
	}
	if unix.Fchdir(fd) != nil {
		return fail(hostExecDirectory)
	}
	selected, err := os.OpenRoot(".")
	if err != nil {
		return fail(hostExecOwnership)
	}
	defer selected.Close()
	if mode == hostExecVersion {
		bin, err := selected.Lstat("bin")
		if err != nil || !bin.IsDir() || bin.Mode()&os.ModeSymlink != 0 {
			return fail(hostExecOwnership)
		}
	}
	// Fixed entrypoints cannot be links to arbitrary account files. Imported
	// dependency symlinks remain supported inside the verified runner archive.
	target, err := selected.Lstat(path)
	if err == nil && (!target.Mode().IsRegular() || target.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid())) {
		return fail(hostExecOwnership)
	}
	if err != nil {
		return fail(hostExecRun)
	}
	if mode == hostExecWorker {
		// Recheck Stop/force and generation authority inside the child, after
		// opening its directory and immediately before executing any runner code.
		if hostWorkerAdmission(ctx, in) != nil {
			return fail(hostExecStale)
		}
	}
	if ctx.Err() != nil {
		return fail(hostExecStale)
	}
	if err := syscall.Exec(path, args, env); err != nil {
		return fail(hostExecRun)
	}
	return 1
}
