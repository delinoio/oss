package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeRunnerBuilder struct {
	prepare func(context.Context, Config, Pool, RunnerArtifact, RunnerRelease) (Pool, error)
	cleanup func(context.Context, Config, RunnerArtifact) error
	calls   int
	cleaned []string
}

func (f *fakeRunnerBuilder) Prepare(ctx context.Context, c Config, p Pool, a RunnerArtifact, r RunnerRelease) (Pool, error) {
	f.calls++
	if f.prepare != nil {
		return f.prepare(ctx, c, p, a, r)
	}
	p.Image = "example/managed@sha256:" + strings.Repeat("b", 64)
	p.RunnerVersion = r.Version()
	p.ImageSource = nil
	return p, nil
}
func (f *fakeRunnerBuilder) Cleanup(ctx context.Context, c Config, a RunnerArtifact) error {
	if f.cleanup != nil {
		if err := f.cleanup(ctx, c, a); err != nil {
			return err
		}
	}
	f.cleaned = append(f.cleaned, a.ID)
	return nil
}
func managedFixture(t *testing.T) (*Manager, Config, *fakeRunnerBuilder, string) {
	t.Helper()
	m, c, _, _, id := testManager(t)
	c.Pools[0].RunnerVersion = LatestRunner
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunnerBuilder{}
	m.RunnerBuilder = f
	m.ReleaseClient = &http.Client{Transport: runnerReleaseTransport(func(req *http.Request) (*http.Response, error) { return nil, errors.New("unexpected network fixture") })}
	setManagedReleases(t, m, []RunnerRelease{{Tag: "v2.338.0", Published: time.Now().Add(-time.Hour)}, {Tag: "v2.337.0", Published: time.Now().Add(-30 * 24 * time.Hour)}})
	return m, c, f, id
}
func setManagedReleases(t *testing.T, m *Manager, releases []RunnerRelease) {
	t.Helper()
	if err := m.Store.Update(func(s *Snapshot) error { s.Releases = releases; s.ReleaseChecked = nowUTC(); return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestManagedUpdatePreservesExecutionGeneration(t *testing.T) {
	m, c, b, old := managedFixture(t)
	r := Runner{ID: newID(), PoolID: old, Generation: m.Store.View().Pools[old].Generation, Backend: Docker, Image: c.Pools[0].Image, Resources: c.Pools[0].Resources, Phase: Busy, Deadline: time.Now().Add(time.Hour)}
	if err := m.Store.Update(func(s *Snapshot) error { s.Runners[r.ID] = &r; return nil }); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	q := s.Managed["linux"]
	if b.calls != 1 || q.Current == nil || q.Current.RunnerVersion != "2.338.0" || s.Pools[old].Phase != Draining {
		t.Fatalf("update not activated: %+v %+v", q, s.Pools[old])
	}
	if fingerprint(s.Runners[r.ID]) != fingerprint(r) || s.Generations[r.Generation].Pools[0].RunnerVersion != "2.337.0" {
		t.Fatal("running job lost its immutable configuration")
	}
	if s.Requested.Pools[0].RunnerVersion != LatestRunner || s.Config.Pools[0].RunnerVersion != "2.338.0" {
		t.Fatal("requested and effective state were conflated")
	}
	m.updateManaged(context.Background(), "linux")
	if b.calls != 1 {
		t.Fatal("rebuilt an unchanged candidate")
	}
}
func TestManagedFailureFallbackAndKnownExpiry(t *testing.T) {
	m, _, b, id := managedFixture(t)
	b.prepare = func(context.Context, Config, Pool, RunnerArtifact, RunnerRelease) (Pool, error) {
		return Pool{}, problem(ErrImage, "Fixture candidate failed.", "Retry.")
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	q := s.Managed["linux"]
	if q.Current.RunnerVersion != "2.337.0" || q.Phase != UpdateRetry || q.Problem == nil || len(s.Artifacts) != 0 || !q.NextCheck.After(time.Now()) {
		t.Fatalf("lost fallback or retry: %+v", q)
	}
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[id].ScaleSetID = 1
		s.Pools[id].Session = "fixture"
		s.Managed["linux"].Expires = time.Now().Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s = m.Store.View()
	if eligible(s, s.Pools[id]) {
		t.Fatal("expired runner can acquire work")
	}
	// Unknown freshness preserves the last verified image and the known deadline.
	before := q.Current.Image
	if err := m.Store.Update(func(s *Snapshot) error { s.ReleaseChecked = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	if m.Store.View().Managed["linux"].Current.Image != before {
		t.Fatal("network failure replaced the working image")
	}
}
func TestManagedUpdateHonorsConcurrentPauseStopAndReload(t *testing.T) {
	for _, action := range []string{"pause", "stop", "reload"} {
		t.Run(action, func(t *testing.T) {
			m, c, b, _ := managedFixture(t)
			b.prepare = func(_ context.Context, _ Config, p Pool, _ RunnerArtifact, r RunnerRelease) (Pool, error) {
				if err := m.Store.Update(func(s *Snapshot) error {
					switch action {
					case "pause":
						s.Managed["linux"].Paused = true
					case "stop":
						s.Stopping = true
					case "reload":
						s.Managed["linux"].DesiredHash = "replacement"
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				p.Image = "sha256:" + strings.Repeat("b", 64)
				p.RunnerVersion = r.Version()
				return p, nil
			}
			m.updateManaged(context.Background(), "linux")
			s := m.Store.View()
			if s.Managed["linux"].Current.Image != c.Pools[0].Image || s.Config.Pools[0].RunnerVersion != "2.337.0" {
				t.Fatal("stale update activated")
			}
		})
	}
}
func TestManagedPreparationReservesNextCapacityAndRecoversCrash(t *testing.T) {
	m, c, b, id := managedFixture(t)
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Config.Host.MaxRunners = 1
		s.Requested.Host.MaxRunners = 1
		s.Runners["busy"] = &Runner{ID: "busy", PoolID: id, Phase: Busy, Backend: Docker, Resources: c.Pools[0].Resources}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	if b.calls != 0 || s.Managed["linux"].Phase != UpdateWaiting || len(Schedule(s)) != 0 {
		t.Fatal("update did not reserve priority")
	}
	if err := m.Store.Update(func(s *Snapshot) error { delete(s.Runners, "busy"); return nil }); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	if b.calls != 1 {
		t.Fatal("freed slot did not serve the update")
	}
	orphan := RunnerArtifact{ID: newID(), Pool: "linux", Backend: Docker, Phase: ArtifactPreparing, Reserved: true, Resources: Resources{1, 128}}
	if err := m.Store.Update(func(s *Snapshot) error { s.Artifacts[orphan.ID] = &orphan; return nil }); err != nil {
		t.Fatal(err)
	}
	b.cleanup = func(context.Context, Config, RunnerArtifact) error {
		return problem(ErrOwnership, "Fixture ownership mismatch.", "Inspect.")
	}
	if err := m.cleanupManaged(context.Background()); err == nil {
		t.Fatal("lost uncertain cleanup")
	}
	if !m.Store.View().Artifacts[orphan.ID].Reserved {
		t.Fatal("released reservation before termination")
	}
	b.cleanup = nil
	if err := m.cleanupManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Store.View().Artifacts[orphan.ID] != nil {
		t.Fatal("did not reconcile interrupted preparation")
	}
}
func TestManagedMetadataIsSharedAndReleaseOrderIsSemantic(t *testing.T) {
	m, _, _, _ := managedFixture(t)
	calls := 0
	m.ReleaseClient = &http.Client{Transport: runnerReleaseTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := json.Marshal([]RunnerRelease{{Tag: "v2.9.0", Published: time.Now()}, {Tag: "v2.10.0", Published: time.Now().Add(-time.Hour)}, {Tag: "v9.0.0", Published: time.Now(), Prerelease: true}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	if err := m.Store.Update(func(s *Snapshot) error { s.ReleaseChecked = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	m.updateManaged(context.Background(), "linux")
	if calls != 1 || m.Store.View().Managed["linux"].Current.RunnerVersion != "2.10.0" {
		t.Fatal("release resolution was duplicated or lexical")
	}
}
func TestManagedPendingPoolPauseAndResume(t *testing.T) {
	m, c, _, _, _ := testManager(t)
	c.Pools[0].Name = "new"
	c.Pools[0].ScaleSet = "new"
	c.Pools[0].Image = ""
	c.Pools[0].RunnerVersion = LatestRunner
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause("new"); err != nil {
		t.Fatal(err)
	}
	if !m.Store.View().Managed["new"].Paused {
		t.Fatal("pending pool pause lost")
	}
	if err := m.Resume(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
	if m.Store.View().Managed["new"].Paused {
		t.Fatal("pending pool did not resume")
	}
}
