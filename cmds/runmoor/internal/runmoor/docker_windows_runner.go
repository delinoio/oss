package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func dockerExecutionOS() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

var validateWindowsDockerHost = func(context.Context) error { return nil }

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func powershell(script string) []string {
	return []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; " + script}
}
func windowsDockerLimits(r Resources) container.Resources {
	return container.Resources{CPUCount: int64(r.CPU), Memory: r.MemoryMiB * 1024 * 1024}
}
func windowsDockerHost(r Resources) *container.HostConfig {
	return &container.HostConfig{Isolation: container.IsolationHyperV, Resources: windowsDockerLimits(r), LogConfig: container.LogConfig{Type: "none"}}
}
func windowsImageCompatible(osName, arch, version string) bool {
	return osName == "windows" && arch == "amd64" && strings.HasPrefix(version, "10.0.20348.")
}

func (d *DockerDriver) prepareWindows(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if !validHostJIT(jit) || p.Mode != Plain || !validWindowsRunnerPath(p.RunnerPath) {
		return problem(ErrConfig, "Windows Docker requires plain mode and a dedicated Windows runner path.", "Use Windows x64 without DinD.")
	}
	if e := d.Validate(ctx, c, p, s); e != nil {
		return e
	}
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return e
	}
	defer cli.Close()
	image, e := cli.ImageInspect(ctx, p.Image)
	if e != nil {
		return dockerProblem()
	}
	if !windowsImageCompatible(image.Os, image.Architecture, image.OsVersion) {
		return problem(ErrImage, "Windows Docker requires a Server Core LTSC 2022 x64 image.", "Use a compatible immutable Windows image with PowerShell and ContainerUser.")
	}
	volume := r.Name + "-work"
	v, e := cli.VolumeInspect(ctx, volume, client.VolumeInspectOptions{})
	if e == nil && !ownedDockerVolume(v.Volume.Name, v.Volume.Labels, s, r, "work") {
		return problem(ErrOwnership, "Windows work volume ownership is ambiguous.", "Preserve the volume and inspect its ownership.")
	}
	if e != nil && !errdefs.IsNotFound(e) {
		return dockerProblem()
	}
	if errdefs.IsNotFound(e) {
		_, e = cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: volume, Labels: dockerLabels(s, r, "work")})
		if e != nil {
			return dockerProblem()
		}
	}
	host := windowsDockerHost(p.Resources)
	host.NetworkMode = "none"
	host.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: p.RunnerPath + `\_work`}}
	// Only the disposable volume is writable by the unprivileged runner. No host
	// bind mounts or Docker engine pipe are exposed to jobs.
	initScript := `& icacls.exe ` + psQuote(p.RunnerPath+`\_work`) + ` /grant '*S-1-5-93-2-2:(OI)(CI)F' /T /Q | Out-Null; if($LASTEXITCODE -ne 0){exit 1}`
	init, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: r.Name + "-init", Config: &container.Config{Image: p.Image, User: "ContainerAdministrator", Entrypoint: powershell(initScript), Labels: dockerLabels(s, r, "init")}, HostConfig: host})
	if e != nil {
		return dockerProblem()
	}
	if _, e = cli.ContainerStart(ctx, init.ID, client.ContainerStartOptions{}); e != nil {
		return dockerProblem()
	}
	if e = waitContainer(ctx, cli, init.ID); e != nil {
		return e
	}
	marker := p.RunnerPath + `\.runmoor-ready-` + r.ID
	script := `Set-Location -LiteralPath ` + psQuote(p.RunnerPath) + `; $v=& .\bin\Runner.Listener.exe --version 2>$null; if($LASTEXITCODE -ne 0 -or @($v | Where-Object { $_ -eq ` + psQuote(p.RunnerVersion) + ` }).Count -ne 1){exit 78}; foreach($f in @('.runner','.credentials','.credentials_rsaparams')){if(Test-Path -LiteralPath $f){exit 78}}; $jit=[Console]::ReadLine(); if([string]::IsNullOrEmpty($jit)){exit 78}; [IO.File]::WriteAllText(` + psQuote(marker) + `, 'ready'); & .\bin\Runner.Listener.exe run --jitconfig $jit *> $null; exit $LASTEXITCODE`
	host = windowsDockerHost(p.Resources)
	host.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: p.RunnerPath + `\_work`}}
	created, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: r.Name, Config: &container.Config{Image: p.Image, User: "ContainerUser", Entrypoint: powershell(script), Env: []string{"RUNNER_LOG_TO_STDOUT=0"}, OpenStdin: true, StdinOnce: true, AttachStdin: true, Labels: dockerLabels(s, r, "runner")}, HostConfig: host})
	if e != nil {
		return dockerProblem()
	}
	h := r.Handle
	h.Container = created.ID
	if e = publish(h); e != nil {
		return e
	}
	attached, e := cli.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{Stream: true, Stdin: true})
	if e != nil {
		return dockerProblem()
	}
	defer attached.Close()
	if _, e = cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); e != nil {
		return dockerProblem()
	}
	_ = attached.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, e = io.WriteString(attached.Conn, jit+"\n"); e != nil {
		return dockerProblem()
	}
	if e = attached.CloseWrite(); e != nil {
		return dockerProblem()
	}
	var ready time.Time
	for {
		q, e := cli.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
		if e != nil {
			return dockerProblem()
		}
		if q.Container.State == nil || !q.Container.State.Running {
			return problem(ErrPreparation, "Windows container runner exited before readiness.", "Check the Windows image, runner version and ContainerUser permissions.")
		}
		if ready.IsZero() && execDocker(ctx, cli, created.ID, powershell(`if(!(Test-Path -LiteralPath `+psQuote(marker)+`)){exit 1}`)) == nil {
			ready = time.Now()
		}
		if !ready.IsZero() && time.Since(ready) >= time.Second {
			return nil
		}
		if !waitContext(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func (b *ManagedImageBuilder) prepareWindowsDocker(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
	if e := validateWindowsDockerHost(ctx); e != nil {
		return p, e
	}
	var e error
	c, e = dockerArtifactConfig(c, b.Store.View())
	if e != nil {
		return p, e
	}
	cli, e := dockerClient(ctx, c)
	if e != nil {
		return p, e
	}
	defer cli.Close()
	ref := p.Image
	if ref == "" {
		ref = "mcr.microsoft.com/windows/servercore:ltsc2022"
	}
	info, e := cli.ImageInspect(ctx, ref)
	if errdefs.IsNotFound(e) || p.Image == "" {
		pull, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{Platforms: []ocispec.Platform{{OS: "windows", Architecture: "amd64"}}})
		if err != nil {
			return p, dockerProblem()
		}
		_, err = io.Copy(io.Discard, &contextReader{ctx, pull})
		pull.Close()
		if err != nil {
			return p, dockerProblem()
		}
		info, e = cli.ImageInspect(ctx, ref)
	}
	if e != nil {
		return p, dockerProblem()
	}
	if !windowsImageCompatible(info.Os, info.Architecture, info.OsVersion) || info.Config == nil || len(info.Config.Volumes) > 0 {
		return p, problem(ErrImage, "Windows runner base must be Server Core LTSC 2022 x64 without declared volumes.", "Select a compatible immutable base image.")
	}
	archive, e := b.archive(ctx, c, p, a, r)
	if e != nil {
		return p, e
	}
	labels := map[string]string{ownerKey: b.Store.View().Installation, artifactKey: a.ID, roleKey: "image-preparation"}
	host := windowsDockerHost(a.Resources)
	host.NetworkMode = "none"
	created, e := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: artifactName(a.ID), Config: &container.Config{Image: info.ID, User: "ContainerAdministrator", Entrypoint: powershell(`while($true){Start-Sleep -Seconds 1}`), Labels: labels}, HostConfig: host})
	if e != nil {
		return p, dockerProblem()
	}
	if e = b.Store.Update(func(s *Snapshot) error {
		if s.Artifacts[a.ID] == nil {
			return staleUpdate()
		}
		s.Artifacts[a.ID].Container = created.ID
		return nil
	}); e != nil {
		return p, e
	}
	if _, e = cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); e != nil {
		return p, dockerProblem()
	}
	if e = dockerExec(ctx, cli, created.ID, "ContainerAdministrator", powershell(`if(Test-Path -LiteralPath `+psQuote(p.RunnerPath)+`){Remove-Item -LiteralPath `+psQuote(p.RunnerPath)+` -Recurse -Force}; New-Item -ItemType Directory -Path `+psQuote(p.RunnerPath)+` | Out-Null`)); e != nil {
		return p, e
	}
	f, e := os.Open(archive)
	if e != nil {
		return p, e
	}
	_, e = cli.CopyToContainer(ctx, created.ID, client.CopyToContainerOptions{DestinationPath: p.RunnerPath, Content: f})
	f.Close()
	if e != nil {
		return p, dockerProblem()
	}
	script := `& icacls.exe ` + psQuote(p.RunnerPath) + ` /grant '*S-1-5-93-2-2:(OI)(CI)F' /T /Q | Out-Null; if($LASTEXITCODE -ne 0){exit 1}`
	if e = dockerExec(ctx, cli, created.ID, "ContainerAdministrator", powershell(script)); e != nil {
		return p, e
	}
	script = `Set-Location -LiteralPath ` + psQuote(p.RunnerPath) + `; $v=& .\bin\Runner.Listener.exe --version 2>$null; if($LASTEXITCODE -ne 0 -or @($v | Where-Object {$_ -eq ` + psQuote(r.Version()) + `}).Count -ne 1){exit 1}; Remove-Item -LiteralPath '_diag' -Recurse -Force -ErrorAction SilentlyContinue`
	if e = dockerExec(ctx, cli, created.ID, "ContainerUser", powershell(script)); e != nil {
		return p, e
	}
	if e = b.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID].Generated = true; return nil }); e != nil {
		return p, e
	}
	// Preserve the base's Windows PATH/runtime environment and image metadata.
	// Only the disposable candidate's execution user and ownership labels change.
	var commitConfig container.Config
	raw, e := json.Marshal(info.Config)
	if e != nil || json.Unmarshal(raw, &commitConfig) != nil {
		return p, dockerProblem()
	}
	commitConfig.User = "ContainerUser"
	if commitConfig.Labels == nil {
		commitConfig.Labels = map[string]string{}
	}
	for key, value := range labels {
		commitConfig.Labels[key] = value
	}
	committed, e := cli.ContainerCommit(ctx, created.ID, client.ContainerCommitOptions{Reference: artifactTag(a.ID), Config: &commitConfig})
	if e != nil {
		return p, dockerProblem()
	}
	if e = b.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID].Image = committed.ID; return nil }); e != nil {
		return p, e
	}
	if e = cleanupPreparationContainer(ctx, cli, b.Store.View().Installation, a.ID, created.ID); e != nil {
		return p, e
	}
	p.Image = committed.ID
	p.RunnerVersion = r.Version()
	b.log("runner_image_verified", a)
	return p, nil
}
