package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const artifactKey = "io.runmoor.artifact"

func artifactName(id string) string { return "runmoor-image-" + id }
func artifactTag(id string) string  { return "runmoor-managed:" + id }
func artifactDirectory(c Config, id string) string {
	return filepath.Join(c.Storage.Data, "runner-artifacts", id)
}
func managedPathValid(p string) bool {
	return validRunnerPath(p) && filepath.Clean(p) == p && p != "/" && strings.Count(p, "/") >= 2
}
func (b *ManagedImageBuilder) Prepare(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
	if !managedPathValid(p.RunnerPath) {
		return p, problem(ErrConfig, "Automatic installation requires a dedicated clean runner directory.", "Use the default runner_path or another dedicated absolute directory.")
	}
	if p.Backend == Docker {
		return b.prepareDocker(ctx, c, p, a, r)
	}
	return b.prepareTart(ctx, c, p, a, r)
}
func (b *ManagedImageBuilder) archive(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (string, error) {
	asset, err := runnerArchiveAsset(r, p.Backend, p.Arch)
	if err != nil {
		return "", err
	}
	dir := artifactDirectory(c, a.ID)
	archive, err := downloadRunnerArchive(ctx, b.Client, asset, dir)
	if err != nil {
		return "", err
	}
	output := filepath.Join(dir, "runner.tar")
	if err = validatedRunnerTar(ctx, archive, output); err != nil {
		return "", err
	}
	return output, nil
}
func (b *ManagedImageBuilder) prepareDocker(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
	cli, err := dockerClient(ctx, c)
	if err != nil {
		return p, err
	}
	defer cli.Close()
	ref := p.Image
	official := ref == ""
	if official {
		ref = "ghcr.io/actions/actions-runner:" + r.Version()
	}
	info, err := cli.ImageInspect(ctx, ref)
	if err != nil && (!errdefs.IsNotFound(err)) {
		return p, dockerProblem()
	}
	if official || errdefs.IsNotFound(err) {
		pulled, e := cli.ImagePull(ctx, ref, client.ImagePullOptions{Platforms: []ocispec.Platform{{OS: "linux", Architecture: p.Arch}}})
		if e != nil {
			return p, dockerProblem()
		}
		defer pulled.Close()
		decoder := json.NewDecoder(pulled)
		for {
			var progress struct {
				Error string `json:"error"`
			}
			e = decoder.Decode(&progress)
			if e == io.EOF {
				break
			}
			if e != nil || progress.Error != "" {
				return p, dockerProblem()
			}
		}
		info, err = cli.ImageInspect(ctx, ref)
	}
	if err != nil || info.Os != "linux" || info.Architecture != p.Arch {
		return p, problem(ErrImage, "Runner image has an incompatible platform.", "Choose a native Linux image; emulation is unsupported.")
	}
	resolved := info.ID
	if official {
		found := false
		for _, digest := range info.RepoDigests {
			if strings.HasPrefix(digest, "ghcr.io/actions/actions-runner@sha256:") && immutableDockerImage(digest) {
				resolved = digest
				found = true
				break
			}
		}
		if !found {
			return p, problem(ErrImage, "The official image did not resolve to an immutable digest.", "Wait for the official image publication and retry.")
		}
	}
	labels := map[string]string{ownerKey: b.Store.View().Installation, artifactKey: a.ID, roleKey: "image-preparation"}
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: artifactName(a.ID), Config: &container.Config{Image: resolved, User: "runner", Entrypoint: []string{"/bin/sh", "-c", "while :; do sleep 1; done"}, Cmd: []string{}, Labels: labels}, HostConfig: &container.HostConfig{NetworkMode: "none", Resources: limits(a.Resources), LogConfig: container.LogConfig{Type: "none"}, SecurityOpt: []string{"no-new-privileges"}}})
	if err != nil {
		return p, dockerProblem()
	}
	if err = b.Store.Update(func(s *Snapshot) error {
		v := s.Artifacts[a.ID]
		if v == nil {
			return staleUpdate()
		}
		v.Container = created.ID
		return nil
	}); err != nil {
		return p, err
	}
	if _, err = cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return p, dockerProblem()
	}
	if !official {
		archive, e := b.archive(ctx, c, p, a, r)
		if e != nil {
			return p, e
		}
		// Destructive replacement happens only in an owned disposable candidate,
		// with no mounts, network or registration. The source digest is untouched.
		if e = dockerExec(ctx, cli, created.ID, "root", []string{"/bin/sh", "-c", `set -eu; [ "$(id -u runner):$(id -g runner)" = 1001:1001 ]; p="$1"; while [ "$p" != / ]; do [ ! -L "$p" ]; p=$(dirname "$p"); done; rm -rf "$1"; mkdir -p "$1"; chown 1001:1001 "$1"`, "runmoor", p.RunnerPath}); e != nil {
			return p, e
		}
		f, e := os.Open(archive)
		if e != nil {
			return p, e
		}
		_, e = cli.CopyToContainer(ctx, created.ID, client.CopyToContainerOptions{DestinationPath: p.RunnerPath, Content: f, CopyUIDGID: true})
		f.Close()
		if e != nil {
			return p, dockerProblem()
		}
	}
	if err = dockerExec(ctx, cli, created.ID, "runner", []string{"/bin/sh", "-c", `set -eu; [ "$(id -u):$(id -g)" = 1001:1001 ]; cd "$1"; [ -x ./run.sh ]; [ "$(RUNNER_LOG_TO_STDOUT=0 ./bin/Runner.Listener --version 2>/dev/null)" = "$2" ]; for f in .runner .credentials .credentials_rsaparams; do [ ! -e "$f" ]; done; [ ! -d _work ] || [ -z "$(ls -A _work)" ]`, "runmoor", p.RunnerPath, r.Version()}); err != nil {
		return p, err
	}
	if !official {
		// A deterministic tag plus ownership labels recovers a commit whose response
		// was lost. Do not retry a commit or adopt a pre-existing unowned image.
		if err = b.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID].Generated = true; return nil }); err != nil {
			return p, err
		}
		var commitConfig container.Config
		raw, e := json.Marshal(info.Config)
		if e != nil {
			return p, dockerProblem()
		}
		if json.Unmarshal(raw, &commitConfig) != nil {
			return p, dockerProblem()
		}
		commitConfig.Labels = labels
		committed, e := cli.ContainerCommit(ctx, created.ID, client.ContainerCommitOptions{Reference: artifactTag(a.ID), Config: &commitConfig})
		if e != nil {
			return p, dockerProblem()
		}
		resolved = committed.ID
	}
	if err = b.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID].Image = resolved; return nil }); err != nil {
		return p, err
	}
	if err = cleanupPreparationContainer(ctx, cli, b.Store.View().Installation, a.ID, created.ID); err != nil {
		return p, err
	}
	p.Image = resolved
	p.ImageSource = nil
	p.RunnerVersion = r.Version()
	return p, nil
}
func dockerExec(ctx context.Context, cli *client.Client, id, user string, args []string) error {
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{User: user, Cmd: args})
	if err != nil {
		return dockerProblem()
	}
	if _, err = cli.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err != nil {
		return dockerProblem()
	}
	for {
		result, e := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if e != nil {
			return dockerProblem()
		}
		if !result.Running {
			if result.ExitCode != 0 {
				return problem(ErrImage, "Runner candidate validation or installation failed.", "Check the dedicated runner path, non-root account and image dependencies.")
			}
			return nil
		}
		if !waitContext(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
}
func cleanupPreparationContainer(ctx context.Context, cli *client.Client, installation, id, containerID string) error {
	info, err := cli.ContainerInspect(ctx, artifactName(id), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		if containerID != "" {
			_, err = cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
			if err == nil {
				return problem(ErrOwnership, "The preparation container was renamed.", "Restore its owned identity before cleanup.")
			}
			if !errdefs.IsNotFound(err) {
				return dockerProblem()
			}
		}
		return nil
	}
	if err != nil {
		return dockerProblem()
	}
	if info.Container.Config == nil || info.Container.Config.Labels[ownerKey] != installation || info.Container.Config.Labels[artifactKey] != id || (containerID != "" && containerID != info.Container.ID) {
		return problem(ErrOwnership, "Preparation container ownership changed.", "Preserve the foreign resource and restore the matching ownership record.")
	}
	if _, err = cli.ContainerRemove(ctx, info.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return dockerProblem()
	}
	_, err = cli.ContainerInspect(ctx, artifactName(id), client.ContainerInspectOptions{})
	if !errdefs.IsNotFound(err) {
		if err == nil {
			return problem(ErrOwnership, "A replacement preparation container exists.", "Inspect the changed resource; its reservation is retained.")
		}
		return dockerProblem()
	}
	return nil
}
func (b *ManagedImageBuilder) prepareTart(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
	archive, err := b.archive(ctx, c, p, a, r)
	if err != nil {
		return p, err
	}
	source := p.Image
	home := ""
	if q := b.Store.View().Managed[p.Name]; q != nil && q.BaseImage != "" {
		source = q.BaseImage
	}
	if source == "" && p.ImageSource != nil {
		source = p.ImageSource.From
		home = p.ImageSource.SourceHome
	}
	im, err := b.Images.create(ctx, c, ImageRequest{ID: a.ID, Action: "create", Name: artifactName(a.ID), From: source, SourceHome: home, Resources: a.Resources})
	if err != nil {
		return p, err
	}
	if err = b.Store.Update(func(s *Snapshot) error {
		s.Artifacts[a.ID].Image = im.ID
		s.Artifacts[a.ID].Generated = true
		return nil
	}); err != nil {
		return p, err
	}
	if err = b.Images.reserve(c, im.ID); err != nil {
		return p, err
	}
	if _, err = b.Images.Tart.Exec.Start(c.TartExecutable, []string{"run", "--no-graphics", "--no-audio", im.VM}, tartEnv(c)); err != nil {
		return p, problem(ErrPreparation, "Cannot boot the runner preparation VM.", "Restore Tart availability; Runmoor will retry.")
	}
	if err = b.installTartArchive(ctx, c, im.VM, p.RunnerPath, archive); err != nil {
		return p, err
	}
	sealed, err := b.Images.Operate(ctx, c, ImageRequest{Action: "seal", ID: im.ID, RunnerPath: p.RunnerPath, RunnerVersion: r.Version()})
	if err != nil {
		return p, err
	}
	p.Image = sealed.ID
	p.ImageSource = nil
	p.RunnerVersion = r.Version()
	return p, nil
}
func (b *ManagedImageBuilder) installTartArchive(ctx context.Context, c Config, vm, path, archive string) error {
	// A separate readiness probe accepts a prepared guest without a runner. It
	// rejects dirty registration/workspace state before installing any files.
	script := `set -eu
invalid() { printf 'RUNMOOR_INVALID\n'; exit 0; }
[ "$(id -u)" != 0 ] || invalid
agent=$(tart-guest-agent --version | awk '{print $NF}')
case "$agent" in "$2"|"$2"-*) ;; *) invalid;; esac
p="$1"; while [ "$p" != / ]; do [ ! -L "$p" ] || invalid; p=$(dirname "$p"); done
for f in .runner .credentials .credentials_rsaparams; do [ ! -e "$1/$f" ] || invalid; done
[ ! -d "$1/_work" ] || [ -z "$(ls -A "$1/_work")" ] || invalid
printf 'RUNMOOR_READY\n'`
	for {
		out, e := b.Images.Tart.run(ctx, c, []string{"exec", vm, "/bin/sh", "-c", script, "runmoor", path, GuestAgentVersion}, nil)
		if e == nil {
			if strings.TrimSpace(string(out)) != "RUNMOOR_READY" {
				return problem(ErrImage, "Prepared macOS guest is incompatible or contains runner credentials/workspaces.", "Prepare a clean non-root account with Guest Agent RPC.")
			}
			break
		}
		if !waitContext(ctx, time.Second) {
			return ctx.Err()
		}
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = b.Images.Tart.run(ctx, c, []string{"exec", "-i", vm, "/bin/sh", "-c", `set -eu; umask 077; rm -rf "$1"; mkdir -p "$1"; cd "$1"; tar -xf -`, "runmoor", path}, f)
	return err
}
func (b *ManagedImageBuilder) Cleanup(ctx context.Context, c Config, a RunnerArtifact) error {
	if a.Backend == Docker {
		cli, err := dockerClient(ctx, c)
		if err != nil {
			return err
		}
		defer cli.Close()
		if err = cleanupPreparationContainer(ctx, cli, b.Store.View().Installation, a.ID, a.Container); err != nil {
			return err
		}
		if a.Generated {
			ref := artifactTag(a.ID)
			image, err := cli.ImageInspect(ctx, ref)
			if errdefs.IsNotFound(err) && a.Image != "" {
				image, err = cli.ImageInspect(ctx, a.Image)
				ref = a.Image
			}
			if err != nil && !errdefs.IsNotFound(err) {
				return dockerProblem()
			}
			if err == nil {
				if image.Config == nil || image.Config.Labels[ownerKey] != b.Store.View().Installation || image.Config.Labels[artifactKey] != a.ID || a.Image != "" && image.ID != a.Image {
					return problem(ErrOwnership, "Managed image ownership changed.", "Preserve the image and restore matching ownership before cleanup.")
				}
				if _, err = cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
					return problem(ErrCleanup, "A managed image is still referenced by Docker.", "Remove external references to this generated image; Runmoor will retry without force.")
				}
			}
		}
	} else {
		s := b.Store.View()
		im := s.Images[a.ID]
		if im != nil {
			if err := verifyVMOwner(c, im.VM, s.Installation, im.ID); err == nil {
				if err = b.Images.Tart.Stop(ctx, c, Runner{ID: im.ID, Handle: Handle{VM: im.VM}}, s); err != nil {
					return err
				}
			} else {
				_, vmErr := os.Lstat(vmPath(c, im.VM))
				_, ownerErr := os.Lstat(vmOwnerPath(c, im.VM))
				if !os.IsNotExist(vmErr) || !os.IsNotExist(ownerErr) {
					return err
				}
			}
			if err := b.Store.Update(func(v *Snapshot) error { v.Images[a.ID].Phase = ImagePreparing; return nil }); err != nil {
				return err
			}
			if _, err := b.Images.Operate(ctx, c, ImageRequest{Action: "remove", ID: a.ID}); err != nil {
				return err
			}
		}
	}
	return cleanupRunnerDownload(c, a.ID)
}
func cleanupRunnerDownload(c Config, id string) error {
	if !validID(id) {
		return problem(ErrOwnership, "Invalid download journal identity.", "Preserve the managed data directory.")
	}
	dir := artifactDirectory(c, id)
	if st, err := os.Lstat(dir); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return problem(ErrOwnership, "Managed download directory changed.", "Restore the owned directory before cleanup.")
		}
		return os.RemoveAll(dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
