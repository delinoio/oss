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
)

const TartVersion = "2.37.0"
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
		return nil, problem(ErrDependency, "Tart command failed.", "Check Tart 2.37.0, Guest Agent RPC and the owned VM with 'runmoor doctor'.")
	}
	return b, nil
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
	if strings.TrimSpace(string(b)) != TartVersion {
		return problem(ErrDependency, "Runmoor requires Tart 2.37.0.", "Install the documented Tart version yourself; Runmoor does not bundle it.")
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
	if e := verifyVMOwner(c, im.VM, installation, im.ID); e != nil {
		return e
	}
	v, e := t.vm(ctx, c, im.VM)
	if e != nil {
		return e
	}
	if v.Running || v.State == "suspended" {
		return problem(ErrImage, "A sealed base image must remain stopped.", "Stop external access to the base and prepare a new clean revision.")
	}
	digest, e := imageDigest(ctx, c, im.VM)
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

func (t *TartDriver) vm(ctx context.Context, c Config, name string) (vmInfo, error) {
	b, e := t.run(ctx, c, []string{"get", name, "--format", "json"}, nil)
	var v vmInfo
	if e != nil {
		return v, e
	}
	if json.Unmarshal(b, &v) != nil {
		return v, problem(ErrDependency, "Tart returned an incompatible VM description.", "Use the documented Tart version.")
	}
	return v, nil
}

type vmOwner struct {
	Installation string `json:"installation"`
	Entity       string `json:"entity"`
	VM           string `json:"vm"`
}

func vmPath(c Config, name string) string { return filepath.Join(c.Storage.Data, "tart", "vms", name) }
func vmOwnerPath(c Config, name string) string {
	return filepath.Join(c.Storage.Data, "tart-ownership", name+".json")
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
	f, e := openPrivate(vmOwnerPath(c, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	defer f.Close()
	b, _ := json.Marshal(vmOwner{installation, entity, name})
	if _, e = f.Write(b); e != nil {
		return e
	}
	return f.Sync()
}
func verifyVMOwner(c Config, name, installation, entity string) error {
	b, e := readPrivate(vmOwnerPath(c, name), 4096)
	var o vmOwner
	if e != nil || json.Unmarshal(b, &o) != nil || o != (vmOwner{installation, entity, name}) {
		return problem(ErrOwnership, "Tart VM ownership cannot be verified.", "Preserve the VM and restore its matching ownership record before cleanup.")
	}
	return nil
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
	if _, e := t.run(ctx, c, []string{"clone", im.VM, name}, nil); e != nil {
		return e
	}
	if _, e := t.run(ctx, c, []string{"set", name, "--cpu", strconv.Itoa(p.Resources.CPU), "--memory", strconv.FormatInt(p.Resources.MemoryMiB, 10)}, nil); e != nil {
		return e
	}
	pid, e := t.Exec.Start(c.TartExecutable, []string{"run", "--no-graphics", "--no-audio", name}, tartEnv(c))
	if e != nil {
		return problem(ErrPreparation, "Cannot start the owned Tart clone.", "Check virtualization support and the two-VM limit.")
	}
	h.PID = pid
	if e = publish(h); e != nil {
		return e
	}
	if e = t.guestReady(ctx, c, name, p.RunnerPath, p.RunnerVersion); e != nil {
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
	if _, e = t.run(ctx, c, []string{"exec", "-i", name, "/bin/sh", "-c", `umask 077; mkdir -p /tmp/runmoor; cat > /tmp/runmoor/helper; chmod 700 /tmp/runmoor/helper`}, binary); e != nil {
		return e
	}
	b, _ := json.Marshal(GuestInput{ID: r.ID, RunnerPath: p.RunnerPath, RunnerVersion: p.RunnerVersion, JIT: jit})
	_, e = t.run(ctx, c, []string{"exec", "-i", name, "/tmp/runmoor/helper", "__guest-bootstrap"}, strings.NewReader(string(b)))
	if e != nil {
		return problem(ErrPreparation, "Guest runner bootstrap did not confirm a live runner.", "Check that run.sh is executable and starts the pinned runner successfully, then resume the pool.")
	}
	return nil
}
func (t *TartDriver) guestReady(ctx context.Context, c Config, vm, path, version string) error {
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
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		return Observation{Handle: h}, nil
	}
	if e := verifyVMOwner(c, name, s.Installation, r.ID); e != nil {
		return Observation{}, e
	}
	v, e := t.vm(ctx, c, name)
	if e != nil {
		return Observation{}, e
	}
	out := Observation{Exists: true, Running: v.Running, Handle: h}
	if !v.Running {
		return out, nil
	}
	b, e := t.run(ctx, c, []string{"exec", name, "/tmp/runmoor/helper", "__guest-status", r.ID}, nil)
	if e != nil {
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
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		return nil
	}
	if e := verifyVMOwner(c, name, s.Installation, r.ID); e != nil {
		return e
	}
	v, e := t.vm(ctx, c, name)
	if e != nil {
		return e
	}
	if v.Running {
		if _, e = t.run(ctx, c, []string{"stop", name}, nil); e != nil {
			return e
		}
	}
	for {
		v, e = t.vm(ctx, c, name)
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
func (t *TartDriver) Cleanup(ctx context.Context, c Config, r Runner, s Snapshot) error {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	if _, e := os.Lstat(vmPath(c, name)); e == nil {
		if e = verifyVMOwner(c, name, s.Installation, r.ID); e != nil {
			return e
		}
		v, e := t.vm(ctx, c, name)
		if e != nil {
			return e
		}
		if v.Running {
			return problem(ErrCleanup, "Cannot delete a running VM.", "Confirm termination first.")
		}
		if _, e = t.run(ctx, c, []string{"delete", name}, nil); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return problem(ErrCleanup, "Cannot inspect the VM for cleanup.", "Check private data directory permissions.")
	}
	if e := verifyVMOwner(c, name, s.Installation, r.ID); e == nil {
		if e = os.Remove(vmOwnerPath(c, name)); e != nil && !os.IsNotExist(e) {
			return problem(ErrCleanup, "Cannot remove the completed VM ownership marker.", "Check private data directory permissions.")
		}
	}
	return nil
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
