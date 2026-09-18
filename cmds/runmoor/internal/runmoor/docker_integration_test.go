package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const testRunnerBase = "ghcr.io/actions/actions-runner@sha256:e5496277be5d09bc968b3d64911b74e219ac4a3f2edce956a3ecf9271bea1ef4"
const testDindBase = "docker.io/library/docker@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac"

func TestDockerIntegration(t *testing.T) {
	if os.Getenv("RUNMOOR_DOCKER_TEST") != "1" {
		t.Skip("set RUNMOOR_DOCKER_TEST=1 for real local Docker validation without GitHub")
	}
	c, s := fixtureStore(t)
	c.Host.CPU = 4
	c.Host.MemoryMiB = 2048
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cli, e := dockerClient(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer cli.Close()
	endpoint, e := dockerEndpoint(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	base := os.Getenv("RUNMOOR_TEST_RUNNER_IMAGE")
	if base == "" {
		base = testRunnerBase
	}
	daemon := os.Getenv("RUNMOOR_TEST_DIND_IMAGE")
	if daemon == "" {
		daemon = testDindBase
	}
	for _, ref := range []string{base, daemon} {
		stream, e := cli.ImagePull(ctx, ref, image.PullOptions{})
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(io.Discard, stream)
		stream.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	tag := "runmoor-fixture-" + newID()
	build := exec.CommandContext(ctx, "docker", "--host", endpoint, "build", "--quiet", "--tag", tag, "--build-arg", "RUNNER_BASE="+base, "testdata/runner")
	build.Env = minimalEnv()
	build.Stderr = os.Stderr
	out, e := build.Output()
	if e != nil {
		t.Fatal("cannot build disposable runner fixture:", e)
	}
	imageID := strings.TrimSpace(string(out))
	ins, e := cli.ImageInspect(ctx, tag)
	if e != nil {
		t.Fatal(e)
	}
	imageID = ins.ID
	defer cli.ImageRemove(context.Background(), tag, image.RemoveOptions{Force: true})
	for _, mode := range []Mode{Plain, DinD} {
		t.Run(string(mode), func(t *testing.T) {
			p := c.Pools[0]
			p.Image = imageID
			p.Resources = Resources{CPU: 1, MemoryMiB: 512}
			p.Mode = mode
			if mode == DinD {
				p.DaemonImage = daemon
				p.DaemonResources = Resources{CPU: 1, MemoryMiB: 768}
			}
			snap := s.View()
			id := newID()
			r := Runner{ID: id, Name: "runmoor-" + id, Phase: Preparing, Backend: Docker, Resources: p.Cost(), Image: p.Image}
			driver := &DockerDriver{}
			// A foreign volume proves cleanup never sweeps by a broad name prefix.
			foreign, e := cli.VolumeCreate(ctx, volume.CreateOptions{Name: r.Name + "-foreign", Labels: map[string]string{ownerKey: snap.Installation, runnerKey: newID()}})
			if e != nil {
				t.Fatal(e)
			}
			defer cli.VolumeRemove(context.Background(), foreign.Name, true)
			defer func() {
				clean, stop := context.WithTimeout(context.Background(), 45*time.Second)
				defer stop()
				_ = driver.Stop(clean, c, r, snap)
				if e := driver.Cleanup(clean, c, r, snap); e != nil {
					t.Error(e)
				}
			}()
			if e = driver.Prepare(ctx, c, p, r, snap, "fixture-jit", func(h Handle) error { r.Handle = h; return nil }); e != nil {
				t.Fatal(e)
			}
			actual, e := cli.ContainerInspect(ctx, r.Handle.Container)
			if e != nil {
				t.Fatal(e)
			}
			if actual.HostConfig.Memory != 512*1024*1024 || actual.HostConfig.NanoCPUs != 1e9 || actual.HostConfig.LogConfig.Type != "none" {
				t.Fatal("runner limits/log policy not applied")
			}
			encoded, _ := json.Marshal(actual.Config)
			if strings.Contains(string(encoded), "fixture-jit") {
				t.Fatal("JIT leaked into inspectable container configuration")
			}
			for _, m := range actual.Mounts {
				if m.Type != "volume" {
					t.Fatal("host bind mount reached runner")
				}
			}
			if e = integrationExec(ctx, cli, r.Handle.Container, `cd /home/runner/_work; echo marker > marker; test "$(cat marker)" = marker`); e != nil {
				t.Fatal(e)
			}
			if mode == DinD {
				daemonInfo, e := cli.ContainerInspect(ctx, r.Handle.Daemon)
				if e != nil {
					t.Fatal(e)
				}
				if daemonInfo.HostConfig.Memory != 768*1024*1024 || daemonInfo.HostConfig.NanoCPUs != 1e9 || daemonInfo.HostConfig.CgroupnsMode != container.CgroupnsModePrivate {
					t.Fatal("daemon resource envelope is not enforced")
				}
				if e = integrationExec(ctx, cli, r.Handle.Container, dindProbe); e != nil {
					t.Fatal(e)
				}
			}
			// A new adapter instance adopts the still-live owned environment without
			// access to any in-memory provisioning handle or JIT credential.
			recovered := r
			recovered.Handle = Handle{}
			obs, e := (&DockerDriver{}).Inspect(ctx, c, recovered, snap)
			if e != nil || !obs.Running || obs.Handle.Container != r.Handle.Container {
				t.Fatal("restart adoption failed", e)
			}
			if e = driver.Stop(ctx, c, r, snap); e != nil {
				t.Fatal(e)
			}
			if e = driver.Cleanup(ctx, c, r, snap); e != nil {
				t.Fatal(e)
			}
			if e = driver.Cleanup(ctx, c, r, snap); e != nil {
				t.Fatal("cleanup is not idempotent", e)
			}
			containers, e := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: dockerFilter(snap, r)})
			if e != nil || len(containers) != 0 {
				t.Fatal("execution containers leaked")
			}
			vols, e := cli.VolumeList(ctx, volume.ListOptions{Filters: dockerFilter(snap, r)})
			if e != nil || len(vols.Volumes) != 0 {
				t.Fatal("execution volumes leaked")
			}
			nets, e := cli.NetworkList(ctx, network.ListOptions{Filters: dockerFilter(snap, r)})
			if e != nil || len(nets) != 0 {
				t.Fatal("execution networks leaked")
			}
			if _, e = cli.VolumeInspect(ctx, foreign.Name); e != nil {
				t.Fatal("foreign resource was removed")
			}
		})
	}
}
func integrationExec(ctx context.Context, cli *client.Client, id, script string) error {
	v, e := cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: []string{"/bin/sh", "-c", script}, AttachStdout: true, AttachStderr: true})
	if e != nil {
		return e
	}
	conn, e := cli.ContainerExecAttach(ctx, v.ID, container.ExecAttachOptions{})
	if e != nil {
		return e
	}
	defer conn.Close()
	buf := &boundedBuffer{Limit: 8192}
	_, e = stdcopy.StdCopy(buf, buf, conn.Reader)
	if e != nil {
		return e
	}
	state, e := cli.ContainerExecInspect(ctx, v.ID)
	if e != nil {
		return e
	}
	if state.ExitCode != 0 {
		return &Problem{Code: ErrDependency, Message: "Local integration probe failed: " + buf.String(), Recovery: "Inspect the isolated test fixture."}
	}
	return nil
}

const dindProbe = `set -eu
cd /home/runner/_work
docker pull busybox:1.36 >/dev/null
mkdir build-context
printf 'FROM busybox:1.36\nRUN echo built > /proof\nCMD ["cat", "/proof"]\n' > build-context/Dockerfile
docker build -t runmoor-local-test build-context >/dev/null
[ "$(docker run --rm runmoor-local-test)" = built ]
docker run --rm -v /home/runner/_work:/github/workspace -v /home/runner/externals:/__e:ro busybox:1.36 sh -c 'test "$(cat /github/workspace/marker)" = marker; test -n "$(ls -A /__e)"'
docker network create runmoor-local-job >/dev/null
docker run -d --name runmoor-local-service --network runmoor-local-job -p 18080:8080 busybox:1.36 sh -c 'mkdir /www; echo service-ok > /www/index.html; httpd -f -p 8080 -h /www' >/dev/null
docker run --rm --network runmoor-local-job busybox:1.36 wget -qO- http://runmoor-local-service:8080 | grep service-ok
curl --retry 5 --retry-connrefused -fsS http://127.0.0.1:18080 | grep service-ok
# Leave a nested container alive to prove parent termination destroys it and
# its daemon storage rather than leaking a reusable local build cache.
docker run -d --name runmoor-leftover busybox:1.36 sleep 300 >/dev/null
test "$(docker info --format '{{.CgroupVersion}}')" = 2
`
