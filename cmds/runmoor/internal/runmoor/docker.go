package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

type DockerDriver struct{}

const ownerKey = "io.runmoor.installation"
const runnerKey = "io.runmoor.runner"
const roleKey = "io.runmoor.role"

func dockerProblem() error {
	return problem(ErrDependency, "The local Docker operation failed.", "Check Docker availability, image digests, native architecture and permissions with 'runmoor doctor'.")
}
func dockerEndpoint(ctx context.Context, c Config) (string, error) {
	endpoint := c.DockerSocket
	if endpoint == "" {
		endpoint = os.Getenv("DOCKER_HOST")
	}
	if endpoint == "" {
		args := []string{"context", "inspect", "--format", "{{json .Endpoints.docker.Host}}"}
		if v := os.Getenv("DOCKER_CONTEXT"); v != "" {
			args = append(args, v)
		}
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = minimalEnv()
		b, e := cmd.Output()
		if e == nil {
			if json.Unmarshal(b, &endpoint) != nil {
				return "", dockerProblem()
			}
		} else if _, e := os.Stat("/var/run/docker.sock"); e == nil {
			endpoint = "unix:///var/run/docker.sock"
		} else {
			return "", dockerProblem()
		}
	}
	if !strings.HasPrefix(endpoint, "unix:///") || strings.ContainsAny(endpoint, "\x00\n\r") {
		return "", problem(ErrDependency, "Remote Docker endpoints are unsupported.", "Select a local Unix-socket Docker engine; TCP, SSH and emulated engines are not supported.")
	}
	return endpoint, nil
}
func dockerClient(ctx context.Context, c Config) (*client.Client, error) {
	endpoint, e := dockerEndpoint(ctx, c)
	if e != nil {
		return nil, e
	}
	cli, e := client.New(client.WithHost(endpoint))
	if e != nil {
		return nil, dockerProblem()
	}
	return cli, nil
}
func (d *DockerDriver) Validate(ctx context.Context, c Config, p Pool, s Snapshot) error {
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return e
	}
	defer cli.Close()
	info, e := cli.Info(ctx, client.InfoOptions{})
	if e != nil {
		return dockerProblem()
	}
	arch := info.Info.Architecture
	if arch == "aarch64" {
		arch = "arm64"
	}
	if arch == "x86_64" {
		arch = "amd64"
	}
	if info.Info.OSType != "linux" || arch != runtime.GOARCH || arch != p.Arch {
		return problem(ErrPlatform, "Docker must run native-architecture Linux containers.", "Select a matching local Linux engine without CPU emulation.")
	}
	if c.Host.CPU > info.Info.NCPU || c.Host.MemoryMiB > info.Info.MemTotal/(1024*1024) {
		return problem(ErrCapacity, "Configured budgets exceed Docker engine resources.", "Increase Docker's assigned resources or reduce the host budget.")
	}
	if p.Mode == DinD && info.Info.CgroupVersion != "2" {
		return problem(ErrDependency, "Docker-in-Docker resource isolation requires cgroup v2.", "Use a local engine with cgroup v2 and privileged-container support.")
	}
	images := []string{p.Image}
	if p.Mode == DinD {
		images = append(images, p.DaemonImage)
	}
	for _, ref := range images {
		im, e := cli.ImageInspect(ctx, ref)
		if e != nil {
			return problem(ErrImage, "A pinned Docker image is not available locally.", "Pull the exact configured digest with Docker, then run doctor and resume.")
		}
		if im.Os != "linux" || im.Architecture != p.Arch {
			return problem(ErrImage, "A pinned image has the wrong platform.", "Select a Linux image digest for the host's native architecture.")
		}
	}
	return nil
}
func dockerLabels(s Snapshot, r Runner, role string) map[string]string {
	return map[string]string{ownerKey: s.Installation, runnerKey: r.ID, roleKey: role}
}
func ownedDocker(labels map[string]string, s Snapshot, r Runner) bool {
	return labels[ownerKey] == s.Installation && labels[runnerKey] == r.ID
}
func limits(r Resources) container.Resources {
	return container.Resources{NanoCPUs: int64(r.CPU) * 1e9, Memory: r.MemoryMiB * 1024 * 1024, MemorySwap: r.MemoryMiB * 1024 * 1024}
}
func mountsFor(p Pool, r Runner) []mount.Mount {
	v := []mount.Mount{{Type: mount.TypeVolume, Source: r.Name + "-work", Target: p.RunnerPath + "/_work"}}
	if p.Mode == DinD {
		v = append(v, mount.Mount{Type: mount.TypeVolume, Source: r.Name + "-socket", Target: "/run/runmoor-docker"}, mount.Mount{Type: mount.TypeVolume, Source: r.Name + "-externals", Target: p.RunnerPath + "/externals"})
	}
	return v
}
func (d *DockerDriver) Prepare(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if e := d.Validate(ctx, c, p, s); e != nil {
		return e
	}
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return e
	}
	defer cli.Close()
	h := r.Handle
	for _, suffix := range []string{"work", "socket", "externals", "docker"} {
		if p.Mode != DinD && suffix != "work" {
			continue
		}
		name := r.Name + "-" + suffix
		// An existing name is accepted only with both durable ownership labels.
		v, er := cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
		if er == nil && !ownedDocker(v.Volume.Labels, s, r) {
			return problem(ErrOwnership, "Docker volume name is already owned by another execution.", "Preserve the volume and resolve the name collision.")
		}
		if er != nil && !errdefs.IsNotFound(er) {
			return dockerProblem()
		}
		created, e := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: dockerLabels(s, r, suffix)})
		if e != nil {
			return dockerProblem()
		}
		h.Volumes = append(h.Volumes, created.Volume.Name)
		if e = publish(h); e != nil {
			return e
		}
	}
	netName := r.Name + "-network"
	existing, e := cli.NetworkInspect(ctx, netName, client.NetworkInspectOptions{})
	if e == nil && !ownedDocker(existing.Network.Labels, s, r) {
		return problem(ErrOwnership, "Docker network name is already owned by another execution.", "Preserve the network and resolve the name collision.")
	}
	if e != nil && !errdefs.IsNotFound(e) {
		return dockerProblem()
	}
	net, e := cli.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge", Labels: dockerLabels(s, r, "network")})
	if e != nil {
		return dockerProblem()
	}
	h.Network = net.ID
	if e = publish(h); e != nil {
		return e
	}
	// Initialize only disposable named volumes. Source image files are copied
	// through a separate mount, preserving the operator-provided image.
	initMounts := []mount.Mount{{Type: mount.TypeVolume, Source: r.Name + "-work", Target: "/run/runmoor-work"}}
	if p.Mode == DinD {
		initMounts = append(initMounts, mount.Mount{Type: mount.TypeVolume, Source: r.Name + "-externals", Target: "/run/runmoor-externals"}, mount.Mount{Type: mount.TypeVolume, Source: r.Name + "-socket", Target: "/run/runmoor-socket"})
	}
	initScript := `set -eu; uid=$(id -u runner); gid=$(id -g runner); [ "$uid:$gid" = 1001:1001 ]; chown "$uid:$gid" /run/runmoor-work; if [ "$2" = dind ]; then cp -a "$1/externals/." /run/runmoor-externals/; chown "$uid:$gid" /run/runmoor-socket; fi`
	init, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: &container.Config{Image: p.Image, User: "root", Entrypoint: []string{"/bin/sh", "-c", initScript, "runmoor", p.RunnerPath, string(p.Mode)}, Labels: dockerLabels(s, r, "init")}, HostConfig: &container.HostConfig{NetworkMode: "none", Resources: limits(p.Resources), Mounts: initMounts, LogConfig: container.LogConfig{Type: "none"}}, Name: r.Name + "-init"})
	if e != nil {
		return dockerProblem()
	}
	if _, e = cli.ContainerStart(ctx, init.ID, client.ContainerStartOptions{}); e != nil {
		return dockerProblem()
	}
	if e = waitContainer(ctx, cli, init.ID); e != nil {
		return e
	}
	if _, e = cli.ContainerRemove(ctx, init.ID, client.ContainerRemoveOptions{}); e != nil {
		return dockerProblem()
	}
	mode := container.NetworkMode(h.Network)
	if p.Mode == DinD {
		ms := mountsFor(p, r)
		ms = append(ms, mount.Mount{Type: mount.TypeVolume, Source: r.Name + "-docker", Target: "/var/lib/docker"})
		daemon, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: &container.Config{Image: p.DaemonImage, Entrypoint: []string{"dockerd-entrypoint.sh"}, Cmd: []string{"dockerd", "--host=unix:///run/runmoor-docker/docker.sock", "--group=1001", "--log-driver=none"}, Env: []string{"DOCKER_TLS_CERTDIR="}, Labels: dockerLabels(s, r, "daemon")}, HostConfig: &container.HostConfig{NetworkMode: mode, Privileged: true, CgroupnsMode: container.CgroupnsModePrivate, Resources: limits(p.DaemonResources), Mounts: ms, LogConfig: container.LogConfig{Type: "none"}}, Name: r.Name + "-daemon"})
		if e != nil {
			return dockerProblem()
		}
		h.Daemon = daemon.ID
		if e = publish(h); e != nil {
			return e
		}
		if _, e = cli.ContainerStart(ctx, daemon.ID, client.ContainerStartOptions{}); e != nil {
			return dockerProblem()
		}
		mode = container.NetworkMode("container:" + h.Daemon)
	}
	env := []string{"RUNNER_MANUALLY_TRAP_SIG=1"}
	if p.Mode == DinD {
		env = append(env, "DOCKER_HOST=unix:///run/runmoor-docker/docker.sock")
	}
	marker := "/tmp/runmoor-started-" + r.ID
	runner, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: &container.Config{Image: p.Image, User: "runner", Entrypoint: []string{"/bin/sh", "-c", dockerRunnerScript, "runmoor", p.RunnerPath, p.RunnerVersion, string(p.Mode), marker}, Env: env, OpenStdin: true, StdinOnce: true, AttachStdin: true, Labels: dockerLabels(s, r, "runner")}, HostConfig: &container.HostConfig{NetworkMode: mode, Resources: limits(p.Resources), Mounts: mountsFor(p, r), CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"}, LogConfig: container.LogConfig{Type: "none"}}, Name: r.Name})
	if e != nil {
		return dockerProblem()
	}
	h.Container = runner.ID
	if e = publish(h); e != nil {
		return e
	}
	attached, e := cli.ContainerAttach(ctx, runner.ID, client.ContainerAttachOptions{Stream: true, Stdin: true})
	if e != nil {
		return dockerProblem()
	}
	defer attached.Close()
	if _, e = cli.ContainerStart(ctx, runner.ID, client.ContainerStartOptions{}); e != nil {
		return dockerProblem()
	}
	_ = attached.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, e = io.Copy(attached.Conn, strings.NewReader(jit+"\n")); e != nil {
		return dockerProblem()
	}
	if e = attached.CloseWrite(); e != nil {
		return dockerProblem()
	}
	var started time.Time
	for {
		if started.IsZero() && execDocker(ctx, cli, runner.ID, []string{"test", "-f", marker}) == nil {
			started = time.Now()
		}
		v, er := cli.ContainerInspect(ctx, runner.ID, client.ContainerInspectOptions{})
		if er != nil {
			return dockerProblem()
		}
		if v.Container.State != nil && !v.Container.State.Running {
			if v.Container.State.ExitCode == 78 {
				return problem(ErrRunnerVersion, "The image runner does not match runner_version.", "Prepare an image with the exact pinned version, then resume.")
			}
			return problem(ErrPreparation, "Runner bootstrap exited before becoming ready.", "Check the compatible image contract and Docker-in-Docker capabilities.")
		}
		// The marker proves only that bootstrap reached exec. Observe the
		// container after a full startup interval before resetting preparation
		// failures, while keeping run.sh as PID 1 for normal signal handling.
		if !started.IsZero() && time.Since(started) >= time.Second && v.Container.State != nil && v.Container.State.Running {
			return nil
		}
		if !waitContext(ctx, 250*time.Millisecond) {
			return problem(ErrPreparation, "Docker runner preparation timed out.", "Check image compatibility and Docker resources, then resume.")
		}
	}
}

const dockerRunnerScript = `set -eu
cd "$1"
[ "$(RUNNER_LOG_TO_STDOUT=0 ./bin/Runner.Listener --version 2>/dev/null | sed -n '/^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*$/p')" = "$2" ] || exit 78
if [ "$3" = dind ]; then
  until docker info >/dev/null 2>&1; do sleep 1; done
fi
IFS= read -r jit
[ -n "$jit" ] || exit 78
touch "$4"
exec ./run.sh --jitconfig "$jit" >/dev/null 2>&1
`

func waitContainer(ctx context.Context, c *client.Client, id string) error {
	wait := c.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-ctx.Done():
		return dockerProblem()
	case e := <-wait.Error:
		if e != nil {
			return dockerProblem()
		}
		return nil
	case v := <-wait.Result:
		if v.StatusCode != 0 || v.Error != nil {
			return problem(ErrPreparation, "Disposable container initialization failed.", "Check the image's runner user, shell, tools and filesystem permissions.")
		}
		return nil
	}
}
func execDocker(ctx context.Context, c *client.Client, id string, args []string) error {
	v, e := c.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: args})
	if e != nil {
		return e
	}
	if _, e = c.ExecStart(ctx, v.ID, client.ExecStartOptions{Detach: true}); e != nil {
		return e
	}
	for {
		st, e := c.ExecInspect(ctx, v.ID, client.ExecInspectOptions{})
		if e != nil {
			return e
		}
		if !st.Running {
			if st.ExitCode != 0 {
				return dockerProblem()
			}
			return nil
		}
		if !waitContext(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}
func dockerFilter(s Snapshot, r Runner) client.Filters {
	return make(client.Filters).Add("label", ownerKey+"="+s.Installation, runnerKey+"="+r.ID)
}
func (d *DockerDriver) Inspect(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, error) {
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return Observation{}, e
	}
	defer cli.Close()
	v, e := cli.ContainerInspect(ctx, r.Name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(e) {
		return Observation{Handle: r.Handle}, nil
	}
	if e != nil {
		return Observation{}, dockerProblem()
	}
	if v.Container.Config == nil || !ownedDocker(v.Container.Config.Labels, s, r) || (r.Handle.Container != "" && r.Handle.Container != v.Container.ID) {
		return Observation{}, problem(ErrOwnership, "Docker runner ownership is ambiguous.", "Keep the execution quarantined and inspect the owned resource labels.")
	}
	h := r.Handle
	h.Container = v.Container.ID
	out := Observation{Exists: true, Handle: h}
	if v.Container.State != nil {
		out.Running = v.Container.State.Running && !v.Container.State.Paused && !v.Container.State.Restarting
		code := v.Container.State.ExitCode
		out.ExitCode = &code
	}
	dind := r.Handle.Daemon != ""
	if p := s.Pools[r.PoolID]; p != nil && p.Spec.Mode == DinD {
		dind = true
	}
	if out.Running && dind {
		daemon, err := cli.ContainerInspect(ctx, r.Name+"-daemon", client.ContainerInspectOptions{})
		if errdefs.IsNotFound(err) {
			out.Running = false
			return out, nil
		}
		if err != nil {
			return Observation{}, dockerProblem()
		}
		if daemon.Container.Config == nil || !ownedDocker(daemon.Container.Config.Labels, s, r) || daemon.Container.Config.Labels[roleKey] != "daemon" || (r.Handle.Daemon != "" && r.Handle.Daemon != daemon.Container.ID) {
			return Observation{}, problem(ErrOwnership, "Docker-in-Docker daemon ownership is ambiguous.", "Keep the execution quarantined and inspect its recorded daemon identity and ownership labels.")
		}
		out.Handle.Daemon = daemon.Container.ID
		// A live runner without its daemon is not an available execution. This
		// requests busy-aware cleanup; only Stop may confirm local termination.
		out.Running = daemon.Container.State != nil && daemon.Container.State.Running && !daemon.Container.State.Paused && !daemon.Container.State.Restarting
	}
	return out, nil
}

// Label discovery alone cannot prove absence: a replacement with foreign labels
// is omitted by that query. Check both deterministic names and persisted IDs,
// including partial preparations, before stopping anything and before releasing
// the execution's reservation.
func verifyDockerContainers(ctx context.Context, cli *client.Client, r Runner, s Snapshot, stopped bool) error {
	refs := []struct{ ref, id, role string }{
		{r.Name, r.Handle.Container, "runner"},
		{r.Name + "-daemon", r.Handle.Daemon, "daemon"},
		{r.Name + "-init", "", "init"},
		{r.Handle.Container, r.Handle.Container, "runner"},
		{r.Handle.Daemon, r.Handle.Daemon, "daemon"},
	}
	for _, ref := range refs {
		if ref.ref == "" {
			continue
		}
		v, err := cli.ContainerInspect(ctx, ref.ref, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return dockerProblem()
		}
		if v.Container.Config == nil || !ownedDocker(v.Container.Config.Labels, s, r) || v.Container.Config.Labels[roleKey] != ref.role || (ref.id != "" && v.Container.ID != ref.id) {
			return problem(ErrOwnership, "Docker container ownership is ambiguous during termination.", "Keep the execution quarantined and inspect its recorded container identities and ownership labels.")
		}
		if stopped && (v.Container.State == nil || v.Container.State.Running || v.Container.State.Restarting) {
			return problem(ErrCleanup, "Container termination is not confirmed.", "Restore Docker access and retry cleanup; reservations remain held.")
		}
	}
	return nil
}

func (d *DockerDriver) Stop(ctx context.Context, c Config, r Runner, s Snapshot) error {
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return e
	}
	defer cli.Close()
	if e = verifyDockerContainers(ctx, cli, r, s, false); e != nil {
		return e
	}
	list, e := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: dockerFilter(s, r)})
	if e != nil {
		return dockerProblem()
	}
	for _, v := range list.Items {
		if !ownedDocker(v.Labels, s, r) {
			return problem(ErrOwnership, "Container ownership changed during termination.", "Inspect the resource before retrying cleanup.")
		}
		seconds := 10
		if _, e = cli.ContainerStop(ctx, v.ID, client.ContainerStopOptions{Timeout: &seconds}); e != nil && !errdefs.IsNotFound(e) && !errdefs.IsNotModified(e) {
			return dockerProblem()
		}
		st, e := cli.ContainerInspect(ctx, v.ID, client.ContainerInspectOptions{})
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil || st.Container.State == nil || st.Container.State.Running {
			return problem(ErrCleanup, "Container termination is not confirmed.", "Restore Docker access and retry cleanup; reservations remain held.")
		}
	}
	return verifyDockerContainers(ctx, cli, r, s, true)
}
func (d *DockerDriver) Cleanup(ctx context.Context, c Config, r Runner, s Snapshot) error {
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return e
	}
	defer cli.Close()
	list, e := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: dockerFilter(s, r)})
	if e != nil {
		return dockerProblem()
	}
	for _, v := range list.Items {
		if v.State == "running" || v.State == "restarting" {
			return problem(ErrCleanup, "A container remains active during cleanup.", "Confirm termination before removing resources.")
		}
		if _, e = cli.ContainerRemove(ctx, v.ID, client.ContainerRemoveOptions{RemoveVolumes: true}); e != nil && !errdefs.IsNotFound(e) {
			return dockerProblem()
		}
	}
	nets, e := cli.NetworkList(ctx, client.NetworkListOptions{Filters: dockerFilter(s, r)})
	if e != nil {
		return dockerProblem()
	}
	for _, v := range nets.Items {
		if _, e = cli.NetworkRemove(ctx, v.ID, client.NetworkRemoveOptions{}); e != nil && !errdefs.IsNotFound(e) {
			return dockerProblem()
		}
	}
	vols, e := cli.VolumeList(ctx, client.VolumeListOptions{Filters: dockerFilter(s, r)})
	if e != nil {
		return dockerProblem()
	}
	for _, v := range vols.Items {
		if _, e = cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: false}); e != nil && !errdefs.IsNotFound(e) {
			return dockerProblem()
		}
	}
	return nil
}

// Docker output is only exposed to tests as a bounded byte buffer. Production
// execution intentionally disables the Docker log driver for all job resources.
type boundedBuffer struct {
	bytes.Buffer
	Limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remain := b.Limit - b.Len()
	if remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
