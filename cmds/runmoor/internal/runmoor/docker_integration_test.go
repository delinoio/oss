package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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
		stream, e := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
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
	defer cli.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true})
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
			snap.Pools["integration"] = &PoolState{ID: "integration", Spec: p}
			id := newID()
			r := Runner{ID: id, PoolID: "integration", Name: "runmoor-" + id, Phase: Preparing, Backend: Docker, Resources: p.Cost(), Image: p.Image}
			driver := &DockerDriver{}
			// A foreign volume proves cleanup never sweeps by a broad name prefix.
			foreign, e := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: r.Name + "-foreign", Labels: map[string]string{ownerKey: snap.Installation, runnerKey: newID()}})
			if e != nil {
				t.Fatal(e)
			}
			defer cli.VolumeRemove(context.Background(), foreign.Volume.Name, client.VolumeRemoveOptions{Force: true})
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
			actual, e := cli.ContainerInspect(ctx, r.Handle.Container, client.ContainerInspectOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if actual.Container.HostConfig.Memory != 512*1024*1024 || actual.Container.HostConfig.NanoCPUs != 1e9 || actual.Container.HostConfig.LogConfig.Type != "none" {
				t.Fatal("runner limits/log policy not applied")
			}
			encoded, _ := json.Marshal(actual.Container.Config)
			if strings.Contains(string(encoded), "fixture-jit") {
				t.Fatal("JIT leaked into inspectable container configuration")
			}
			for _, m := range actual.Container.Mounts {
				if m.Type != "volume" {
					t.Fatal("host bind mount reached runner")
				}
			}
			if e = integrationExec(ctx, cli, r.Handle.Container, `cd /home/runner/_work; echo marker > marker; test "$(cat marker)" = marker`); e != nil {
				t.Fatal(e)
			}
			if mode == DinD {
				daemonInfo, e := cli.ContainerInspect(ctx, r.Handle.Daemon, client.ContainerInspectOptions{})
				if e != nil {
					t.Fatal(e)
				}
				if daemonInfo.Container.HostConfig.Memory != 768*1024*1024 || daemonInfo.Container.HostConfig.NanoCPUs != 1e9 || daemonInfo.Container.HostConfig.CgroupnsMode != container.CgroupnsModePrivate {
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
			if _, e = cli.ContainerPause(ctx, r.Handle.Container, client.ContainerPauseOptions{}); e != nil {
				t.Fatal(e)
			}
			obs, e = driver.Inspect(ctx, c, r, snap)
			if e != nil || obs.Running || !obs.Exists {
				t.Fatal("paused runner remained available", e)
			}
			if _, e = cli.ContainerUnpause(ctx, r.Handle.Container, client.ContainerUnpauseOptions{}); e != nil {
				t.Fatal(e)
			}
			obs, e = driver.Inspect(ctx, c, r, snap)
			if e != nil || !obs.Running {
				t.Fatal("unpaused runner was not recovered", e)
			}
			if mode == DinD {
				if obs.Handle.Daemon != r.Handle.Daemon {
					t.Fatal("restart adoption lost the owned daemon")
				}
				if _, e = cli.ContainerStop(ctx, r.Handle.Daemon, client.ContainerStopOptions{}); e != nil {
					t.Fatal(e)
				}
				obs, e = driver.Inspect(ctx, c, r, snap)
				actual, inspectErr := cli.ContainerInspect(ctx, r.Handle.Container, client.ContainerInspectOptions{})
				if e != nil || inspectErr != nil || obs.Running || !actual.Container.State.Running {
					t.Fatal("live runner remained available without its DinD daemon", e, inspectErr)
				}
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
			containers, e := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: dockerFilter(snap, r)})
			if e != nil || len(containers.Items) != 0 {
				t.Fatal("execution containers leaked")
			}
			vols, e := cli.VolumeList(ctx, client.VolumeListOptions{Filters: dockerFilter(snap, r)})
			if e != nil || len(vols.Items) != 0 {
				t.Fatal("execution volumes leaked")
			}
			nets, e := cli.NetworkList(ctx, client.NetworkListOptions{Filters: dockerFilter(snap, r)})
			if e != nil || len(nets.Items) != 0 {
				t.Fatal("execution networks leaked")
			}
			if _, e = cli.VolumeInspect(ctx, foreign.Volume.Name, client.VolumeInspectOptions{}); e != nil {
				t.Fatal("foreign resource was removed")
			}
		})
	}
	t.Run("force stop preserves foreign name replacement", func(t *testing.T) {
		m, config, remote, _, pool := testManager(t)
		id := seedRunner(t, m, pool, Busy)
		snap := m.Store.View()
		r := snap.Runners[id]
		makeContainer := func(labels map[string]string) string {
			t.Helper()
			v, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: &container.Config{
				Image: imageID, Entrypoint: []string{"/bin/sh"}, Cmd: []string{"-c", "sleep 3600"}, Labels: labels,
			}, HostConfig: &container.HostConfig{NetworkMode: "none", LogConfig: container.LogConfig{Type: "none"}, Resources: container.Resources{NanoCPUs: 1e9, Memory: 128 * 1024 * 1024}}, Name: r.Name})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = cli.ContainerRemove(context.Background(), v.ID, client.ContainerRemoveOptions{Force: true})
			})
			if _, err = cli.ContainerStart(ctx, v.ID, client.ContainerStartOptions{}); err != nil {
				t.Fatal(err)
			}
			return v.ID
		}
		original := makeContainer(dockerLabels(snap, *r, "runner"))
		config.DockerSocket = endpoint
		if err := m.Store.Update(func(s *Snapshot) error {
			s.Generations[r.Generation] = config
			s.Runners[id].Handle.Container = original
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.ContainerRemove(ctx, original, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		labels := dockerLabels(snap, *r, "runner")
		labels[ownerKey] = newID()
		replacement := makeContainer(labels)
		m.Drivers = func(Backend) (Driver, error) { return &DockerDriver{}, nil }
		if err := m.Stop(true); err != nil {
			t.Fatal(err)
		}
		m.cleanup(ctx, id)
		r = m.Store.View().Runners[id]
		requireCode(t, r.Problem, ErrOwnership)
		if r.Phase != Quarantined || r.Terminated || r.LocalCleaned || remote.removed != 0 {
			t.Fatal("force stop released ambiguous execution ownership", r)
		}
		if _, count, _ := usage(m.Store.View()); count != 1 {
			t.Fatal("foreign replacement released the reservation")
		}
		live, err := cli.ContainerInspect(ctx, replacement, client.ContainerInspectOptions{})
		if err != nil || !live.Container.State.Running {
			t.Fatal("force stop mutated the foreign replacement", err)
		}
		// Only this test's creator removes the foreign container. Confirmed
		// absence then permits the manager to finish its original cleanup.
		if _, err = cli.ContainerRemove(ctx, replacement, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		m.cleanup(ctx, id)
		if got := m.Store.View().Runners[id]; got.Phase != Completed || !got.LocalCleaned {
			t.Fatal("cleanup did not recover after confirmed absence", got)
		}
	})
	t.Run("early runner exits suspend the pool", func(t *testing.T) {
		c.Pools[0].Image = imageID
		c.Pools[0].RunnerPath = "/home/runner/failing"
		c.Pools[0].Resources = Resources{CPU: 1, MemoryMiB: 512}
		m := NewManager(s, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer m.cancel()
		m.RemoteFactory = func(Connection) (Remote, error) { return &fakeRemote{}, nil }
		if err := m.activate(c); err != nil {
			t.Fatal(err)
		}
		var pool string
		for id := range s.View().Pools {
			pool = id
		}
		for attempt := 1; attempt <= 3; attempt++ {
			id := seedRunner(t, m, pool, Preparing)
			defer m.cleanup(context.Background(), id)
			m.prepare(ctx, id)
			snap := s.View()
			if snap.Runners[id].Phase != Cleaning || snap.Pools[pool].PreparationFailures != attempt {
				t.Fatal("early exit reset the preparation failure counter")
			}
			requireCode(t, snap.Runners[id].Problem, ErrPreparation)
			m.cleanup(ctx, id)
			if s.View().Runners[id].Phase != Completed {
				t.Fatal("failed startup leaked owned Docker resources")
			}
		}
		if s.View().Pools[pool].Phase != Suspended {
			t.Fatal("broken custom image bypassed pool suspension")
		}
	})
}
func integrationExec(ctx context.Context, cli *client.Client, id, script string) error {
	v, e := cli.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: []string{"/bin/sh", "-c", script}, AttachStdout: true, AttachStderr: true})
	if e != nil {
		return e
	}
	conn, e := cli.ExecAttach(ctx, v.ID, client.ExecAttachOptions{})
	if e != nil {
		return e
	}
	defer conn.Close()
	buf := &boundedBuffer{Limit: 8192}
	_, e = stdcopy.StdCopy(buf, buf, conn.Reader)
	if e != nil {
		return e
	}
	state, e := cli.ExecInspect(ctx, v.ID, client.ExecInspectOptions{})
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
# Nested action/service output must not be retained in daemon storage, even
# when interrupted cleanup keeps that storage available for recovery.
test "$(docker info --format '{{.LoggingDriver}}')" = none
docker run --name runmoor-log-probe busybox:1.36 sh -c 'echo private-output; echo private-error >&2' >/dev/null 2>&1
test "$(docker inspect --format '{{.HostConfig.LogConfig.Type}}' runmoor-log-probe)" = none
test -z "$(docker inspect --format '{{.LogPath}}' runmoor-log-probe)"
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
