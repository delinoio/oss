package runmoor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if err := m.Store.Update(func(s *Snapshot) error { s.Artifacts[orphan.ID].NextCleanup = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
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

func TestManagedScopedResumeKeepsOtherPendingPoolsPaused(t *testing.T) {
	m, c, _, _, _ := testManager(t)
	for _, name := range []string{"first", "second"} {
		p := c.Pools[0]
		p.Name, p.ScaleSet, p.Image, p.RunnerVersion = name, name, "", LatestRunner
		c.Pools = append(c.Pools, p)
	}
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(""); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if s.Paused || s.Managed["first"].Paused || !s.Managed["second"].Paused {
		t.Fatal("scoped resume unpaused another pending pool")
	}
}

func TestPausedManagedPoolChecksReleasesWithoutPreparing(t *testing.T) {
	m, _, builder, _ := managedFixture(t)
	if err := m.Pause(""); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	q := s.Managed["linux"]
	if !s.Paused || builder.calls != 0 || q.Current.RunnerVersion != "2.337.0" || q.CandidateVersion != "2.338.0" || q.LastCheck.IsZero() || !q.NextCheck.After(time.Now()) {
		t.Fatal("paused release checks changed execution or lost freshness")
	}
}

func TestManagedImportedSourceSurvivesPreparationRetry(t *testing.T) {
	c, s := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	p := c.Pools[0]
	p.Backend, p.Image, p.RunnerVersion, p.RunnerPath = Tart, "", LatestRunner, "/Users/runner/actions-runner"
	p.ImageSource = &ImageSource{From: "/fixture.tvm"}
	c.Pools = []Pool{p}
	c.Host.MaxRunners = 1
	a := RunnerArtifact{ID: newID(), Pool: p.Name, Backend: Tart, Phase: ArtifactPreparing, Resources: p.Resources, Reserved: true, CreatedAt: nowUTC()}
	if err := s.Update(func(s *Snapshot) error {
		s.Config = c
		initializeManaged(s, c)
		s.Artifacts[a.ID] = &a
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	builder := &ManagedImageBuilder{Store: s, Images: images}
	base, err := builder.importTartSource(context.Background(), c, p, a)
	if err != nil {
		t.Fatal(err)
	}
	frozen := s.View().Images[base]
	if frozen.Phase != ImageImported || frozen.Digest == "" || s.View().Managed[p.Name].BaseImage != base {
		t.Fatal("import was not frozen before guest changes")
	}
	// Simulate a failed candidate and a later retry with the external source gone.
	if err = builder.Cleanup(context.Background(), c, *s.View().Artifacts[a.ID]); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(s *Snapshot) error { delete(s.Artifacts, a.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	a.ID = newID()
	if err = s.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	release, body := fixtureRelease(t, Tart, p.Arch)
	builder.Client = archiveClient(body)
	resolved, err := builder.Prepare(context.Background(), c, p, a, release)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Image == base || fingerprint(s.View().Images[base]) != fingerprint(frozen) {
		t.Fatal("retry modified its frozen source")
	}
	imports := 0
	fixture.mu.Lock()
	for _, cmd := range fixture.commands {
		if cmd[0] == "import" {
			imports++
		}
	}
	fixture.mu.Unlock()
	if imports != 1 {
		t.Fatalf("resolved the external source %d times", imports)
	}
	for _, action := range []string{"open", "seal", "remove"} {
		if _, err = images.Operate(context.Background(), c, ImageRequest{Action: action, ID: base}); err == nil {
			t.Fatalf("allowed %s of the active source", action)
		}
	}
}

func TestManagedUnreferencedReadyArtifactIsCollectedWithoutPools(t *testing.T) {
	m, _, builder, _ := managedFixture(t)
	a := RunnerArtifact{ID: newID(), Backend: Docker, Phase: ArtifactReady, Image: "sha256:" + strings.Repeat("c", 64)}
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Managed = nil
		s.Artifacts[a.ID] = &a
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.startManagedWork()
	m.wg.Wait()
	if len(builder.cleaned) != 1 || m.Store.View().Artifacts[a.ID] != nil {
		t.Fatal("orphan collection required a remaining managed pool")
	}
}

func TestDockerCapacityRecoveryBlocksOnlyDocker(t *testing.T) {
	m, c, _, _, id := testManager(t)
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Requested.DockerCapacityPending = true
		s.Config.DockerCapacityPending = true
		s.Pools[id].ScaleSetID, s.Pools[id].Session = 1, "fixture"
		other := *s.Pools[id]
		other.ID = "macos"
		other.Spec.Name, other.Spec.ScaleSet, other.Spec.Backend = "macos", "macos", Tart
		s.Pools[other.ID] = &other
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if eligible(s, s.Pools[id]) || !eligible(s, s.Pools["macos"]) {
		t.Fatal("pending engine capacity affected the wrong pools")
	}
	m.ResolveCapacity = func(context.Context, Config) (Config, error) { return Config{}, dockerProblem() }
	if err := m.retryDockerCapacity(context.Background()); err == nil || !m.Store.View().Config.DockerCapacityPending {
		t.Fatal("failed capacity probe released the wait")
	}
	m.ResolveCapacity = func(_ context.Context, c Config) (Config, error) {
		c.DockerCapacityPending = false
		c.DockerBudget = c.Pools[0].Cost()
		return c, nil
	}
	if err := m.retryDockerCapacity(context.Background()); err != nil {
		t.Fatal(err)
	}
	s = m.Store.View()
	if s.Config.DockerCapacityPending || s.Config.DockerBudget != c.Pools[0].Cost() || !eligible(s, s.Pools[id]) {
		t.Fatal("recovered capacity was not applied atomically")
	}
}

func TestManagedTartPreparesSeparateSealedRevision(t *testing.T) {
	c, s := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	source, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "source", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}
	source, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: source.ID, RunnerVersion: "2.337.0"})
	if err != nil {
		t.Fatal(err)
	}
	before := fingerprint(source)
	p := c.Pools[0]
	p.Backend = Tart
	p.Image = source.ID
	p.RunnerPath = source.RunnerPath
	p.RunnerVersion = LatestRunner
	a := RunnerArtifact{ID: newID(), Pool: p.Name, Backend: Tart, Phase: ArtifactPreparing, Resources: p.Resources, Reserved: true, CreatedAt: nowUTC()}
	if err = s.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	release, body := fixtureRelease(t, Tart, p.Arch)
	builder := &ManagedImageBuilder{Store: s, Images: images, Client: archiveClient(body)}
	result, err := builder.Prepare(context.Background(), c, p, a, release)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.View()
	candidate := snap.Images[result.Image]
	if result.Image == source.ID || result.RunnerVersion != "2.338.0" || candidate.Phase != ImageSealed || candidate.Digest == "" || fingerprint(snap.Images[source.ID]) != before {
		t.Fatal("source changed or candidate was not sealed")
	}
	fixture.mu.Lock()
	jit := fixture.jit
	running := fixture.running[candidate.VM]
	fixture.mu.Unlock()
	if jit != "" || running {
		t.Fatal("preparation registered a runner or left the VM running")
	}
	if err = builder.Cleanup(context.Background(), c, *snap.Artifacts[a.ID]); err != nil {
		t.Fatal(err)
	}
	if s.View().Images[source.ID] == nil || s.View().Images[a.ID] != nil {
		t.Fatal("cleanup removed the source or retained the candidate")
	}
}

func TestManagedForceStopCancelsBuilder(t *testing.T) {
	m, _, builder, _ := managedFixture(t)
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	builder.prepare = func(ctx context.Context, _ Config, _ Pool, _ RunnerArtifact, _ RunnerRelease) (Pool, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return Pool{}, ctx.Err()
	}
	if !m.startWork("runner-update", func(ctx context.Context) { m.updateManaged(ctx, "linux") }) {
		t.Fatal("worker did not start")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("builder did not start")
	}
	done := make(chan error, 1)
	go func() { done <- m.Stop(true) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("force stop did not cancel preparation")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not finish cleanup")
	}
	m.wg.Wait()
	s := m.Store.View()
	if !s.Stopping || len(s.Artifacts) != 0 || s.Config.Pools[0].RunnerVersion != "2.337.0" {
		t.Fatal("stop changed the active image or lost cleanup")
	}
}
func TestManagedReleaseFailuresShareRetryDeadline(t *testing.T) {
	m, _, _, _ := managedFixture(t)
	calls := 0
	m.ReleaseClient = &http.Client{Transport: runnerReleaseTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"600"}}, Body: io.NopCloser(strings.NewReader("fixture-secret"))}, nil
	})}
	if err := m.Store.Update(func(s *Snapshot) error { s.ReleaseChecked = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	if calls != 1 || time.Until(s.ReleaseRetryAt) < 9*time.Minute || s.ReleaseProblem == nil || strings.Contains(s.Managed["linux"].Problem.Error(), "fixture-secret") {
		t.Fatal("shared rate-limit/backoff or redaction failed")
	}
}
func TestManagedRetentionPreservesLiveAndPreviousImages(t *testing.T) {
	m, _, builder, _ := managedFixture(t)
	builder.prepare = func(_ context.Context, _ Config, p Pool, _ RunnerArtifact, r RunnerRelease) (Pool, error) {
		p.Image = "sha256:" + fingerprint(r.Version())
		p.RunnerVersion = r.Version()
		return p, nil
	}
	m.updateManaged(context.Background(), "linux")
	first := m.Store.View().Managed["linux"].CurrentArtifact
	for _, version := range []string{"2.339.0", "2.340.0"} {
		setManagedReleases(t, m, []RunnerRelease{{Tag: "v" + version, Published: nowUTC()}, {Tag: "v2.338.0", Published: nowUTC()}, {Tag: "v2.337.0", Published: nowUTC()}})
		m.updateManaged(context.Background(), "linux")
		if err := m.Store.Update(func(s *Snapshot) error {
			for _, p := range s.Pools {
				if p.Phase == Draining {
					p.Phase = Retired
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.cleanupManaged(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	q := s.Managed["linux"]
	if s.Artifacts[first] != nil || s.Artifacts[q.CurrentArtifact] == nil || s.Artifacts[q.PreviousArtifact] == nil || len(s.Artifacts) != 2 {
		t.Fatal("current/previous retention failed")
	}
}

func managedCurrentPool(t *testing.T, s Snapshot) string {
	t.Helper()
	for id, p := range s.Pools {
		if p.Phase != Draining && p.Phase != Retired && fingerprint(p.Spec) == fingerprint(*s.Managed["linux"].Current) {
			return id
		}
	}
	t.Fatal("no current managed execution pool")
	return ""
}

func TestManagedRevalidatesAndRepairsCurrentImage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		backend    Backend
		newImage   bool
		validation ErrorCode
		failure    ErrorCode
	}{
		{name: "pruned Docker digest", backend: Docker, validation: ErrImage, failure: ErrImage},
		{name: "new custom Docker image", backend: Docker, newImage: true, validation: ErrImage, failure: ErrImage},
		{name: "invalid Tart revision", backend: Tart, newImage: true, validation: ErrImage, failure: ErrImage},
		{name: "recorded runner version failure", backend: Docker, failure: ErrRunnerVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, builder, _ := managedFixture(t)
			if tc.backend == Tart {
				c.Pools[0].Backend = Tart
				c.Pools[0].Image = newID()
				c.Pools[0].RunnerPath = "/Users/runner/actions-runner"
				if err := m.accept(c, false); err != nil {
					t.Fatal(err)
				}
				builder.prepare = func(_ context.Context, _ Config, p Pool, _ RunnerArtifact, r RunnerRelease) (Pool, error) {
					p.Image, p.RunnerVersion = newID(), r.Version()
					return p, nil
				}
			}
			m.updateManaged(context.Background(), "linux")
			before := m.Store.View()
			pool := managedCurrentPool(t, before)
			current := *before.Managed["linux"].Current
			runner := seedRunner(t, m, pool, Busy)
			beforeRunner := fingerprint(m.Store.View().Runners[runner])
			m.poolProblem(pool, problem(tc.failure, "Fixture image failure.", "Repair."), true)
			validated, repaired := 0, false
			m.Drivers = func(backend Backend) (Driver, error) {
				return validatingDriver{validate: func(ctx context.Context, _ Config, p Pool, _ Snapshot) error {
					validated++
					if _, bounded := ctx.Deadline(); !bounded || backend != tc.backend || p.RunnerVersion != current.RunnerVersion {
						t.Fatal("validation lost deadline or committed image identity")
					}
					if !repaired && tc.validation != "" {
						return problem(tc.validation, "Fixture image is unavailable.", "Repair.")
					}
					return nil
				}}, nil
			}
			builder.prepare = func(context.Context, Config, Pool, RunnerArtifact, RunnerRelease) (Pool, error) {
				repaired = true
				p := current
				if tc.newImage {
					if tc.backend == Tart {
						p.Image = newID()
					} else {
						p.Image = "sha256:" + strings.Repeat("c", 64)
					}
				}
				return p, nil
			}
			m.updateManaged(context.Background(), "linux")
			after := m.Store.View()
			q := after.Managed["linux"]
			active := after.Pools[managedCurrentPool(t, after)]
			if builder.calls != 2 || validated != 2 || q.Phase != UpdateReady || active.Phase != Ready || active.Problem != nil {
				t.Fatalf("image repair did not recover the pool: calls=%d validations=%d managed=%+v pool=%+v", builder.calls, validated, q, active)
			}
			if q.CurrentArtifact == before.Managed["linux"].CurrentArtifact || q.PreviousArtifact != before.Managed["linux"].CurrentArtifact || fingerprint(after.Runners[runner]) != beforeRunner {
				t.Fatal("repair lost artifact history or changed a running job")
			}
			if tc.newImage && after.Pools[pool].Phase != Draining {
				t.Fatal("replacement skipped generation draining")
			}
		})
	}
}

func TestManagedCurrentImageValidationFailureRetainsFallback(t *testing.T) {
	for _, code := range []ErrorCode{ErrRetry, ErrOwnership, ErrPlatform, ErrImage} {
		t.Run(string(code), func(t *testing.T) {
			m, _, builder, _ := managedFixture(t)
			m.updateManaged(context.Background(), "linux")
			before := m.Store.View().Managed["linux"]
			m.Drivers = func(Backend) (Driver, error) {
				return validatingDriver{validate: func(context.Context, Config, Pool, Snapshot) error {
					return problem(code, "Fixture validation failed.", "Retry.")
				}}, nil
			}
			m.updateManaged(context.Background(), "linux")
			after := m.Store.View().Managed["linux"]
			wantBuilds := 1
			if code == ErrImage {
				// The candidate must also validate, including dependencies such as
				// the DinD daemon image, before any suspension can be cleared.
				wantBuilds++
			}
			if builder.calls != wantBuilds || after.CurrentArtifact != before.CurrentArtifact || fingerprint(after.Current) != fingerprint(before.Current) || after.Phase != UpdateRetry || after.Problem == nil || after.Problem.Code != code || !after.NextCheck.After(time.Now()) {
				t.Fatalf("validation failure lost fallback or retry: %+v (builds=%d)", after, builder.calls)
			}
		})
	}
}

func TestManagedImageRepairPreservesUnrelatedSuspension(t *testing.T) {
	for _, failure := range []ErrorCode{ErrAuth, ErrOwnership, ErrPreparation} {
		t.Run(string(failure), func(t *testing.T) {
			m, _, builder, _ := managedFixture(t)
			m.updateManaged(context.Background(), "linux")
			pool := managedCurrentPool(t, m.Store.View())
			m.poolProblem(pool, problem(failure, "Fixture unrelated failure.", "Inspect."), true)
			m.Drivers = func(Backend) (Driver, error) {
				return validatingDriver{validate: func(context.Context, Config, Pool, Snapshot) error {
					if builder.calls == 1 {
						return problem(ErrImage, "Fixture pruned image.", "Repair.")
					}
					return nil
				}}, nil
			}
			m.updateManaged(context.Background(), "linux")
			p := m.Store.View().Pools[pool]
			if builder.calls != 2 || p.Phase != Suspended || p.Problem == nil || p.Problem.Code != failure {
				t.Fatalf("image repair cleared an unrelated suspension: %+v", p)
			}
		})
	}
}

func TestManagedRevalidationHonorsConcurrentLifecycle(t *testing.T) {
	for _, repair := range []bool{false, true} {
		for _, action := range []string{"pause", "stop", "drain", "reload", "cancel"} {
			t.Run(fmt.Sprintf("repair=%t/%s", repair, action), func(t *testing.T) {
				m, _, builder, _ := managedFixture(t)
				m.updateManaged(context.Background(), "linux")
				before := m.Store.View().Managed["linux"]
				pool := managedCurrentPool(t, m.Store.View())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				validations := 0
				m.Drivers = func(Backend) (Driver, error) {
					return validatingDriver{validate: func(context.Context, Config, Pool, Snapshot) error {
						validations++
						if repair && validations == 1 {
							return problem(ErrImage, "Fixture missing image.", "Repair.")
						}
						if err := m.Store.Update(func(s *Snapshot) error {
							switch action {
							case "pause":
								s.Paused = true
							case "stop":
								s.Stopping = true
							case "drain":
								s.Managed["linux"].Paused = true
								s.Pools[pool].Phase = Draining
							case "reload":
								s.Managed["linux"].DesiredHash = "replacement"
							case "cancel":
								cancel()
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						return nil
					}}, nil
				}
				m.updateManaged(ctx, "linux")
				after := m.Store.View().Managed["linux"]
				if after.CurrentArtifact != before.CurrentArtifact || after.Phase == UpdateReady {
					t.Fatalf("stale validation published readiness: %+v (builds=%d)", after, builder.calls)
				}
			})
		}
	}
}
