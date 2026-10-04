package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Process identities are compared with kernel start times. They stay private;
// neither command arguments nor process names establish execution ownership.
type HostProcess struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
	Group int    `json:"group"`
}
type HostExecution struct {
	Supervisor    HostProcess `json:"supervisor"`
	Worker        HostProcess `json:"worker"`
	LaunchPending bool        `json:"launch_pending"`
	Terminated    bool        `json:"terminated"`
	Cleaned       bool        `json:"cleaned"`
}
type HostExecutionPhase string

const (
	HostStarting HostExecutionPhase = "starting"
	HostRunning  HostExecutionPhase = "running"
	HostFinished HostExecutionPhase = "finished"
	HostFailed   HostExecutionPhase = "failed"
)

type HostExecutionStatus struct {
	ID         string             `json:"id"`
	Token      string             `json:"token"`
	Supervisor HostProcess        `json:"supervisor"`
	Worker     HostProcess        `json:"worker"`
	Phase      HostExecutionPhase `json:"phase"`
	ExitCode   int                `json:"exit_code"`
}
type HostBootstrap struct {
	Directory HostDirectory `json:"directory"`
	Storage   Storage       `json:"storage"`
	JIT       string        `json:"jit"`
	Deadline  time.Time     `json:"deadline"`
}

// All native execution is injectable. Ordinary tests never launch host jobs,
// Docker, Tart, Guest Agent, or GitHub requests.
type HostNative interface {
	Platform(context.Context) error
	Version(context.Context, *os.Root, string) error
	Launch(context.Context, *os.Root, HostBootstrap, func(HostProcess) error) error
	Alive(HostProcess) (bool, error)
	Group(HostProcess) ([]HostProcess, error)
	Stop(HostProcess) error
}
type HostDriver struct {
	Store  *Store
	Native HostNative
}

func (h *HostDriver) native() HostNative {
	if h.Native == nil {
		return nativeHost{}
	}
	return h.Native
}
func (h *HostDriver) Validate(ctx context.Context, c Config, p Pool, s Snapshot) error {
	if err := h.native().Platform(ctx); err != nil {
		return err
	}
	d := s.HostDirectories[p.Image]
	if d == nil || d.Kind != HostDistribution || d.Version != p.RunnerVersion || d.Digest == "" {
		return problem(ErrImage, "Host runner distribution is not verified.", "Start the manager or request a runner update; existing executions retain their original distribution.")
	}
	root, err := openHostDirectory(c, *d, s.Installation, false)
	if err != nil {
		return err
	}
	defer root.Close()
	digest, err := hostRunnerDigest(ctx, root)
	if err != nil {
		return err
	}
	if digest != d.Digest {
		return problem(ErrImage, "Host runner distribution changed after verification.", "Request a runner update; preserve the changed distribution for diagnosis.")
	}
	return nil
}
func (h *HostDriver) Prepare(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := diskCheck(c); err != nil {
		return err
	}
	if !validHostJIT(jit) {
		return problem(ErrPreparation, "Private host bootstrap is invalid.", "Retry registration with a fresh single-job runner.")
	}
	if err := h.Validate(ctx, c, p, s); err != nil {
		return err
	}
	distribution := s.HostDirectories[p.Image]
	source, err := openHostDirectory(c, *distribution, s.Installation, false)
	if err != nil {
		return err
	}
	defer source.Close()
	d, root, err := createHostDirectory(ctx, h.Store, c, r.ID, HostWorkspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = copyHostRunner(ctx, source, root); err != nil {
		return err
	}
	// Revalidate the source after copying, before registration data can reach it.
	digest, err := hostRunnerDigest(ctx, root)
	if err != nil || digest != distribution.Digest {
		return problem(ErrImage, "Host installation did not match its verified distribution.", "Retry with a new execution after cleanup.")
	}
	for _, path := range []string{"home", "tmp", "work"} {
		if err = root.Mkdir(path, 0700); err != nil {
			return hostOwnership()
		}
	}
	if err = root.Symlink("../work", "runner/_work"); err != nil {
		return hostOwnership()
	}
	if err = publish(r.Handle); err != nil {
		return err
	}
	// Journal ambiguous launch intent before spawn. Both manager and supervisor
	// recheck the durable unforced Preparing phase immediately before launch.
	if err = h.Store.Update(func(v *Snapshot) error {
		current := v.Runners[r.ID]
		if ctx.Err() != nil || current == nil || current.Forced || current.Phase != Preparing {
			return staleUpdate()
		}
		if v.HostExecutions == nil {
			v.HostExecutions = map[string]*HostExecution{}
		}
		if v.HostExecutions[r.ID] != nil {
			return hostOwnership()
		}
		v.HostExecutions[r.ID] = &HostExecution{LaunchPending: true}
		return nil
	}); err != nil {
		return err
	}
	bootstrap := HostBootstrap{Directory: d, Storage: c.Storage, JIT: jit, Deadline: r.CreatedAt.Add(c.JobTimeout())}
	err = h.native().Launch(ctx, root, bootstrap, func(process HostProcess) error {
		return h.Store.Update(func(v *Snapshot) error {
			e := v.HostExecutions[r.ID]
			if e == nil {
				return hostOwnership()
			}
			e.Supervisor = process
			current := v.Runners[r.ID]
			if ctx.Err() != nil || current == nil || current.Forced || current.Phase != Preparing {
				return staleUpdate()
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		obs, status, err := h.observe(ctx, c, r, h.Store.View())
		if err != nil {
			var pending *Problem
			if !errors.As(err, &pending) || pending.Code != ErrCleanup {
				return err
			}
			// Detached status publication is asynchronous. Uncertain observations
			// remain reserved and retry only within the preparation context.
			if !waitContext(ctx, 50*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}
		if status.Phase == HostRunning && obs.Running {
			return h.Store.Update(func(v *Snapshot) error {
				e := v.HostExecutions[r.ID]
				if e == nil {
					return hostOwnership()
				}
				e.Worker = status.Worker
				e.LaunchPending = false
				return nil
			})
		}
		if status.Phase == HostFailed || status.Phase == HostFinished || !obs.Running {
			return problem(ErrPreparation, "Host runner exited before startup was confirmed.", "Check installed tools and runner compatibility, then resume the pool.")
		}
		if !waitContext(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}
func hostReadStatus(root *os.Root, d HostDirectory) (HostExecutionStatus, error) {
	var status HostExecutionStatus
	b, err := hostRootRead(root, "status.json", 8192)
	if err != nil || json.Unmarshal(b, &status) != nil || status.ID != d.ID || status.Token != d.Token {
		return status, hostOwnership()
	}
	switch status.Phase {
	case HostStarting, HostRunning, HostFinished, HostFailed:
	default:
		return status, hostOwnership()
	}
	return status, nil
}
func sameHostProcess(a, b HostProcess) bool { return a.PID > 0 && a.Start != "" && a == b }
func (h *HostDriver) observe(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, HostExecutionStatus, error) {
	obs := Observation{Handle: r.Handle}
	var status HostExecutionStatus
	if ctx.Err() != nil {
		return obs, status, ctx.Err()
	}
	d := s.HostDirectories[r.ID]
	execution := s.HostExecutions[r.ID]
	if execution != nil && execution.Cleaned {
		return obs, status, nil
	}
	if d == nil {
		if execution == nil {
			return obs, status, nil
		}
		return obs, status, hostOwnership()
	}
	root, err := openHostDirectory(c, *d, s.Installation, false)
	if err != nil {
		// Interrupted staged removal is safe to recover only after durable termination.
		if execution != nil && execution.Terminated {
			root, err = openHostDirectory(c, *d, s.Installation, true)
			if err == nil {
				root.Close()
				return obs, status, nil
			}
		}
		return obs, status, err
	}
	defer root.Close()
	obs.Exists = true
	if execution == nil {
		return obs, status, nil
	}
	status, err = hostReadStatus(root, *d)
	if err != nil {
		return obs, status, hostPending()
	}
	if execution.Supervisor.PID > 0 && !sameHostProcess(execution.Supervisor, status.Supervisor) {
		return obs, status, hostOwnership()
	}
	alive, err := h.native().Alive(status.Supervisor)
	if err != nil {
		return obs, status, hostPending()
	}
	if status.Worker.PID > 0 {
		if execution.Worker.PID > 0 && !sameHostProcess(execution.Worker, status.Worker) {
			return obs, status, hostOwnership()
		}
		members, e := h.native().Group(status.Worker)
		if e != nil {
			return obs, status, hostPending()
		}
		obs.Running = alive || len(members) > 0
	} else {
		obs.Running = alive
	}
	if !alive && status.Phase == HostStarting && status.Worker.PID == 0 {
		return obs, status, hostPending()
	}
	if !alive && (status.Phase == HostStarting || status.Phase == HostRunning) && obs.Running {
		return obs, status, hostPending()
	}
	if !obs.Running {
		code := status.ExitCode
		obs.ExitCode = &code
	}
	return obs, status, nil
}
func (h *HostDriver) Inspect(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, error) {
	obs, _, err := h.observe(ctx, c, r, s)
	return obs, err
}
func (h *HostDriver) Stop(ctx context.Context, c Config, r Runner, s Snapshot) error {
	execution := s.HostExecutions[r.ID]
	if execution == nil {
		return hostUnlaunched(c, r, s)
	}
	if execution.Terminated {
		return nil
	}
	for {
		obs, status, err := h.observe(ctx, c, r, s)
		if err != nil {
			return err
		}
		if !obs.Running {
			if status.Phase == "" {
				return hostPending()
			}
			if h.Store == nil {
				return hostPending()
			}
			return h.Store.Update(func(v *Snapshot) error {
				v.HostExecutions[r.ID].Terminated = true
				v.HostExecutions[r.ID].Worker = status.Worker
				return nil
			})
		}
		alive, err := h.native().Alive(status.Supervisor)
		if err != nil || !alive {
			return hostPending()
		}
		// Signal the exact verified supervisor, which owns its runner group. Never
		// signal an orphaned numeric group from a missing/reused supervisor identity.
		if err = h.native().Stop(status.Supervisor); err != nil {
			return hostPending()
		}
		if !waitContext(ctx, 50*time.Millisecond) {
			return hostPending()
		}
	}
}
func (h *HostDriver) Cleanup(ctx context.Context, c Config, r Runner, s Snapshot) error {
	if h.Store != nil {
		s = h.Store.View()
	}
	execution := s.HostExecutions[r.ID]
	if execution != nil && execution.Cleaned {
		return nil
	}
	if execution == nil {
		if err := hostUnlaunched(c, r, s); err != nil {
			return err
		}
	}
	if execution != nil && !execution.Terminated {
		return hostPending()
	}
	d := s.HostDirectories[r.ID]
	if d == nil {
		return nil
	}
	if err := removeHostDirectory(ctx, h.Store, c, *d); err != nil {
		return err
	}
	return h.Store.Update(func(v *Snapshot) error {
		delete(v.HostDirectories, r.ID)
		if e := v.HostExecutions[r.ID]; e != nil {
			e.Cleaned = true
		}
		return nil
	})
}
func (b *ManagedImageBuilder) prepareHost(ctx context.Context, c Config, p Pool, a RunnerArtifact, release RunnerRelease) (Pool, error) {
	native := b.Host
	if native == nil {
		native = nativeHost{}
	}
	if err := native.Platform(ctx); err != nil {
		return p, err
	}
	_, root, err := createHostDirectory(ctx, b.Store, c, a.ID, HostDistribution)
	if err != nil {
		return p, err
	}
	defer root.Close()
	asset, err := runnerArchiveAsset(release, Host, p.Arch)
	if err != nil {
		return p, err
	}
	if err = root.Mkdir("download", 0700); err != nil {
		return p, hostOwnership()
	}
	archive, err := root.OpenFile("download/runner.tar.gz", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return p, hostOwnership()
	}
	defer archive.Close()
	b.log("runner_archive_downloading", a)
	if err = writeRunnerArchive(ctx, b.Client, asset, archive); err != nil {
		return p, err
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return p, err
	}
	validated, err := root.OpenFile("download/runner.tar", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return p, hostOwnership()
	}
	defer validated.Close()
	if err = repackRunnerTar(ctx, archive, validated); err != nil {
		return p, err
	}
	b.log("runner_archive_verified", a)
	if _, err = validated.Seek(0, 0); err != nil {
		return p, err
	}
	if err = extractHostRunner(ctx, validated, root); err != nil {
		return p, err
	}
	for _, name := range []string{"home", "tmp"} {
		if err = root.Mkdir(name, 0700); err != nil {
			return p, hostOwnership()
		}
	}
	for _, name := range []string{".runner", ".credentials", ".credentials_rsaparams", "_work"} {
		if _, e := root.Lstat(filepath.Join("runner", name)); !os.IsNotExist(e) {
			return p, archiveProblem()
		}
	}
	if err = native.Version(ctx, root, release.Version()); err != nil {
		return p, err
	}
	if err = root.RemoveAll("download"); err != nil {
		return p, hostOwnership()
	}
	if err = root.RemoveAll("runner/_diag"); err != nil {
		return p, hostOwnership()
	}
	digest, err := hostRunnerDigest(ctx, root)
	if err != nil {
		return p, err
	}
	if err = b.Store.Update(func(v *Snapshot) error {
		record := v.HostDirectories[a.ID]
		if record == nil {
			return staleUpdate()
		}
		record.Digest = digest
		record.Version = release.Version()
		v.Artifacts[a.ID].Image = a.ID
		v.Artifacts[a.ID].Generated = true
		return nil
	}); err != nil {
		return p, err
	}
	p.Image = a.ID
	p.RunnerVersion = release.Version()
	p.ImageSource = nil
	b.log("host_distribution_verified", a)
	return p, nil
}
func (b *ManagedImageBuilder) cleanupHost(ctx context.Context, c Config, a RunnerArtifact) error {
	s := b.Store.View()
	if d := s.HostDirectories[a.ID]; d != nil {
		if err := removeHostDirectory(ctx, b.Store, c, *d); err != nil {
			return err
		}
		if err := b.Store.Update(func(v *Snapshot) error { delete(v.HostDirectories, a.ID); return nil }); err != nil {
			return err
		}
	}
	return nil
}

// Absence of the launch journal is authoritative only before any supervisor
// publication. A status file without its durable record is lost ownership.
func hostUnlaunched(c Config, r Runner, s Snapshot) error {
	d := s.HostDirectories[r.ID]
	if d == nil {
		return nil
	}
	root, err := openHostDirectory(c, *d, s.Installation, false)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err = root.Lstat("status.json"); !os.IsNotExist(err) {
		return hostOwnership()
	}
	return nil
}
