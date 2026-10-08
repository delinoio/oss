package runmoor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type artifactEngineFixture struct {
	endpoint           string
	alive, unavailable atomic.Bool
	calls              atomic.Int32
}

func artifactEngine(t *testing.T, c Config, installation, id string, owned bool) *artifactEngineFixture {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(c.Storage.State, newID()[30:]+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	f := &artifactEngineFixture{endpoint: "unix://" + listener.Addr().String()}
	f.alive.Store(owned)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.51")
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1.51")
		if path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		f.calls.Add(1)
		if f.unavailable.Load() {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"private-engine-response"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/"):
			if !f.alive.Load() {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "original-container", "Config": map[string]any{"Labels": map[string]string{ownerKey: installation, artifactKey: id}}})
		case r.Method == http.MethodDelete && path == "/containers/original-container":
			f.alive.Store(false)
			w.WriteHeader(204)
		default:
			t.Error("unexpected engine request", r.Method, path)
			w.WriteHeader(500)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return f
}

func seedArtifactEndpoint(t *testing.T, m *Manager, c Config, a RunnerArtifact, endpoint string) {
	t.Helper()
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; s.DockerArtifactEndpoint = endpoint; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDockerEndpointRejectsReloadAndRestartBeforeActivation(t *testing.T) {
	for _, phase := range []ArtifactPhase{ArtifactPreparing, ArtifactRemoving, ArtifactReady} {
		t.Run(string(phase), func(t *testing.T) {
			m, c, _, _ := managedFixture(t)
			a := RunnerArtifact{ID: newID(), Backend: Docker, Pool: "linux", Phase: phase, Reserved: true, Resources: Resources{1, 128}, Container: "original-container"}
			engineA := artifactEngine(t, c, m.Store.View().Installation, a.ID, true)
			engineB := artifactEngine(t, c, m.Store.View().Installation, a.ID, false)
			c.DockerSocket = engineA.endpoint
			seedArtifactEndpoint(t, m, c, a, engineA.endpoint)
			// Ready/current and previous references must retain the same origin guard.
			if phase == ArtifactReady {
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Managed["linux"].CurrentArtifact = a.ID
					s.Managed["linux"].PreviousArtifact = a.ID
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			before := m.Store.View()
			next := c
			next.DockerSocket = engineB.endpoint
			writeSuspensionReload(t, m, next)
			requireCode(t, m.Reload(context.Background()), ErrOwnership)
			if !reflect.DeepEqual(before, m.Store.View()) {
				t.Fatal("rejected reload changed authority or reservation")
			}
			requireCode(t, m.initializeRun(next), ErrOwnership)
			if !reflect.DeepEqual(before, m.Store.View()) {
				t.Fatal("rejected startup changed authority or reservation")
			}
			if err := m.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(next)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			restarted := NewManager(reopened, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer restarted.cancel()
			requireCode(t, restarted.initializeRun(next), ErrOwnership)
			if !reflect.DeepEqual(before, reopened.View()) {
				t.Fatal("cold restart changed original authority")
			}
			if engineA.calls.Load() != 0 || engineB.calls.Load() != 0 || !engineA.alive.Load() {
				t.Fatal("rejected endpoint contacted an engine or lost original resource")
			}
			body, err := json.Marshal(statusOf(before, true))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "docker_artifact_endpoint") || strings.Contains(string(body), engineA.endpoint) {
				t.Fatal("private endpoint leaked into public status")
			}
		})
	}
}

func TestManagedDockerCleanupUsesRetainedEngineAndPreservesUnavailableOrigin(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			m, c, _, _ := managedFixture(t)
			a := RunnerArtifact{ID: newID(), Backend: Docker, Pool: "removed", Phase: ArtifactRemoving, Reserved: true, Resources: Resources{1, 128}, Container: "original-container"}
			engineA := artifactEngine(t, c, m.Store.View().Installation, a.ID, true)
			engineB := artifactEngine(t, c, m.Store.View().Installation, a.ID, false)
			c.DockerSocket = ""
			t.Setenv("DOCKER_HOST", engineA.endpoint)
			seedArtifactEndpoint(t, m, c, a, engineA.endpoint)
			t.Setenv("DOCKER_HOST", engineB.endpoint)
			engineA.unavailable.Store(unavailable)
			m.RunnerBuilder = &ManagedImageBuilder{Store: m.Store}
			err := m.cleanupManaged(context.Background())
			if unavailable {
				requireCode(t, err, ErrDependency)
				retained := m.Store.View().Artifacts[a.ID]
				if retained == nil || !retained.Reserved || !engineA.alive.Load() {
					t.Fatal("unavailable original engine lost ownership")
				}
				if strings.Contains(err.Error(), "private-engine-response") {
					t.Fatal("raw engine error exposed")
				}
			} else if err != nil || m.Store.View().Artifacts[a.ID] != nil || engineA.alive.Load() {
				t.Fatalf("original cleanup not confirmed: %v", err)
			}
			if engineB.calls.Load() != 0 || engineA.calls.Load() == 0 {
				t.Fatal("cleanup selected current ambient engine")
			}
		})
	}
}

func TestManagedDockerEndpointPinPrecedesReservationAndSurvivesRestart(t *testing.T) {
	m, c, b, _ := managedFixture(t)
	c.DockerSocket = ""
	t.Setenv("DOCKER_HOST", "unix:///original-engine.sock")
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	b.prepare = func(_ context.Context, buildConfig Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
		snapshot := m.Store.View()
		if snapshot.DockerArtifactEndpoint != "unix:///original-engine.sock" || snapshot.Artifacts[a.ID] == nil || !snapshot.Artifacts[a.ID].Reserved || buildConfig.DockerSocket != snapshot.DockerArtifactEndpoint {
			t.Fatal("reservation or builder lacks durable origin pin")
		}
		t.Setenv("DOCKER_HOST", "unix:///replacement-engine.sock")
		p.Image = "example/managed@sha256:" + strings.Repeat("b", 64)
		p.ImageSource = nil
		p.RunnerVersion = r.Version()
		return p, nil
	}
	m.updateManaged(context.Background(), "linux")
	before := m.Store.View()
	if len(before.Artifacts) != 1 || before.DockerArtifactEndpoint != "unix:///original-engine.sock" || before.Requested.DockerSocket != "" {
		t.Fatal("pin changed authored/public configuration")
	}
	requireCode(t, m.activate(c), ErrOwnership)
	if err := m.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := NewManager(reopened, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer restarted.cancel()
	requireCode(t, restarted.initializeRun(c), ErrOwnership)
	if !reflect.DeepEqual(before, reopened.View()) {
		t.Fatal("restart changed committed ownership")
	}
}

func TestManagedDockerEndpointAllowsEmptySwitchAndRejectsUnknownLegacyOrigin(t *testing.T) {
	m, c, b, _ := managedFixture(t)
	a := RunnerArtifact{ID: newID(), Backend: Docker, Phase: ArtifactRemoving, Reserved: true, Container: "legacy-container"}
	if err := m.Store.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	before := m.Store.View()
	next := c
	next.DockerSocket = "unix:///different-engine.sock"
	writeSuspensionReload(t, m, next)
	requireCode(t, m.Reload(context.Background()), ErrOwnership)
	requireCode(t, m.activate(next), ErrOwnership)
	requireCode(t, m.cleanupManaged(context.Background()), ErrOwnership)
	if len(b.cleaned) != 0 || m.Store.View().Artifacts[a.ID] == nil || !m.Store.View().Artifacts[a.ID].Reserved {
		t.Fatal("legacy unknown origin adopted current engine")
	}
	if fingerprint(m.Store.View().Config) != fingerprint(before.Config) || fingerprint(m.Store.View().Requested) != fingerprint(before.Requested) {
		t.Fatal("rejected legacy switch changed configuration")
	}
	if err := m.Store.Update(func(s *Snapshot) error { delete(s.Artifacts, a.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Store.View().Requested.DockerSocket != next.DockerSocket {
		t.Fatal("empty endpoint switch was rejected")
	}
	if err := m.initializeRun(next); err != nil {
		t.Fatal(err)
	}
	setManagedReleases(t, m, m.Store.View().Releases)
	b.prepare = func(_ context.Context, buildConfig Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
		if m.Store.View().DockerArtifactEndpoint != next.DockerSocket || buildConfig.DockerSocket != next.DockerSocket {
			t.Fatal("empty switch did not establish new artifact authority")
		}
		p.Image = "example/managed@sha256:" + strings.Repeat("b", 64)
		p.ImageSource = nil
		p.RunnerVersion = r.Version()
		return p, nil
	}
	m.updateManaged(context.Background(), "linux")
	if len(m.Store.View().Artifacts) != 1 || m.Store.View().DockerArtifactEndpoint != next.DockerSocket {
		t.Fatal("new engine reservation was unavailable after empty switch")
	}
}

func TestManagedDockerEndpointGuardsAmbientContextChanges(t *testing.T) {
	m, c, _, _ := managedFixture(t)
	c.DockerSocket = ""
	t.Setenv("DOCKER_HOST", "")
	directory := t.TempDir()
	script := "#!/bin/sh\ncase \"$5\" in\noriginal) printf '%s' '\"unix:///original-context.sock\"';;\n*) printf '%s' '\"unix:///replacement-context.sock\"';;\nesac\n"
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("DOCKER_CONTEXT", "original")
	endpoint, err := dockerEndpoint(context.Background(), c)
	if err != nil || endpoint != "unix:///original-context.sock" {
		t.Fatalf("fixture context: %q %v", endpoint, err)
	}
	a := RunnerArtifact{ID: newID(), Backend: Docker, Phase: ArtifactReady}
	seedArtifactEndpoint(t, m, c, a, endpoint)
	before := m.Store.View()
	t.Setenv("DOCKER_CONTEXT", "replacement")
	writeSuspensionReload(t, m, c)
	requireCode(t, m.Reload(context.Background()), ErrOwnership)
	requireCode(t, m.activate(c), ErrOwnership)
	if !reflect.DeepEqual(before, m.Store.View()) {
		t.Fatal("context switch changed durable state")
	}
}

func TestManagedDockerReloadRechecksResolvedCandidateAuthority(t *testing.T) {
	m, c, _, _ := managedFixture(t)
	a := RunnerArtifact{ID: newID(), Backend: Docker, Phase: ArtifactReady}
	seedArtifactEndpoint(t, m, c, a, c.DockerSocket)
	before := m.Store.View()
	m.ResolveCapacity = func(_ context.Context, c Config) (Config, error) {
		c.DockerSocket = "unix:///different-resolved-engine.sock"
		return c, nil
	}
	writeSuspensionReload(t, m, c)
	requireCode(t, m.Reload(context.Background()), ErrOwnership)
	if !reflect.DeepEqual(before, m.Store.View()) {
		t.Fatal("resolved candidate changed original authority")
	}
}
