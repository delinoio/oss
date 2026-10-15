//go:build windows

package runmoor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// This opt-in fixture never registers with GitHub or changes Docker modes. It
// tests the real Windows container/volume/bootstrap contract with a fake runner.
func TestWindowsDockerIntegration(t *testing.T) {
	if os.Getenv("RUNMOOR_WINDOWS_DOCKER_TEST") != "1" {
		t.Skip("set RUNMOOR_WINDOWS_DOCKER_TEST=1 with Docker in Windows containers mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := validateWindowsDockerHost(ctx); err != nil {
		t.Fatal(err)
	}
	c := fixtureConfig(t)
	c.Host.CPU = 1
	c.Host.MemoryMiB = 2048
	c.Host.MaxRunners = 1
	cli, err := dockerClient(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	endpoint, err := dockerEndpoint(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	buildDir := windowsPrivateFixture(t)
	source := `package main
import("fmt";"os";"time")
func main(){if len(os.Args)==2&&os.Args[1]=="--version"{fmt.Println("2.337.0");return};if len(os.Args)!=4||os.Args[1]!="run"||os.Args[2]!="--jitconfig"||os.Args[3]!="fixture-jit"{os.Exit(2)};if os.WriteFile("_work/started",[]byte("fixture"),0600)!=nil{os.Exit(3)};for{time.Sleep(time.Second)}}`
	file := filepath.Join(buildDir, "runner.go")
	if err = os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(buildDir, "Runner.Listener.exe"), file).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	dockerfile := `FROM mcr.microsoft.com/windows/servercore:ltsc2022
USER ContainerAdministrator
COPY Runner.Listener.exe C:/actions-runner/bin/Runner.Listener.exe
RUN powershell.exe -NoProfile -Command "& icacls.exe C:\actions-runner /grant '*S-1-5-93-2-2:(OI)(CI)F' /T /Q; if($LASTEXITCODE -ne 0){exit 1}"
USER ContainerUser
`
	if err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	tag := "runmoor-windows-fixture-" + newID()
	build := exec.CommandContext(ctx, "docker", "--host", endpoint, "build", "--quiet", "--tag", tag, buildDir)
	build.Env = minimalEnv()
	if _, err = build.Output(); err != nil {
		t.Fatal("Windows fixture image build failed; check Docker mode, Hyper-V and base image access")
	}
	defer cli.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{})
	image, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pools[0]
	p.Image = image.ID
	p.RunnerPath = `C:\actions-runner`
	p.Resources = Resources{CPU: 1, MemoryMiB: 2048}
	p.MaxRunners = 1
	c.Pools = []Pool{p}
	store, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	id := newID()
	r := Runner{ID: id, Name: "runmoor-" + id, PoolID: "windows-fixture", Backend: Docker, Phase: Preparing, Image: p.Image, Resources: p.Resources}
	s := store.View()
	s.Pools[r.PoolID] = &PoolState{ID: r.PoolID, Spec: p}
	driver := &DockerDriver{}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_ = driver.Stop(clean, c, r, s)
		if err := driver.Cleanup(clean, c, r, s); err != nil {
			t.Error(err)
		}
	}()
	if err = driver.Prepare(ctx, c, p, r, s, "fixture-jit", func(h Handle) error { r.Handle = h; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = execDocker(ctx, cli, r.Handle.Container, powershell(`if(!(Test-Path -LiteralPath 'C:\actions-runner\_work\started')){exit 1}`)); err != nil {
		t.Fatal(err)
	}
	container, err := cli.ContainerInspect(ctx, r.Handle.Container, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(container.Container.Config)
	if strings.Contains(string(raw), "fixture-jit") {
		t.Fatal("JIT leaked into Docker configuration")
	}
	if err = store.Update(func(v *Snapshot) error { v.Pools = s.Pools; v.Runners[r.ID] = &r; return nil }); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store = nil
	store, err = OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	s = store.View()
	observation, err := driver.Inspect(ctx, c, r, s)
	if err != nil || !observation.Running {
		t.Fatalf("restart inspection: running=%v error=%v", observation.Running, err)
	}
	if err = driver.Stop(ctx, c, r, s); err != nil {
		t.Fatal(err)
	}
	if err = driver.Cleanup(ctx, c, r, s); err != nil {
		t.Fatal(err)
	}
	if _, err = cli.VolumeInspect(ctx, r.Name+"-work", client.VolumeInspectOptions{}); !errdefs.IsNotFound(err) {
		t.Fatal("owned work volume was not removed")
	}
}
