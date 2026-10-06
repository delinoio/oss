package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/mod/semver"
)

type serviceReloadStage string

const (
	reloadValidating     serviceReloadStage = "validating"
	reloadPrepared       serviceReloadStage = "prepared"
	reloadPublished      serviceReloadStage = "published"
	reloadRestartPending serviceReloadStage = "restart_pending"
	reloadHandoffPending serviceReloadStage = "handoff_pending"
	reloadUnloaded       serviceReloadStage = "unloaded"
	reloadStarting       serviceReloadStage = "starting"
	reloadRunning        serviceReloadStage = "running"
)

const reloadJournalLimit = 256 << 10

// This journal contains references and native identities only. It neither
// backs up state nor grants authority to stop a runner or downgrade a database.
type serviceReloadJournal struct {
	Schema          int                `json:"schema"`
	Token           string             `json:"token"`
	Installation    string             `json:"installation"`
	Storage         Storage            `json:"storage"`
	Platform        string             `json:"platform"`
	Unit            string             `json:"unit"`
	ConfigPath      string             `json:"config_path"`
	Binary          string             `json:"binary"`
	Version         string             `json:"version"`
	PreviousVersion string             `json:"previous_version"`
	Original        []byte             `json:"original"`
	Target          []byte             `json:"target"`
	OriginalFileID  string             `json:"original_file_id"`
	TargetFileID    string             `json:"target_file_id"`
	PID             int                `json:"pid"`
	ProcessStart    string             `json:"process_start"`
	ReloadPID       int                `json:"reload_pid,omitempty"`
	ReloadStart     string             `json:"reload_start,omitempty"`
	Stage           serviceReloadStage `json:"stage"`
}

type serviceReloader struct {
	Platform, Unit, Binary, Version string
	Exec                            CommandExecutor
	Log                             *slog.Logger
	Control                         func(context.Context, Config, ControlRequest, int) (ControlResponse, int, error)
	ProcessArgs                     func(int) ([]string, error)
	ProcessStart                    func(int) (string, error)
	Preflight                       func(context.Context, Config, Snapshot) error
	Wait                            func(context.Context) bool
	BeforePublish                   func()
}

func newServiceReloader(out io.Writer) *serviceReloader {
	binary, _ := os.Executable()
	binary, _ = filepath.EvalSymlinks(binary)
	args := systemdProcessCommandLine
	if runtime.GOOS == "darwin" {
		args = launchdProcessCommandLine
	}
	return &serviceReloader{Platform: runtime.GOOS, Unit: servicePath(), Binary: binary, Version: Version, Exec: OSCommand{},
		Log: slog.New(slog.NewTextHandler(out, nil)),
		Control: func(ctx context.Context, c Config, req ControlRequest, pid int) (ControlResponse, int, error) {
			return sendControlPeer(ctx, c, req, pid, true)
		}, ProcessArgs: args, ProcessStart: tartRunProcessStartIdentity,
		Preflight: func(ctx context.Context, c Config, s Snapshot) error {
			_, err := validateReloadCandidate(ctx, c, s, resolveDockerCapacity, defaultDriver, NewGitHub)
			return err
		}, Wait: func(ctx context.Context) bool { return waitContext(ctx, time.Second) },
	}
}

func reloadJournalPath(unit string) string {
	return filepath.Join(filepath.Dir(unit), ".runmoor-service-reload.json")
}

func reloadTargetPath(j *serviceReloadJournal) string {
	return filepath.Join(filepath.Dir(j.Unit), ".runmoor-reload-"+j.Token)
}

func reloadFailure() error {
	return problem(ErrControl, "Service reload did not complete.", "Inspect status and the user service, then retry reload with the same installed CLI and configuration. Preserve state and managed data; do not roll back to an incompatible binary.")
}

func lockServiceOperation(unit string) (*os.File, error) {
	lock, err := lockState(filepath.Join(filepath.Dir(unit), ".runmoor-service.lock"))
	if err != nil {
		return nil, problem(ErrRetry, "Another service operation is in progress or its lock is unavailable.", "Wait for the Runmoor service operation, then retry from the same user session.")
	}
	return lock, nil
}

func serviceVersion(version string) (string, error) {
	v := "v" + version
	if !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Canonical(v) != v {
		return "", problem(ErrControl, "The manager or CLI reported an invalid release version.", "Use a supported Runmoor release and inspect the running manager.")
	}
	return v, nil
}

func readReloadJournal(unit string) (*serviceReloadJournal, error) {
	path := reloadJournalPath(unit)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, reloadFailure()
	}
	body, err := readPrivate(path, reloadJournalLimit)
	if err != nil {
		return nil, err
	}
	var j serviceReloadJournal
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&j) != nil {
		return nil, reloadFailure()
	}
	var extra any
	if d.Decode(&extra) != io.EOF || j.Schema != 1 || !validID(j.Token) || !validID(j.Installation) || j.PID <= 0 || j.ProcessStart == "" || j.ReloadPID < 0 || j.ReloadPID == 0 && j.ReloadStart != "" || j.ReloadPID > 0 && j.ReloadStart == "" || j.OriginalFileID == "" || j.TargetFileID == "" {
		return nil, reloadFailure()
	}
	for _, path := range []string{j.Unit, j.ConfigPath, j.Binary, j.Storage.State, j.Storage.Data} {
		cleaned, err := cleanAbsoluteServicePath(path)
		if err != nil || cleaned != path {
			return nil, reloadFailure()
		}
	}
	if j.Unit != unit {
		return nil, reloadFailure()
	}
	for _, body := range [][]byte{j.Original, j.Target} {
		config, err := serviceConfigFromDefinition(j.Platform, body)
		if err != nil || config != j.ConfigPath {
			return nil, reloadFailure()
		}
	}
	target, err := serviceDefinition(j.Platform, j.Binary, j.ConfigPath)
	if err != nil || target != string(j.Target) {
		return nil, reloadFailure()
	}
	if _, err := serviceVersion(j.Version); err != nil {
		return nil, err
	}
	if _, err := serviceVersion(j.PreviousVersion); err != nil {
		return nil, err
	}
	switch j.Stage {
	case reloadPrepared, reloadPublished, reloadRestartPending, reloadHandoffPending, reloadUnloaded, reloadStarting, reloadRunning:
	default:
		return nil, reloadFailure()
	}
	return &j, nil
}

// Replace only the owner-only file that was read. Publish complete bytes and
// sync its directory so an interruption never leaves a partial service unit.
func replaceReloadFile(path string, old []byte, limit int64, body []byte) error {
	before, info, err := readPrivateServiceDefinitionWithLimit(path, limit)
	if err != nil || !bytes.Equal(before, old) {
		return reloadFailure()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".runmoor-reload-*")
	if err != nil {
		return reloadFailure()
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err != nil {
		f.Close()
		return reloadFailure()
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return reloadFailure()
	}
	if err = f.Close(); err != nil {
		return reloadFailure()
	}
	current, currentInfo, err := readPrivateServiceDefinitionWithLimit(path, limit)
	if err != nil || !os.SameFile(info, currentInfo) || !bytes.Equal(current, old) {
		return reloadFailure()
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return reloadFailure()
	}
	return syncPrivateDir(filepath.Dir(path))
}

func readPrivateServiceDefinitionWithLimit(path string, limit int64) ([]byte, os.FileInfo, error) {
	f, err := openPrivate(path, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, nil, reloadFailure()
	}
	return body, info, nil
}

func (r *serviceReloader) stage(c Config, j *serviceReloadJournal, next serviceReloadStage) error {
	old, _ := json.Marshal(j)
	copy := *j
	copy.Stage = next
	body, _ := json.Marshal(&copy)
	if err := replaceReloadFile(reloadJournalPath(r.Unit), old, reloadJournalLimit, body); err != nil {
		return err
	}
	*j = copy
	r.Log.Info("service_reload_stage", "stage", next, "previous_version", j.PreviousVersion, "target_version", j.Version)
	return nil
}

func (r *serviceReloader) nativePID(ctx context.Context) (int, bool, error) {
	if r.Platform == "linux" {
		pid, err := systemdMainPID(ctx, r.Exec)
		return pid, true, err
	}
	if r.Platform != "darwin" {
		return 0, false, problem(ErrPlatform, "User-service reload is unsupported on this platform.", "Use a supported macOS or Ubuntu host.")
	}
	body, err := r.Exec.Run(ctx, "launchctl", []string{"list"}, minimalEnv(), nil)
	if err != nil {
		return 0, false, reloadFailure()
	}
	return parseLaunchdServiceListPID(body)
}

func reloadArguments(platform string, definition []byte) ([]string, error) {
	if platform == "linux" {
		return systemdServiceInvocation(definition)
	}
	return launchdServiceArguments(definition)
}

func (r *serviceReloader) invocation(pid int, expected []string) error {
	if pid <= 0 {
		return reloadFailure()
	}
	args, err := r.ProcessArgs(pid)
	if err != nil || !sameServiceArguments(args, expected) {
		return reloadFailure()
	}
	return nil
}

func (r *serviceReloader) originalInvocation(j *serviceReloadJournal) error {
	args, err := reloadArguments(r.Platform, j.Original)
	if err != nil {
		return err
	}
	if err := r.invocation(os.Getpid(), args); err != nil {
		return err
	}
	current, err := serviceVersion(r.Version)
	if err != nil {
		return err
	}
	previous, err := serviceVersion(j.PreviousVersion)
	if err != nil || current != previous {
		return reloadFailure()
	}
	return nil
}

func (r *serviceReloader) originalProcess(ctx context.Context, c Config, j *serviceReloadJournal) error {
	pid, _, err := r.nativePID(ctx)
	if err != nil || pid <= 0 {
		return reloadFailure()
	}
	args, err := reloadArguments(r.Platform, j.Original)
	if err != nil {
		return err
	}
	if err := r.invocation(pid, args); err != nil {
		return err
	}
	if pid == j.PID {
		start, err := r.ProcessStart(pid)
		if err != nil || start != j.ProcessStart {
			return reloadFailure()
		}
		return nil
	}
	// Before the replacement signal is committed, the service manager may have
	// restarted the previous binary from the unchanged definition. Exact
	// invocation plus the live manager version authorizes that same old manager;
	// the journal remains the authority for the later replacement.
	response, peer, err := r.Control(ctx, c, ControlRequest{Action: "status"}, pid)
	if err != nil || peer != pid || response.Status == nil || !response.Status.Running || response.Status.Version != j.PreviousVersion {
		return reloadFailure()
	}
	return nil
}

func (r *serviceReloader) definition(path string, j *serviceReloadJournal) (bool, error) {
	snapshot, err := validateServiceConfigMatch(r.Platform, r.Unit, path)
	if err != nil {
		return false, err
	}
	identity := hostFileIdentity(snapshot.info)
	if identity == j.TargetFileID && bytes.Equal(snapshot.data, j.Target) {
		return true, nil
	}
	if identity == j.OriginalFileID && bytes.Equal(snapshot.data, j.Original) {
		return false, nil
	}
	return false, reloadFailure()
}

func (r *serviceReloader) command(ctx context.Context, name string, args ...string) error {
	if ctx.Err() != nil {
		return reloadFailure()
	}
	_, err := r.Exec.Run(ctx, name, args, serviceCommandEnv(r.Platform, name), nil)
	if err != nil {
		return reloadFailure()
	}
	return nil
}

func (r *serviceReloader) stopping(c Config, j *serviceReloadJournal) error {
	s, err := ReadSnapshot(c)
	if err != nil {
		return err
	}
	if s.Installation != j.Installation || s.Stopping {
		return reloadFailure()
	}
	return nil
}

func (r *serviceReloader) publish(path string, j *serviceReloadJournal) error {
	target, err := r.definition(path, j)
	if err != nil || target {
		return err
	}
	body, info, err := readPrivateServiceDefinitionWithLimit(reloadTargetPath(j), serviceDefinitionLimit)
	if err != nil || hostFileIdentity(info) != j.TargetFileID || !bytes.Equal(body, j.Target) {
		return reloadFailure()
	}
	if _, err := r.definition(path, j); err != nil {
		return err
	}
	if r.BeforePublish != nil {
		r.BeforePublish()
	}
	if err := conditionalReplaceServiceDefinition(r.Unit, reloadTargetPath(j), j.OriginalFileID, j.Original, j.TargetFileID, j.Target); err != nil {
		return reloadFailure()
	}
	return syncPrivateDir(filepath.Dir(r.Unit))
}

// conditionalReplaceServiceDefinition uses an atomic exchange on Unix. If
// the path changed after the last ordinary read, it exchanges the files back
// only while the installed path still contains the exact target. This keeps a
// concurrent external replacement authoritative instead of overwriting it.
func conditionalReplaceServiceDefinition(unit, staged, expectedID string, expected []byte, targetID string, target []byte) error {
	current, info, err := readPrivateServiceDefinitionWithLimit(unit, serviceDefinitionLimit)
	if err != nil || hostFileIdentity(info) != expectedID || !bytes.Equal(current, expected) {
		return reloadFailure()
	}
	stagedBody, stagedInfo, err := readPrivateServiceDefinitionWithLimit(staged, serviceDefinitionLimit)
	if err != nil || hostFileIdentity(stagedInfo) != targetID || !bytes.Equal(stagedBody, target) {
		return reloadFailure()
	}
	if err := exchangeServiceFiles(staged, unit); err != nil {
		return reloadFailure()
	}

	oldBody, oldInfo, oldErr := readPrivateServiceDefinitionWithLimit(staged, serviceDefinitionLimit)
	currentBody, currentInfo, currentErr := readPrivateServiceDefinitionWithLimit(unit, serviceDefinitionLimit)
	oldMatches := oldErr == nil && hostFileIdentity(oldInfo) == expectedID && bytes.Equal(oldBody, expected)
	currentMatches := currentErr == nil && hostFileIdentity(currentInfo) == targetID && bytes.Equal(currentBody, target)
	if oldMatches && currentMatches {
		if err := os.Remove(staged); err != nil {
			return reloadFailure()
		}
		return nil
	}
	// Restore the current path only when it still contains the exact target we
	// exchanged into it. If another writer changed that path after the exchange,
	// preserve that writer's definition and leave the operation retryable.
	if currentMatches {
		if err := exchangeServiceFiles(staged, unit); err != nil {
			return reloadFailure()
		}
	}
	return reloadFailure()
}

func (r *serviceReloader) claimReloadInitiator(j *serviceReloadJournal) error {
	pid := os.Getpid()
	start, err := r.ProcessStart(pid)
	if err != nil || start == "" {
		return reloadFailure()
	}
	old, _ := json.Marshal(j)
	claimed := *j
	claimed.ReloadPID, claimed.ReloadStart = pid, start
	body, _ := json.Marshal(&claimed)
	if err := replaceReloadFile(reloadJournalPath(r.Unit), old, reloadJournalLimit, body); err != nil {
		return err
	}
	*j = claimed
	r.Log.Info("service_reload_recovered", "stage", j.Stage, "previous_version", j.PreviousVersion, "target_version", j.Version)
	return nil
}

func (r *serviceReloader) reloadInitiatorFinished(j *serviceReloadJournal) bool {
	if j.ReloadPID <= 0 || j.ReloadStart == "" {
		// Journals written before the initiator identity was added are retained
		// conservatively until a recovery CLI records its verified identity.
		return false
	}
	start, err := r.ProcessStart(j.ReloadPID)
	return os.IsNotExist(err) || err == nil && start != "" && start != j.ReloadStart
}

// The caller holds the service-operation lock. Only an explicit Start may
// discard completed Stop intent before manager startup; an automatic replacement
// must still preserve Stop even after the initiating CLI has exited.
func (r *serviceReloader) retireCompletedStopReload(ctx context.Context, path string, c Config) error {
	j, err := readReloadJournal(r.Unit)
	if err != nil || j == nil {
		return err
	}
	if j.Platform != r.Platform || j.ConfigPath != path || j.Storage != c.Storage || j.Binary != r.Binary || j.Version != r.Version {
		return reloadFailure()
	}
	target, err := r.definition(path, j)
	if err != nil || !target || !r.reloadInitiatorFinished(j) {
		return reloadFailure()
	}
	pid, _, err := r.nativePID(ctx)
	if err != nil || pid != 0 {
		return reloadFailure()
	}
	// Read without creating or migrating state, then exclude foreground managers
	// and offline mutations while checking cleanup and retiring the exact record.
	if _, err := ReadSnapshot(c); err != nil {
		return reloadFailure()
	}
	lock, err := lockState(filepath.Join(c.Storage.State, "manager.lock"))
	if err != nil {
		return reloadFailure()
	}
	defer unlockState(lock)
	s, err := ReadSnapshot(c)
	if err != nil || s.Installation != j.Installation || s.Config.Storage != j.Storage || s.Requested.Storage != j.Storage || !s.Stopping || !allTerminated(s) || !hostRestoreBoundary(s) {
		return reloadFailure()
	}
	// Native and file identities can change outside our locks. Recheck both
	// immediately before retirement, and leave uncertain intent intact.
	pid, _, err = r.nativePID(ctx)
	if err != nil || pid != 0 || ctx.Err() != nil || !r.reloadInitiatorFinished(j) {
		return reloadFailure()
	}
	target, err = r.definition(path, j)
	if err != nil || !target {
		return reloadFailure()
	}
	if err := r.retire(j); err != nil {
		return err
	}
	r.Log.Info("service_start_completed_stop_recovered", "target_version", j.Version)
	return nil
}

func (r *serviceReloader) retire(j *serviceReloadJournal) error {
	body, err := json.Marshal(j)
	if err != nil {
		return reloadFailure()
	}
	actual, err := readPrivate(reloadJournalPath(r.Unit), reloadJournalLimit)
	if err != nil || !bytes.Equal(actual, body) {
		return reloadFailure()
	}
	if err := os.Remove(reloadJournalPath(r.Unit)); err != nil {
		return reloadFailure()
	}
	return syncPrivateDir(filepath.Dir(r.Unit))
}

func (r *serviceReloader) Reload(ctx context.Context, path string, c Config) (result error) {
	j, err := readReloadJournal(r.Unit)
	if err != nil {
		return err
	}
	response, peer, controlErr := r.Control(ctx, c, ControlRequest{Action: "status"}, 0)
	if j == nil {
		if controlErr != nil {
			return controlErr
		}
		if _, err := os.Lstat(r.Unit); os.IsNotExist(err) {
			_, _, err = r.Control(ctx, c, ControlRequest{Action: "reload"}, peer)
			return err
		} else if err != nil {
			return reloadFailure()
		}
		pid, _, err := r.nativePID(ctx)
		if err != nil {
			r.Log.Warn("service_reload_native_inspection_unavailable", "platform", r.Platform, "code", classify(err, ErrControl, "Native service inspection failed.", "Inspect the user service.").Code)
		}
		if err != nil || pid != peer {
			// Without native ownership evidence, only the authenticated socket peer
			// may handle ordinary reload. Pending journals require native recovery.
			_, _, err = r.Control(ctx, c, ControlRequest{Action: "reload"}, peer)
			return err
		}
	} else if controlErr == nil {
		// A reachable socket owned by a different process takes precedence over
		// journal recovery. The service may have stopped while a foreground
		// manager started, so resuming native recovery would operate on the wrong
		// generation or start a competing service.
		pid, _, err := r.nativePID(ctx)
		if err != nil {
			return err
		}
		if pid != peer {
			_, _, err = r.Control(ctx, c, ControlRequest{Action: "reload"}, peer)
			return err
		}
	}
	lock, err := lockServiceOperation(r.Unit)
	if err != nil {
		return err
	}
	defer unlockState(lock)
	// A competing invocation may have finished before this lock was acquired.
	j, err = readReloadJournal(r.Unit)
	if err != nil {
		return err
	}
	if j != nil {
		// Repeat the peer check after taking the lock. A foreground manager can
		// start between the initial probe and lock acquisition; its socket owns
		// reload even when the old service journal is still present.
		_, peer, controlErr = r.Control(ctx, c, ControlRequest{Action: "status"}, 0)
		if controlErr == nil {
			pid, _, e := r.nativePID(ctx)
			if e != nil {
				return e
			}
			if pid != peer {
				_, _, e = r.Control(ctx, c, ControlRequest{Action: "reload"}, peer)
				return e
			}
		}
	}
	previousVersion, targetVersion := "", ""
	defer func() {
		if result != nil {
			stage := reloadValidating
			if j != nil {
				stage, previousVersion, targetVersion = j.Stage, j.PreviousVersion, j.Version
			}
			r.Log.Warn("service_reload_incomplete", "stage", stage, "previous_version", previousVersion, "target_version", targetVersion, "code", classify(result, ErrControl, "Reload failed.", "Retry reload.").Code)
		}
	}()
	if j == nil {
		snapshot, err := validateServiceConfigMatch(r.Platform, r.Unit, path)
		if err != nil {
			return err
		}
		response, peer, err = r.Control(ctx, c, ControlRequest{Action: "status"}, peer)
		if err != nil || response.Status == nil || !response.Status.Running {
			return reloadFailure()
		}
		pid, _, err := r.nativePID(ctx)
		if err != nil || pid != peer {
			return reloadFailure()
		}
		args, err := reloadArguments(r.Platform, snapshot.data)
		if err != nil {
			return err
		}
		if err := r.invocation(pid, args); err != nil {
			return err
		}
		current, err := serviceVersion(response.Status.Version)
		if err != nil {
			return err
		}
		targetSemver, err := serviceVersion(r.Version)
		if err != nil {
			return err
		}
		if semver.Compare(current, targetSemver) >= 0 {
			_, _, err = r.Control(ctx, c, ControlRequest{Action: "reload"}, peer)
			return err
		}
		previousVersion, targetVersion = response.Status.Version, r.Version
		// The strings have passed the release-version parser before logging.
		r.Log.Info("service_reload_validation", "stage", reloadValidating, "previous_version", previousVersion, "target_version", r.Version)
		s, err := ReadSnapshot(c)
		if err != nil {
			return err
		}
		if s.Stopping || response.Status.Stopping {
			return reloadFailure()
		}
		if err := r.Preflight(ctx, c, s); err != nil {
			return err
		}
		// Verify the installed file still executes this CLI, including a
		// Homebrew path that moved to another Cellar revision.
		if _, err := cleanAbsoluteServicePath(r.Binary); err != nil {
			return reloadFailure()
		}
		version, err := r.Exec.Run(ctx, r.Binary, []string{"version"}, minimalEnv(), nil)
		if err != nil || strings.TrimSpace(string(version)) != "runmoor "+r.Version+" ("+Revision+")" {
			return reloadFailure()
		}
		text, err := serviceDefinition(r.Platform, r.Binary, path)
		if err != nil {
			return err
		}
		start, err := r.ProcessStart(pid)
		if err != nil {
			return reloadFailure()
		}
		reloadStart, err := r.ProcessStart(os.Getpid())
		if err != nil {
			return reloadFailure()
		}
		j = &serviceReloadJournal{Schema: 1, Token: newID(), Installation: s.Installation, Storage: c.Storage, Platform: r.Platform, Unit: r.Unit, ConfigPath: path, Binary: r.Binary, Version: r.Version, PreviousVersion: response.Status.Version, Original: snapshot.data, Target: []byte(text), PID: pid, ProcessStart: start, ReloadPID: os.Getpid(), ReloadStart: reloadStart, Stage: reloadPrepared}
		j.OriginalFileID = hostFileIdentity(snapshot.info)
		if err := requireServiceDefinitionUnchanged(r.Platform, r.Unit, path, snapshot); err != nil {
			return err
		}
		if err := r.originalProcess(ctx, c, j); err != nil {
			return err
		}
		if err := writePrivateExclusive(reloadTargetPath(j), j.Target); err != nil {
			return reloadFailure()
		}
		_, targetInfo, err := readPrivateServiceDefinition(reloadTargetPath(j))
		if err != nil {
			return err
		}
		j.TargetFileID = hostFileIdentity(targetInfo)
		body, _ := json.Marshal(j)
		if err := writePrivateExclusive(reloadJournalPath(r.Unit), body); err != nil {
			return reloadFailure()
		}
		r.Log.Info("service_reload_prepared", "previous_version", j.PreviousVersion, "target_version", j.Version)
	} else {
		if j.Platform != r.Platform || j.Unit != r.Unit || j.ConfigPath != path || j.Binary != r.Binary || j.Version != r.Version {
			return reloadFailure()
		}
		s, err := ReadSnapshot(c)
		if err != nil || s.Installation != j.Installation {
			return reloadFailure()
		}
		if err := r.Preflight(ctx, c, s); err != nil {
			return err
		}
		// The retry owns completion before checking readiness or resuming native
		// actions, so replacement startup cannot reclaim an exited CLI's intent.
		if err := r.claimReloadInitiator(j); err != nil {
			return err
		}
	}
	if _, err := r.definition(path, j); err != nil {
		return err
	}
	// Recovery observes the current outcome before repeating an OS operation.
	if !r.ready(ctx, c, j) {
		if r.Platform == "linux" {
			err = r.replaceLinux(ctx, path, c, j)
		} else {
			err = r.replaceLaunchd(ctx, path, c, j)
		}
		if err != nil {
			return err
		}
		for !r.ready(ctx, c, j) {
			if err := r.stopping(c, j); err != nil {
				return err
			}
			if !r.Wait(ctx) {
				return reloadFailure()
			}
		}
	}
	if err := r.stage(c, j, reloadRunning); err != nil {
		return err
	}
	pid, _, err := r.nativePID(ctx)
	if err != nil {
		return err
	}
	if err := r.stopping(c, j); err != nil {
		return err
	}
	if _, _, err := r.Control(ctx, c, ControlRequest{Action: "reload"}, pid); err != nil {
		return err
	}
	if !r.ready(ctx, c, j) {
		return reloadFailure()
	}
	body, _ := json.Marshal(j)
	actual, err := readPrivate(reloadJournalPath(r.Unit), reloadJournalLimit)
	if err != nil || !bytes.Equal(actual, body) {
		return reloadFailure()
	}
	if err := os.Remove(reloadJournalPath(r.Unit)); err != nil {
		return reloadFailure()
	}
	if err := syncPrivateDir(filepath.Dir(r.Unit)); err != nil {
		return err
	}
	r.Log.Info("service_reload_completed", "previous_version", j.PreviousVersion, "target_version", j.Version)
	return nil
}

func (r *serviceReloader) ready(ctx context.Context, c Config, j *serviceReloadJournal) bool {
	target, err := r.definition(j.ConfigPath, j)
	if err != nil || !target {
		return false
	}
	pid, _, err := r.nativePID(ctx)
	if err != nil || pid <= 0 {
		return false
	}
	args, err := reloadArguments(r.Platform, j.Target)
	if err != nil || r.invocation(pid, args) != nil {
		return false
	}
	response, peer, err := r.Control(ctx, c, ControlRequest{Action: "status"}, pid)
	return err == nil && peer == pid && response.Status != nil && response.Status.Running && !response.Status.Stopping && response.Status.Version == j.Version
}

func (r *serviceReloader) replaceLinux(ctx context.Context, path string, c Config, j *serviceReloadJournal) error {
	pid, _, err := r.nativePID(ctx)
	if err != nil {
		return err
	}
	if pid != j.PID && j.Stage != reloadPrepared && j.Stage != reloadPublished {
		// Once replacement was requested, zero or a new matching process is
		// an outcome to observe, never authority to kill another generation.
		if j.Stage == reloadRestartPending || j.Stage == reloadRunning {
			return nil
		}
		return reloadFailure()
	}
	if err := r.originalProcess(ctx, c, j); err != nil {
		return err
	}
	if err := r.stopping(c, j); err != nil {
		return err
	}
	if err := r.publish(path, j); err != nil {
		return err
	}
	if err := r.stage(c, j, reloadPublished); err != nil {
		return err
	}
	if err := r.command(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	target, err := r.definition(path, j)
	if err != nil || !target {
		return reloadFailure()
	}
	if err := r.originalProcess(ctx, c, j); err != nil {
		return err
	}
	if err := r.stopping(c, j); err != nil {
		return err
	}
	if err := r.stage(c, j, reloadRestartPending); err != nil {
		return err
	}
	return r.command(ctx, "systemctl", "--user", "kill", "--kill-who=main", "--signal=SIGKILL", systemdServiceName)
}

func (r *serviceReloader) replaceLaunchd(ctx context.Context, path string, c Config, j *serviceReloadJournal) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	target := domain + "/" + serviceLabel
	pid, loaded, err := r.nativePID(ctx)
	if err != nil {
		return err
	}
	runHandoff := func(stage bool) error {
		if err := r.originalProcess(ctx, c, j); err != nil {
			return err
		}
		if err := r.stopping(c, j); err != nil {
			return err
		}
		if stage {
			if err := r.stage(c, j, reloadHandoffPending); err != nil {
				return err
			}
		}
		// launchd's one-run debug override consumes a harmless helper instead
		// of relaunching the old cached manager. Unloading the helper cannot
		// invoke manager Stop or mutate a runner's durable lifecycle.
		if err := r.command(ctx, "launchctl", "debug", target, "--program", j.Binary, "--", j.Binary, "__service-reload-handoff", reloadJournalPath(r.Unit), j.Token); err != nil {
			return err
		}
		if err := r.originalProcess(ctx, c, j); err != nil {
			return err
		}
		if err := r.stopping(c, j); err != nil {
			return err
		}
		if _, err := r.definition(path, j); err != nil {
			return err
		}
		if err := r.command(ctx, "launchctl", "kickstart", "-k", target); err != nil {
			return err
		}
		for {
			pid, loaded, err = r.nativePID(ctx)
			if err != nil {
				return err
			}
			if pid > 0 && pid != j.PID {
				break
			}
			if !r.Wait(ctx) {
				return reloadFailure()
			}
		}
		return nil
	}
	if loaded && (pid == j.PID || j.Stage == reloadPrepared) {
		if err := runHandoff(true); err != nil {
			return err
		}
	} else if loaded && j.Stage == reloadHandoffPending {
		// A previous manager can restart after the handoff stage is journaled but
		// before launchd starts the one-run helper. Re-establish the helper only
		// when the loaded process still proves the original invocation/version;
		// a helper process falls through to its existing verified path below.
		if err := r.originalProcess(ctx, c, j); err == nil {
			if err := runHandoff(false); err != nil {
				return err
			}
		}
	}
	if loaded {
		if j.Stage == reloadStarting || j.Stage == reloadRunning {
			return nil
		}
		if j.Stage != reloadHandoffPending {
			return reloadFailure()
		}
		expected := []string{j.Binary, "__service-reload-handoff", reloadJournalPath(r.Unit), j.Token}
		if err := r.invocation(pid, expected); err != nil {
			return err
		}
		if _, err := r.definition(path, j); err != nil {
			return err
		}
		if err := r.command(ctx, "launchctl", "bootout", target); err != nil {
			return err
		}
	}
	// A missing job counts only in a reachable domain, after recorded
	// handoff intent. An inactive unverified loaded job never crosses here.
	if j.Stage != reloadHandoffPending && j.Stage != reloadUnloaded && j.Stage != reloadStarting {
		return reloadFailure()
	}
	if err := r.command(ctx, "launchctl", "print", domain); err != nil {
		return err
	}
	if err := r.stage(c, j, reloadUnloaded); err != nil {
		return err
	}
	if err := r.stopping(c, j); err != nil {
		return err
	}
	if err := r.publish(path, j); err != nil {
		return err
	}
	if err := r.stage(c, j, reloadStarting); err != nil {
		return err
	}
	if target, err := r.definition(path, j); err != nil || !target {
		return reloadFailure()
	}
	return r.command(ctx, "launchctl", "bootstrap", domain, r.Unit)
}

func serviceReloadHandoff(journal, token string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return waitServiceReloadHandoff(ctx, journal, token)
}

func waitServiceReloadHandoff(ctx context.Context, journal, token string) int {
	if !filepath.IsAbs(journal) || !validID(token) {
		return 2
	}
	unit := filepath.Join(filepath.Dir(journal), serviceLabel+".plist")
	if journal != reloadJournalPath(unit) {
		return 2
	}
	j, err := readReloadJournal(unit)
	if err != nil || j == nil || j.Token != token || j.Version != Version || j.Platform != "darwin" || j.Stage != reloadHandoffPending {
		return 2
	}
	// Keep this harmless job available for a later reload if the initiating
	// CLI is interrupted. It exits only when launchd unloads it or the user
	// session ends; it never opens manager state or launches work.
	<-ctx.Done()
	return 0
}

// Only the actual replacement service can consume the startup boundary. A
// foreground or previous-version run beside an interrupted journal retains
// normal Start behavior.
func (r *serviceReloader) startup(ctx context.Context, path string, c Config) (Config, bool, *serviceReloadJournal, error) {
	j, err := readReloadJournal(r.Unit)
	if err != nil {
		return c, false, nil, err
	}
	if j == nil {
		return c, false, nil, nil
	}
	pid, _, err := r.nativePID(ctx)
	if err != nil {
		return c, false, nil, err
	}
	if pid != os.Getpid() {
		return c, false, nil, nil
	}
	if j.Platform != r.Platform || j.Unit != r.Unit || j.ConfigPath != path {
		return c, false, nil, reloadFailure()
	}
	if j.Binary != r.Binary || j.Version != r.Version {
		// The original service definition can restart the old manager before the
		// replacement reaches its restart boundary. Let that manager run from its
		// committed configuration and leave the handoff journal untouched.
		if err := r.originalInvocation(j); err != nil {
			return c, false, nil, err
		}
		return c, false, nil, nil
	}
	target, err := r.definition(path, j)
	if err != nil || !target {
		return c, false, nil, reloadFailure()
	}
	args, err := reloadArguments(r.Platform, j.Target)
	if err != nil || r.invocation(pid, args) != nil {
		return c, false, nil, reloadFailure()
	}
	s, err := ReadSnapshot(Config{Storage: j.Storage})
	if err != nil || s.Installation != j.Installation {
		return c, false, nil, reloadFailure()
	}
	return s.Requested, true, j, nil
}
