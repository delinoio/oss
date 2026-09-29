package runmoor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const GuestAgentVersion = "0.14.2"

type CommandExecutor interface {
	Run(context.Context, string, []string, []string, io.Reader) ([]byte, error)
	Start(string, []string, []string) (int, error)
}
type OSCommand struct{}

func (OSCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = in
	out := &boundedBuffer{Limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.Bytes(), err
}
func (OSCommand) Start(name string, args, env []string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	// A detached process must inherit real null descriptors. io.Discard makes
	// os/exec create parent-owned pipes that close on manager exit and can
	// terminate a surviving VM with SIGPIPE when it next writes a diagnostic.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer null.Close()
	cmd.Stdout = null
	cmd.Stderr = null
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

type TartDriver struct {
	Exec            CommandExecutor
	HostCheck       func(context.Context) error
	GuestExecutable string
}

func tartEnv(c Config) []string {
	return append(minimalEnv(), "TART_HOME="+filepath.Join(c.Storage.Data, "tart"), "TART_NO_AUTO_PRUNE=1")
}
func (t *TartDriver) run(ctx context.Context, c Config, args []string, in io.Reader) ([]byte, error) {
	b, e := t.Exec.Run(ctx, c.TartExecutable, args, tartEnv(c), in)
	if e != nil {
		return nil, problem(ErrDependency, "Tart command failed.", "Check Tart 2.x.x, Guest Agent RPC and the owned VM with 'runmoor doctor'.")
	}
	return b, nil
}

func supportedTartVersion(output string) bool {
	version := "v" + strings.TrimSpace(output)
	if !semver.IsValid(version) || semver.Major(version) != "v2" || semver.Prerelease(version) != "" {
		return false
	}
	// x/mod also accepts v2 and v2.1; Tart must report a full SemVer triplet.
	return semver.Canonical(version) == strings.SplitN(version, "+", 2)[0]
}

func (t *TartDriver) check(ctx context.Context, c Config) error {
	if t.HostCheck != nil {
		if e := t.HostCheck(ctx); e != nil {
			return e
		}
	} else {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return problem(ErrPlatform, "Tart requires an Apple Silicon Mac.", "Use macOS 14 or later on arm64.")
		}
		b, e := t.Exec.Run(ctx, "/usr/bin/sw_vers", []string{"-productVersion"}, minimalEnv(), nil)
		if e != nil {
			return problem(ErrPlatform, "Cannot determine macOS version.", "Use macOS 14 or later.")
		}
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(string(b)), ".")[0])
		if major < 14 {
			return problem(ErrPlatform, "Tart execution requires macOS 14 or later.", "Upgrade macOS before enabling Tart pools.")
		}
	}
	b, e := t.run(ctx, c, []string{"--version"}, nil)
	if e != nil {
		return e
	}
	if !supportedTartVersion(string(b)) {
		return problem(ErrDependency, "Runmoor requires a stable Tart 2.x.x release.", "Install a stable Tart 2.x.x release yourself; Runmoor does not bundle it.")
	}
	return nil
}
func (t *TartDriver) Validate(ctx context.Context, c Config, p Pool, s Snapshot) error {
	if e := t.check(ctx, c); e != nil {
		return e
	}
	im := s.Images[p.Image]
	if im == nil || im.Phase != ImageSealed || im.RunnerVersion != p.RunnerVersion || im.RunnerPath != p.RunnerPath {
		return problem(ErrImage, "Pool image is not a compatible sealed revision.", "Seal a clean image with the configured runner version and path, then update the pool.")
	}
	return t.validateSealed(ctx, c, im, s.Installation)
}

func (t *TartDriver) validateSealed(ctx context.Context, c Config, im *Image, installation string) error {
	v, e := t.vmOwned(ctx, c, im.VM, installation, im.ID)
	if e != nil {
		return e
	}
	if v.Running || v.State == "suspended" {
		return problem(ErrImage, "A sealed base image must remain stopped.", "Stop external access to the base and prepare a new clean revision.")
	}
	digest, e := imageDigestOwned(ctx, c, im.VM, installation, im.ID)
	if e != nil {
		return e
	}
	if im.Digest == "" || digest != im.Digest {
		return problem(ErrImage, "Sealed image contents no longer match their recorded digest.", "Restore the matching base from backup or prepare and seal a new revision; do not reuse the altered base.")
	}
	return nil
}

type vmInfo struct {
	Running bool
	State   string
	CPU     int
	Memory  uint64
	OS      string
}

type vmOwner struct {
	Installation string `json:"installation"`
	Entity       string `json:"entity"`
	VM           string `json:"vm"`
}

const vmOwnerMarkerName = ".runmoor-owner.json"

func vmPath(c Config, name string) string { return filepath.Join(c.Storage.Data, "tart", "vms", name) }
func vmOwnerPath(c Config, name string) string {
	return filepath.Join(c.Storage.Data, "tart-ownership", name+".json")
}
func vmOwnerMarkerPath(c Config, name string) string {
	return filepath.Join(vmPath(c, name), vmOwnerMarkerName)
}

func ambiguousVMOwnership() error {
	return problem(ErrOwnership, "Tart VM ownership is ambiguous.", "Preserve the VM, Runmoor record and reservation. Restore a paired backup with its embedded identity or reimport the VM as a new revision; do not adopt or delete it by name.")
}

func claimVM(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) || !validID(installation) || !validID(entity) {
		return problem(ErrOwnership, "Invalid VM ownership identity.", "Preserve local state and inspect the installation.")
	}
	if _, e := os.Lstat(vmPath(c, name)); e == nil {
		return problem(ErrOwnership, "VM destination already exists.", "Never adopt an unrecorded VM; use a new image or runner identity.")
	} else if !os.IsNotExist(e) {
		return problem(ErrPermission, "Cannot inspect the VM destination.", "Check private data directory permissions.")
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart-ownership")); e != nil {
		return e
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart")); e != nil {
		return e
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart", "vms")); e != nil {
		return e
	}
	f, e := openPrivate(vmOwnerPath(c, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(vmOwner{installation, entity, name})
	if _, e = f.Write(b); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return syncPrivateDir(filepath.Join(c.Storage.Data, "tart-ownership"))
}
func verifyVMOwnerRecord(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) || !validID(installation) || !validID(entity) {
		return ambiguousVMOwnership()
	}
	b, e := readPrivate(vmOwnerPath(c, name), 4096)
	var o vmOwner
	if e != nil || json.Unmarshal(b, &o) != nil || o != (vmOwner{installation, entity, name}) {
		return ambiguousVMOwnership()
	}
	return nil
}

func vmDirectoryInfo(c Config, name string) (os.FileInfo, error) {
	if !safeName.MatchString(name) {
		return nil, ambiguousVMOwnership()
	}
	info, err := privateVMDirectory(vmPath(c, name))
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	return info, nil
}

func verifyVMOwner(c Config, name, installation, entity string) error {
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	before, err := vmDirectoryInfo(c, name)
	if err != nil {
		return err
	}
	b, err := readPrivate(vmOwnerMarkerPath(c, name), 4096)
	var marker vmOwner
	if err != nil || json.Unmarshal(b, &marker) != nil || marker != (vmOwner{installation, entity, name}) {
		return ambiguousVMOwnership()
	}
	after, err := vmDirectoryInfo(c, name)
	if err != nil || !os.SameFile(before, after) {
		return ambiguousVMOwnership()
	}
	return verifyVMOwnerRecord(c, name, installation, entity)
}

// publishVMOwnerMarker writes a complete marker into a newly created VM
// without replacing any existing file. A crash before the link leaves the VM
// ambiguous and therefore preserved for explicit recovery. Keep same-directory
// hard-link publication until a supported atomic no-replace rename is available:
// plain rename would overwrite a preexisting marker.
func publishVMOwnerMarker(c Config, name, installation, entity string) error {
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	dir := vmPath(c, name)
	before, err := vmDirectoryInfo(c, name)
	if err != nil {
		return err
	}
	markerPath := vmOwnerMarkerPath(c, name)
	if _, err = os.Lstat(markerPath); err == nil || !os.IsNotExist(err) {
		return ambiguousVMOwnership()
	}
	encoded, _ := json.Marshal(vmOwner{installation, entity, name})
	tmp, err := os.CreateTemp(dir, ".runmoor-owner-*.tmp")
	if err != nil {
		return problem(ErrPermission, "Cannot prepare the Tart VM ownership marker.", "Check the VM directory permissions; preserve the VM and retry only after inspection.")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(encoded)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return problem(ErrPermission, "Cannot write the Tart VM ownership marker.", "Check the VM directory permissions; preserve the VM and retry only after inspection.")
	}
	after, err := vmDirectoryInfo(c, name)
	if err != nil || !os.SameFile(before, after) {
		return ambiguousVMOwnership()
	}
	if err = os.Link(tmpPath, markerPath); err != nil {
		if os.IsExist(err) {
			return ambiguousVMOwnership()
		}
		return problem(ErrPermission, "Cannot publish the Tart VM ownership marker.", "Preserve the VM and check filesystem support and directory permissions.")
	}
	if err = syncPrivateDir(dir); err != nil {
		return problem(ErrPermission, "Cannot sync the Tart VM ownership marker.", "Preserve the VM and check directory permissions.")
	}
	return verifyVMOwner(c, name, installation, entity)
}

func (t *TartDriver) runOwned(ctx context.Context, c Config, installation, entity, name string, args []string, in io.Reader) ([]byte, error) {
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return nil, err
	}
	b, runErr := t.run(ctx, c, args, in)
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return nil, err
	}
	return b, runErr
}

func (t *TartDriver) vmOwned(ctx context.Context, c Config, name, installation, entity string) (vmInfo, error) {
	b, err := t.runOwned(ctx, c, installation, entity, name, []string{"get", name, "--format", "json"}, nil)
	var v vmInfo
	if err != nil {
		return v, err
	}
	if json.Unmarshal(b, &v) != nil {
		return v, problem(ErrDependency, "Tart returned an incompatible VM description.", "Check the installed Tart 2.x.x release.")
	}
	return v, nil
}

func (t *TartDriver) startOwned(c Config, name, installation, entity string, args []string) (int, error) {
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return 0, err
	}
	pid, runErr := t.Exec.Start(c.TartExecutable, args, tartEnv(c))
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return 0, err
	}
	return pid, runErr
}

func imageDigestOwned(ctx context.Context, c Config, name, installation, entity string) (string, error) {
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return "", err
	}
	digest, digestErr := imageDigest(ctx, c, name)
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return "", err
	}
	return digest, digestErr
}
func (t *TartDriver) Prepare(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if e := t.Validate(ctx, c, p, s); e != nil {
		return e
	}
	im := s.Images[p.Image]
	name := "rm-" + r.ID
	if e := claimVM(c, name, s.Installation, r.ID); e != nil {
		return e
	}
	h := Handle{VM: name}
	if e := publish(h); e != nil {
		return e
	}
	if _, e := t.runOwned(ctx, c, s.Installation, im.ID, im.VM, []string{"clone", im.VM, name}, nil); e != nil {
		return e
	}
	if e := publishVMOwnerMarker(c, name, s.Installation, r.ID); e != nil {
		return e
	}
	if _, e := t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"set", name, "--cpu", strconv.Itoa(p.Resources.CPU), "--memory", strconv.FormatInt(p.Resources.MemoryMiB, 10)}, nil); e != nil {
		return e
	}
	pid, e := t.startOwned(c, name, s.Installation, r.ID, []string{"run", "--no-graphics", "--no-audio", name})
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		return problem(ErrPreparation, "Cannot start the owned Tart clone.", "Check virtualization support and the two-VM limit.")
	}
	h.PID = pid
	if e = publish(h); e != nil {
		return e
	}
	if e = t.guestReadyOwned(ctx, c, name, p.RunnerPath, p.RunnerVersion, s.Installation, r.ID); e != nil {
		return e
	}
	exe := t.GuestExecutable
	if exe == "" {
		exe, e = os.Executable()
	}
	if e != nil {
		return problem(ErrPreparation, "Cannot locate the Runmoor guest helper.", "Run an installed release binary.")
	}
	binary, e := os.Open(exe)
	if e != nil {
		return e
	}
	defer binary.Close()
	// The helper is Runmoor's own native arm64 binary. Its upload carries no
	// management credentials, and all dynamic data is argv or stdin, never shell
	// interpolation. The detached guest supervisor survives an exec disconnect.
	if _, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", "-i", name, "/bin/sh", "-c", `umask 077; mkdir -p /tmp/runmoor; cat > /tmp/runmoor/helper; chmod 700 /tmp/runmoor/helper`}, binary); e != nil {
		return e
	}
	b, _ := json.Marshal(GuestInput{ID: r.ID, RunnerPath: p.RunnerPath, RunnerVersion: p.RunnerVersion, JIT: jit})
	_, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", "-i", name, "/tmp/runmoor/helper", "__guest-bootstrap"}, strings.NewReader(string(b)))
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		return problem(ErrPreparation, "Guest runner bootstrap did not confirm a live runner.", "Check that run.sh is executable and starts the pinned runner successfully, then resume the pool.")
	}
	return nil
}

// guestReadyUnowned tests guest validation response handling without a VM
// fixture. Runtime flows must use guestReadyOwned for Runmoor-owned VMs.
func (t *TartDriver) guestReadyUnowned(ctx context.Context, c Config, vm, path, version string) error {
	for {
		b, e := t.run(ctx, c, []string{"exec", vm, "/bin/sh", "-c", guestValidateScript, "runmoor", path, version, GuestAgentVersion}, nil)
		if e == nil && strings.TrimSpace(string(b)) == "RUNMOOR_READY" {
			return nil
		}
		if e == nil {
			return problem(ErrImage, "Guest validation rejected the runner, guest agent or clean workspace.", "Use the image preparation guide to install exact versions and remove runner credentials and workspaces.")
		}
		if !waitContext(ctx, time.Second) {
			return problem(ErrPreparation, "Tart Guest Agent did not become ready before the deadline.", "Enable Guest Agent RPC in the logged-in runner account and check the prepared image.")
		}
	}
}

func (t *TartDriver) guestReadyOwned(ctx context.Context, c Config, vm, path, version, installation, entity string) error {
	for {
		b, e := t.runOwned(ctx, c, installation, entity, vm, []string{"exec", vm, "/bin/sh", "-c", guestValidateScript, "runmoor", path, version, GuestAgentVersion}, nil)
		if e == nil && strings.TrimSpace(string(b)) == "RUNMOOR_READY" {
			return nil
		}
		if e == nil {
			return problem(ErrImage, "Guest validation rejected the runner, guest agent or clean workspace.", "Use the image preparation guide to install exact versions and remove runner credentials and workspaces.")
		}
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		if !waitContext(ctx, time.Second) {
			return problem(ErrPreparation, "Tart Guest Agent did not become ready before the deadline.", "Enable Guest Agent RPC in the logged-in runner account and check the prepared image.")
		}
	}
}

func (t *TartDriver) preparedGuestOwned(ctx context.Context, c Config, vm, path, installation, entity string) error {
	b, err := t.runOwned(ctx, c, installation, entity, vm, []string{"exec", vm, "/bin/sh", "-c", preparedGuestScript, "runmoor", path, GuestAgentVersion}, nil)
	if err != nil {
		if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
			return err
		}
		return problem(ErrPreparation, "Tart Guest Agent RPC is not ready.", "Log into the non-root runner account and enable Guest Agent 0.14.2 RPC at login.")
	}
	if strings.TrimSpace(string(b)) != "RUNMOOR_READY" {
		return problem(ErrImage, "The macOS guest is not prepared for Runmoor.", "Use a non-root runner account, Guest Agent 0.14.2 and a clean dedicated runner directory.")
	}
	return nil
}

const preparedGuestScript = `set -eu
invalid() { printf 'RUNMOOR_INVALID\n'; exit 0; }
[ "$(id -u)" != 0 ] || invalid
agent=$(tart-guest-agent --version | awk '{print $NF}')
case "$agent" in "$2"|"$2"-*) ;; *) invalid;; esac
p="$1"; while [ "$p" != / ]; do [ ! -L "$p" ] || invalid; p=$(dirname "$p"); done
for f in .runner .credentials .credentials_rsaparams; do [ ! -e "$1/$f" ] || invalid; done
[ ! -d "$1/_work" ] || [ -z "$(ls -A "$1/_work")" ] || invalid
printf 'RUNMOOR_READY\n'`

// A reachable guest returns a bounded validation result with a successful RPC.
// Nonzero Tart exec results remain transport failures, which may recover while
// the VM boots. Do not conflate a rejected image with an unavailable agent.
const guestValidateScript = `set -eu
invalid() { printf 'RUNMOOR_INVALID\n'; exit 0; }
uid=$(id -u) || invalid
case "$uid" in ''|0) invalid;; esac
agent_version=$(tart-guest-agent --version | awk '{print $NF}')
case "$agent_version" in "$3"|"$3"-*) ;; *) invalid;; esac
cd "$1" || invalid
[ "$(RUNNER_LOG_TO_STDOUT=0 ./bin/Runner.Listener --version 2>/dev/null | sed -n '/^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*$/p')" = "$2" ] || invalid
for file in .runner .credentials .credentials_rsaparams; do [ ! -e "$file" ] || invalid; done
if [ -d _work ]; then
  work=$(ls -A _work) || invalid
  [ -z "$work" ] || invalid
fi
printf 'RUNMOOR_READY\n'
`

func (t *TartDriver) Inspect(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, error) {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	h := r.Handle
	h.VM = name
	if !safeName.MatchString(name) {
		return Observation{}, ambiguousVMOwnership()
	}
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		return Observation{Handle: h}, nil
	} else if e != nil {
		return Observation{}, problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Check private data directory permissions; the execution reservation remains held.")
	}
	v, e := t.vmOwned(ctx, c, name, s.Installation, r.ID)
	if e != nil {
		return Observation{}, e
	}
	out := Observation{Exists: true, Running: v.Running, Handle: h}
	if !v.Running {
		return out, nil
	}
	b, e := t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", name, "/tmp/runmoor/helper", "__guest-status", r.ID}, nil)
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return Observation{}, e
		}
		if r.Phase == Preparing {
			return out, nil
		}
		return Observation{}, problem(ErrRetry, "Guest runner state is temporarily unavailable.", "Restore Guest Agent connectivity; the VM and reservation are preserved.")
	}
	var g GuestStatus
	if json.Unmarshal(b, &g) != nil || g.ID != r.ID {
		return Observation{}, problem(ErrOwnership, "Guest runner status does not match its execution identity.", "Keep this VM quarantined for diagnosis.")
	}
	if g.Finished {
		out.Running = false
		out.ExitCode = &g.ExitCode
	} else if !g.Ready {
		return Observation{}, problem(ErrRetry, "Guest runner startup has not completed.", "Wait for bootstrap or the original preparation deadline; the VM and reservation are preserved.")
	}
	return out, nil
}
func (t *TartDriver) Stop(ctx context.Context, c Config, r Runner, s Snapshot) error {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Check private data directory permissions; the execution reservation remains held.")
	}
	v, e := t.vmOwned(ctx, c, name, s.Installation, r.ID)
	if e != nil {
		return e
	}
	if v.Running {
		// Tart addresses VMs by name. Recheck after get and immediately before
		// stopping so a name replacement during inspection is left untouched.
		if _, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"stop", name}, nil); e != nil {
			return e
		}
	}
	for {
		v, e = t.vmOwned(ctx, c, name, s.Installation, r.ID)
		if e != nil {
			return e
		}
		if !v.Running {
			return nil
		}
		if !waitContext(ctx, 200*time.Millisecond) {
			return problem(ErrCleanup, "Tart VM termination could not be confirmed.", "Restore Tart access and retry cleanup; its reservation remains held.")
		}
	}
}

func removeVMOwnerRecord(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return problem(ErrCleanup, "Cannot inspect the Tart VM ownership record.", "Preserve local state and check private data directory permissions.")
	}
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	if err := os.Remove(vmOwnerPath(c, name)); err != nil && !os.IsNotExist(err) {
		return problem(ErrCleanup, "Cannot remove the completed VM ownership record.", "Check private data directory permissions; cleanup remains pending.")
	}
	return nil
}

func (t *TartDriver) Cleanup(ctx context.Context, c Config, r Runner, s Snapshot) error {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if _, e := os.Lstat(vmPath(c, name)); e == nil {
		if e = verifyVMOwner(c, name, s.Installation, r.ID); e != nil {
			return e
		}
		v, e := t.vmOwned(ctx, c, name, s.Installation, r.ID)
		if e != nil {
			return e
		}
		if v.Running {
			return problem(ErrCleanup, "Cannot delete a running VM.", "Confirm termination first.")
		}
		// The VM may have been replaced while Tart returned its status. Verify
		// both identities once more at the name-based deletion boundary.
		if e = verifyVMOwner(c, name, s.Installation, r.ID); e != nil {
			return e
		}
		if _, e = t.run(ctx, c, []string{"delete", name}, nil); e != nil {
			return e
		}
		if _, e = os.Lstat(vmPath(c, name)); e == nil {
			if ownerErr := verifyVMOwner(c, name, s.Installation, r.ID); ownerErr != nil {
				return ownerErr
			}
			return problem(ErrCleanup, "Tart did not remove the stopped VM.", "Inspect the VM and retry cleanup; its reservation remains held.")
		} else if !os.IsNotExist(e) {
			return problem(ErrCleanup, "Cannot confirm Tart VM removal.", "Inspect the VM and retry cleanup; its reservation remains held.")
		}
	} else if !os.IsNotExist(e) {
		return problem(ErrCleanup, "Cannot inspect the VM for cleanup.", "Check private data directory permissions.")
	}
	return removeVMOwnerRecord(c, name, s.Installation, r.ID)
}

// Keep file reads bounded and hide os.File.WriteTo so io.CopyBuffer cannot
// bypass cancellation checks while hashing a multi-gigabyte VM disk.
type imageDigestReader struct {
	ctx context.Context
	src io.Reader
}

func (r imageDigestReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

func imageDigest(ctx context.Context, c Config, vm string) (string, error) {
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for _, name := range []string{"config.json", "nvram.bin", "disk.img"} {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		f, e := os.Open(filepath.Join(vmPath(c, vm), name))
		if e != nil {
			return "", problem(ErrImage, "Prepared image files are incomplete.", "Prepare a standalone Tart macOS image before sealing.")
		}
		_, e = io.CopyBuffer(h, imageDigestReader{ctx: ctx, src: f}, buf)
		ce := f.Close()
		if cancelled := ctx.Err(); cancelled != nil {
			return "", cancelled
		}
		if e != nil || ce != nil {
			return "", problem(ErrImage, "Cannot hash the prepared image.", "Check disk integrity and retry sealing.")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
