package runmoor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func writeSuspensionReload(t *testing.T, m *Manager, c Config) {
	t.Helper()
	data, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Storage.State, "suspension-reload.toml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m.ConfigPath = path
}

func TestLateFailureReloadPreservesOperatorPause(t *testing.T) {
	for _, failure := range []struct {
		name        string
		preparation bool
		attempts    int
		code        ErrorCode
	}{
		{"session authentication", false, 1, ErrAuth},
		{"preparation authentication", true, 1, ErrAuth},
		{"preparation circuit breaker", true, 3, ErrPreparation},
	} {
		for _, control := range []struct {
			name  string
			phase PoolPhase
			force bool
		}{
			{"ready", Ready, false},
			{"global pause", Suspended, false},
			{"scoped pause", Paused, false},
			{"scoped stop", Paused, false},
			{"scoped force stop", Paused, true},
		} {
			t.Run(failure.name+"/"+control.name, func(t *testing.T) {
				m, c, remote, _, oldID := testManager(t)
				if len(m.Store.View().Managed) != 0 {
					t.Fatal("fixture must use a manually pinned pool")
				}
				runnerID := seedRunner(t, m, oldID, Preparing)
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Pools[oldID].PreparationFailures = failure.attempts - 1
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				name := c.Pools[0].Name
				var err error
				switch control.name {
				case "global pause":
					err = m.Pause("")
				case "scoped pause":
					err = m.Pause(name)
				case "scoped stop", "scoped force stop":
					err = m.StopPool(name, control.force)
				}
				if err != nil {
					t.Fatal(err)
				}
				blocked := problem(failure.code, "Fixture late dependency failure.", "Correct the dependency and resume.")
				if failure.preparation {
					m.failPreparation(runnerID, blocked)
				} else {
					m.poolProblem(oldID, blocked, true)
				}
				s := m.Store.View()
				if control.phase == Paused && s.Pools[oldID].Phase != Paused {
					t.Fatalf("late failure erased scoped pause: %+v", s.Pools[oldID])
				}
				if !failure.preparation || !control.force {
					if p := s.Pools[oldID].Problem; p == nil || p.Code != failure.code {
						t.Fatalf("late failure lost its pool diagnostic: %+v", p)
					}
				}
				wantFailures := s.Pools[oldID].PreparationFailures
				var wantProblem *Problem
				if p := s.Pools[oldID].Problem; p != nil {
					copy := *p
					wantProblem = &copy
				}
				if failure.preparation {
					r := s.Runners[runnerID]
					if r.Phase != Cleaning || r.Terminated || r.Resources != (Resources{1, 128}) {
						t.Fatalf("late failure bypassed reserved runner cleanup: %+v", r)
					}
					wantFailures := failure.attempts
					if control.force {
						wantFailures = failure.attempts - 1
					} else if r.Problem == nil || r.Problem.Code != failure.code {
						t.Fatalf("late failure lost its runner diagnostic: %+v", r)
					}
					if s.Pools[oldID].PreparationFailures != wantFailures {
						t.Fatal("late failure changed preparation counting")
					}
				}
				c.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL"
				if failure.code == ErrPreparation {
					c.Pools[0].Resources.MemoryMiB++
				}
				writeSuspensionReload(t, m, c)
				if err := m.Reload(context.Background()); err != nil {
					t.Fatal(err)
				}
				s = m.Store.View()
				if s.Pools[oldID].Phase != Draining || s.Paused != (control.name == "global pause") || s.Stopping {
					t.Fatal("reload changed the independent lifecycle controls")
				}
				var replacement *PoolState
				for id, p := range s.Pools {
					if id != oldID {
						replacement = p
					}
				}
				if replacement == nil || replacement.Phase != control.phase {
					t.Fatalf("corrected reload changed operator pause: %+v", replacement)
				}
				if replacement.Phase == Ready {
					if replacement.PreparationFailures != 0 || replacement.Problem != nil {
						t.Fatalf("corrected reload retained a recovered diagnostic: %+v", replacement)
					}
				} else {
					if replacement.PreparationFailures != wantFailures {
						t.Fatalf("corrected reload changed preparation-failure count: got %d want %d", replacement.PreparationFailures, wantFailures)
					}
					if (replacement.Problem == nil) != (wantProblem == nil) || replacement.Problem != nil && *replacement.Problem != *wantProblem {
						t.Fatalf("corrected reload changed the retained pool diagnostic: got %+v want %+v", replacement.Problem, wantProblem)
					}
				}
				if err := m.Resume(context.Background(), name); err != nil {
					t.Fatal(err)
				}
				// Finish the old generation's ordinary ownership boundary before
				// checking acquisition eligibility on the explicitly resumed pool.
				m.cleanup(context.Background(), runnerID)
				m.retirePool(context.Background(), oldID)
				if _, err := m.ensureScaleSet(context.Background(), replacement.ID, remote); err != nil {
					t.Fatal(err)
				}
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Pools[replacement.ID].Session = remote.session.ID()
					s.Pools[replacement.ID].Demand = 1
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				s = m.Store.View()
				if !eligible(s, s.Pools[replacement.ID]) || len(Schedule(s)) == 0 {
					t.Fatal("explicit resume did not restore eligible work after cleanup")
				}
			})
		}
	}
}

func TestValidatedReloadRecoversOnlyRelatedSuspensions(t *testing.T) {
	for _, tc := range []struct {
		name            string
		code            ErrorCode
		change          func(*Config)
		wantReady       bool
		wantReplacement bool
	}{
		{"authentication credential", ErrAuth, func(c *Config) { c.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL" }, true, true},
		{"authentication unrelated image", ErrAuth, func(c *Config) { c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64) }, false, true},
		{"ownership identity", ErrOwnership, func(c *Config) { c.Pools[0].ScaleSet = "replacement-linux" }, true, true},
		{"ownership unrelated image", ErrOwnership, func(c *Config) { c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64) }, false, true},
		{"image replacement", ErrImage, func(c *Config) { c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64) }, true, true},
		{"image unrelated label", ErrImage, func(c *Config) { c.Pools[0].Labels = []string{"test-linux", "extra"} }, false, true},
		{"preparation resources", ErrPreparation, func(c *Config) { c.Pools[0].Resources.MemoryMiB++ }, true, true},
		{"preparation Docker timeout", ErrPreparation, func(c *Config) { c.Timeouts.DockerPreparation = "7m" }, true, true},
		{"preparation Docker socket", ErrPreparation, func(c *Config) { c.DockerSocket = "unix:///var/run/replacement-docker.sock" }, true, true},
		{"preparation unrelated job timeout", ErrPreparation, func(c *Config) { c.Timeouts.Job = "7h" }, false, false},
		{"preparation unrelated Tart timeout", ErrPreparation, func(c *Config) { c.Timeouts.TartPreparation = "11m" }, false, false},
		{"preparation unrelated Tart executable", ErrPreparation, func(c *Config) { c.TartExecutable = "/usr/local/bin/tart" }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, _, _, oldID := testManager(t)
			blocked := problem(tc.code, "Fixture suspension.", "Correct the fixture dependency.")
			blocked.Pool = c.Pools[0].Name
			if err := m.Store.Update(func(s *Snapshot) error {
				p := s.Pools[oldID]
				p.Phase = Suspended
				p.Problem = blocked
				p.PreparationFailures = 3
				if tc.code == ErrOwnership {
					p.SuspensionSource = SuspensionScaleSet
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			tc.change(&c)
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			s := m.Store.View()
			if !tc.wantReplacement {
				if len(s.Pools) != 1 || s.Pools[oldID].Phase != Suspended || s.Pools[oldID].Problem == nil || *s.Pools[oldID].Problem != *blocked || s.Pools[oldID].PreparationFailures != 3 {
					t.Fatalf("unrelated global change altered the suspension: %+v", s.Pools)
				}
				return
			}
			if s.Pools[oldID].Phase != Draining {
				t.Fatal("old generation did not drain")
			}
			var replacement *PoolState
			for id, p := range s.Pools {
				if id != oldID {
					replacement = p
				}
			}
			if replacement == nil || replacement.ScaleSetID != 0 || replacement.CreatePending {
				t.Fatal("replacement skipped the ordinary ownership boundary")
			}
			if tc.wantReady {
				if replacement.Phase != Ready || replacement.Problem != nil || replacement.PreparationFailures != 0 {
					t.Fatalf("validated correction did not resume: %+v", replacement)
				}
			} else if replacement.Phase != Suspended || replacement.Problem == nil || *replacement.Problem != *blocked || replacement.PreparationFailures != 3 {
				t.Fatalf("unrelated change lost suspension details: %+v", replacement)
			}
		})
	}
}

func TestRunnerVersionSuspensionIgnoresDaemonImageReplacement(t *testing.T) {
	m, c, _, _, _ := testManager(t)
	c.Pools[0].Mode = DinD
	c.Pools[0].DaemonImage = c.Pools[0].Image
	c.Pools[0].DaemonResources = Resources{CPU: 1, MemoryMiB: 128}
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	var oldID string
	for id, p := range m.Store.View().Pools {
		if p.Phase == Ready && p.Spec.Mode == DinD {
			oldID = id
		}
	}
	if oldID == "" {
		t.Fatal("DinD fixture was not activated")
	}
	blocked := problem(ErrRunnerVersion, "Fixture runner image has the wrong version.", "Replace the runner image.")
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = blocked
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c.Pools[0].DaemonImage = "example/daemon@sha256:" + strings.Repeat("b", 64)
	writeSuspensionReload(t, m, c)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if s.Pools[oldID].Phase != Draining {
		t.Fatal("old DinD generation did not drain")
	}
	found := false
	for id, p := range s.Pools {
		if id != oldID && p.Phase != Draining {
			found = true
			if p.Phase != Suspended || p.Problem == nil || *p.Problem != *blocked {
				t.Fatalf("daemon image replacement resumed a broken runner image: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("replacement DinD generation was not created")
	}
}

func TestLocalOwnershipSuspensionIgnoresScaleSetChange(t *testing.T) {
	m, c, _, _, oldID := testManager(t)
	blocked := problem(ErrOwnership, "Fixture Docker volume belongs to another execution.", "Inspect the local volume.")
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = blocked
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c.Pools[0].ScaleSet = "replacement-linux"
	writeSuspensionReload(t, m, c)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if s.Pools[oldID].Phase != Draining {
		t.Fatal("old local-ownership generation did not drain")
	}
	found := false
	for id, p := range s.Pools {
		if id != oldID {
			found = true
			if p.Phase != Suspended || p.Problem == nil || *p.Problem != *blocked || p.SuspensionSource != SuspensionUnknown {
				t.Fatalf("scale-set change concealed local ownership conflict: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("replacement pool was not created")
	}
}

func TestOwnershipSuspensionRecordsRemoteOrLocalSource(t *testing.T) {
	t.Run("remote scale set", func(t *testing.T) {
		m, _, remote, _, id := testManager(t)
		remote.failure = problem(ErrOwnership, "Fixture remote scale set is foreign.", "Choose another scale set.")
		m.poolLoop(context.Background(), id)
		p := m.Store.View().Pools[id]
		if p.Phase != Suspended || p.SuspensionSource != SuspensionScaleSet {
			t.Fatalf("remote ownership source was not recorded: %+v", p)
		}
	})
	t.Run("local preparation", func(t *testing.T) {
		m, _, _, _, id := testManager(t)
		runnerID := seedRunner(t, m, id, Preparing)
		m.failPreparation(runnerID, problem(ErrOwnership, "Fixture local volume is foreign.", "Inspect the volume."))
		p := m.Store.View().Pools[id]
		if p.Phase != Suspended || p.SuspensionSource != SuspensionUnknown {
			t.Fatalf("local ownership source was misclassified: %+v", p)
		}
	})
}

func TestLegacySuspensionRemainsVisibleAndRequiresResume(t *testing.T) {
	m, c, _, _, oldID := testManager(t)
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	status := statusOf(m.Store.View(), true)
	if len(status.Pools) != 1 || status.Pools[0].Problem == nil || status.Pools[0].Problem.Code != ErrRetry || !strings.Contains(status.Pools[0].Problem.Message, "original failure reason") {
		t.Fatalf("legacy suspension is silent in status: %+v", status.Pools)
	}
	report := Doctor(context.Background(), c, m.Store.View(), m.RemoteFactory, m.Drivers)
	found := false
	for _, check := range report.Checks {
		if check.Name == "pool_state" && check.Pool == c.Pools[0].Name && check.Problem != nil && check.Problem.Code == ErrRetry && !check.OK {
			found = true
		}
	}
	if !found || m.Store.View().Pools[oldID].Problem != nil {
		t.Fatal("doctor omitted the legacy diagnostic or mutated the stored cause")
	}
	c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64)
	writeSuspensionReload(t, m, c)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, p := range m.Store.View().Pools {
		if id != oldID && (p.Phase != Suspended || p.Problem != nil) {
			t.Fatalf("unknown prior failure was automatically resumed: %+v", p)
		}
	}
}

func TestValidatedReloadPreservesSuspensionDuringGlobalPauseOrStop(t *testing.T) {
	for _, stopping := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "stopping"}[stopping], func(t *testing.T) {
			m, c, _, _, oldID := testManager(t)
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[oldID].Phase = Suspended
				s.Pools[oldID].Problem = problem(ErrImage, "Fixture image failure.", "Replace the image.")
				s.Paused = !stopping
				s.Stopping = stopping
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			c.Pools[0].Image = "example/runner@sha256:" + strings.Repeat("b", 64)
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			for id, p := range m.Store.View().Pools {
				if id != oldID && (p.Phase != Suspended || p.Problem == nil || p.Problem.Code != ErrImage) {
					t.Fatalf("reload overrode a lifecycle decision: %+v", p)
				}
			}
		})
	}
}

func TestRejectedReloadPreservesSuspension(t *testing.T) {
	m, c, remote, _, oldID := testManager(t)
	blocked := problem(ErrAuth, "Fixture authentication failure.", "Replace the credential.")
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = blocked
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := m.Store.View()
	c.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL"
	writeSuspensionReload(t, m, c)
	remote.failure = problem(ErrAuth, "Fixture replacement was rejected.", "Correct it.")
	requireCode(t, m.Reload(context.Background()), ErrAuth)
	after := m.Store.View()
	if fingerprint(after) != fingerprint(before) {
		t.Fatal("failed validation changed the suspended generation")
	}
}

func TestManagedUpdateCarriesUnrelatedSuspensionCause(t *testing.T) {
	m, _, _, oldID := managedFixture(t)
	blocked := problem(ErrAuth, "Fixture authentication failure.", "Replace the credential.")
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = blocked
		s.Pools[oldID].PreparationFailures = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	if s.Pools[oldID].Phase != Draining {
		t.Fatal("old managed generation did not drain")
	}
	var replacement *PoolState
	for id, p := range s.Pools {
		if id != oldID {
			replacement = p
		}
	}
	if replacement == nil || replacement.Phase != Suspended || replacement.Problem == nil || *replacement.Problem != *blocked || replacement.PreparationFailures != 2 {
		t.Fatalf("managed update hid an unrelated suspension: %+v", replacement)
	}
}

func TestValidatedManagedReloadRecoversAfterCandidatePublication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   ErrorCode
		change func(*Config)
		ready  bool
	}{
		{"authentication group", ErrAuth, func(c *Config) { c.Pools[0].RunnerGroup = "replacement-group" }, true},
		{"ownership scale set", ErrOwnership, func(c *Config) { c.Pools[0].ScaleSet = "replacement-linux" }, true},
		{"unrelated labels", ErrAuth, func(c *Config) { c.Pools[0].Labels = append(c.Pools[0].Labels, "extra") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, _, oldID := managedFixture(t)
			if tc.name == "authentication group" {
				c.Connections[0].Target = "https://github.com/delinoio"
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Config.Connections[0].Target = c.Connections[0].Target
					s.Requested.Connections[0].Target = c.Connections[0].Target
					s.Pools[oldID].Connection.Target = c.Connections[0].Target
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			blocked := problem(tc.code, "Fixture managed suspension.", "Correct the configuration.")
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[oldID].Phase = Suspended
				s.Pools[oldID].Problem = blocked
				s.Pools[oldID].PreparationFailures = 2
				if tc.code == ErrOwnership {
					s.Pools[oldID].SuspensionSource = SuspensionScaleSet
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			tc.change(&c)
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if m.Store.View().Pools[oldID].Phase != Suspended {
				t.Fatal("reload resumed the pool before its managed candidate was verified")
			}
			if tc.ready {
				c.Pools[0].Labels = append(c.Pools[0].Labels, "second-reload")
				writeSuspensionReload(t, m, c)
				if err := m.Reload(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			m.updateManaged(context.Background(), "linux")
			s := m.Store.View()
			if s.Pools[oldID].Phase != Draining {
				t.Fatal("old managed generation did not drain")
			}
			for id, p := range s.Pools {
				if id == oldID {
					continue
				}
				if tc.ready && (p.Phase != Ready || p.Problem != nil || p.PreparationFailures != 0) {
					t.Fatalf("validated managed correction was lost: %+v", p)
				}
				if !tc.ready && (p.Phase != Suspended || p.Problem == nil || *p.Problem != *blocked || p.PreparationFailures != 2) {
					t.Fatalf("unrelated managed change resumed the pool: %+v", p)
				}
			}
		})
	}
}

func TestValidatedManagedReloadUsesSuspendedCommittedPoolWhenRequestIsPending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   ErrorCode
		change func(*Config)
		ready  bool
	}{
		{"authentication group", ErrAuth, func(c *Config) { c.Pools[0].RunnerGroup = "replacement-group" }, true},
		{"ownership scale set", ErrOwnership, func(c *Config) { c.Pools[0].ScaleSet = "replacement-linux" }, true},
		{"preparation resources", ErrPreparation, func(c *Config) { c.Pools[0].Resources.MemoryMiB++ }, true},
		{"authentication unrelated labels", ErrAuth, func(c *Config) { c.Pools[0].Labels = append(c.Pools[0].Labels, "pending") }, false},
		{"preparation unrelated labels", ErrPreparation, func(c *Config) { c.Pools[0].Labels = append(c.Pools[0].Labels, "pending") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, _, oldID := managedFixture(t)
			if tc.name == "authentication group" {
				c.Connections[0].Target = "https://github.com/delinoio"
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Config.Connections[0].Target = c.Connections[0].Target
					s.Requested.Connections[0].Target = c.Connections[0].Target
					s.Pools[oldID].Connection.Target = c.Connections[0].Target
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			tc.change(&c)
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s := m.Store.View(); s.Pools[oldID].Phase != Ready || s.Managed["linux"].Current == nil || s.Managed["linux"].AppliedHash == s.Managed["linux"].DesiredHash {
				t.Fatal("fixture did not retain an unresolved managed request")
			}
			blocked := problem(tc.code, "Fixture pending-request suspension.", "Correct the configuration.")
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[oldID].Phase = Suspended
				s.Pools[oldID].Problem = blocked
				s.Pools[oldID].PreparationFailures = 2
				if tc.code == ErrOwnership {
					s.Pools[oldID].SuspensionSource = SuspensionScaleSet
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tc.ready && m.Store.View().ManagedRecovery["linux"] == nil {
				t.Fatal("validated pending correction was not recorded against the failed committed pool")
			}
			if !tc.ready && m.Store.View().ManagedRecovery["linux"] != nil {
				t.Fatal("unrelated pending request was treated as a correction")
			}
			c.Pools[0].Labels = append(c.Pools[0].Labels, "later-reload")
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.updateManaged(context.Background(), "linux")
			s := m.Store.View()
			if s.Pools[oldID].Phase != Draining {
				t.Fatal("suspended committed pool did not drain after candidate publication")
			}
			found := false
			for id, p := range s.Pools {
				if id == oldID {
					continue
				}
				found = true
				if tc.ready && (p.Phase != Ready || p.Problem != nil || p.PreparationFailures != 0) {
					t.Fatalf("validated pending correction did not resume the replacement: %+v", p)
				}
				if !tc.ready && (p.Phase != Suspended || p.Problem == nil || *p.Problem != *blocked || p.PreparationFailures != 2) {
					t.Fatalf("unrelated pending request hid the failure: %+v", p)
				}
			}
			if !found {
				t.Fatal("verified candidate was not published")
			}
		})
	}
}

func TestVerifiedManagedImageRecoversStartupCircuitBreaker(t *testing.T) {
	m, _, _, oldID := managedFixture(t)
	if err := m.Store.Update(func(s *Snapshot) error {
		p := s.Pools[oldID]
		p.Phase = Suspended
		p.Problem = problem(ErrPreparation, "Fixture runner could not start.", "Replace the image.")
		p.PreparationFailures = 3
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.updateManaged(context.Background(), "linux")
	s := m.Store.View()
	if s.Pools[oldID].Phase != Draining {
		t.Fatal("old managed generation did not drain")
	}
	for id, p := range s.Pools {
		if id != oldID && (p.Phase != Ready || p.Problem != nil || p.PreparationFailures != 0) {
			t.Fatalf("verified candidate did not recover startup failure: %+v", p)
		}
	}
}

type credentialRetirementRemote struct {
	*fakeRemote
	deletionError error
	deleted       int
}

func (r *credentialRetirementRemote) DeletePool(_ context.Context, p PoolState) error {
	if r.deletionError != nil {
		return r.deletionError
	}
	if p.ScaleSetID != 1 || p.OwnerLabel == "" {
		return problem(ErrOwnership, "Fixture ownership mismatch.", "Preserve the pool.")
	}
	r.deleted++
	return nil
}

func TestValidatedCredentialReplacementRetiresWithNewAuthority(t *testing.T) {
	for _, ownershipError := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "ownership rejected"}[ownershipError], func(t *testing.T) {
			m, c, _, _, oldID := testManager(t)
			oldRemote := &credentialRetirementRemote{fakeRemote: &fakeRemote{}, deletionError: problem(ErrAuth, "Fixture old credential was revoked.", "Replace the credential.")}
			newRemote := &credentialRetirementRemote{fakeRemote: &fakeRemote{}}
			if ownershipError {
				newRemote.deletionError = problem(ErrOwnership, "Fixture owner label is wrong.", "Preserve the pool.")
			}
			oldCredential := c.Connections[0].Credential.Env
			m.RemoteFactory = func(conn Connection) (Remote, error) {
				if conn.Credential.Env == oldCredential {
					return oldRemote, nil
				}
				return newRemote, nil
			}
			if _, err := m.remote(*m.Store.View().Pools[oldID]); err != nil {
				t.Fatal(err)
			}
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[oldID].Phase = Suspended
				s.Pools[oldID].Problem = oldRemote.deletionError.(*Problem)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			c.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL"
			writeSuspensionReload(t, m, c)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			s := m.Store.View()
			if s.Pools[oldID].Phase != Draining || len(s.RetirementAuthority) != 1 {
				t.Fatal("validated replacement authority was not retained")
			}
			remote, err := m.remote(*s.Pools[oldID])
			if err != nil || remote != newRemote {
				t.Fatal("draining generation kept its revoked cached client", err)
			}
			m.retirePool(context.Background(), oldID)
			s = m.Store.View()
			if ownershipError {
				if s.Pools[oldID].Phase != Draining || s.RetirementAuthority[oldID].Credential.Env != c.Connections[0].Credential.Env {
					t.Fatal("failed ownership verification released the old scale set")
				}
			} else if s.Pools[oldID].Phase != Retired || s.RetirementAuthority[oldID].Name != "" || s.RetirementValidation[oldID] != "" || newRemote.deleted != 1 || oldRemote.deleted != 0 {
				t.Fatal("old scale set was not retired with the validated replacement credential")
			}
		})
	}
}

func TestValidatedCredentialRollbackRefreshesDrainingAuthority(t *testing.T) {
	m, c, _, _, oldID := testManager(t)
	original := c.Connections[0].Credential.Env
	staleOriginal := &credentialRetirementRemote{fakeRemote: &fakeRemote{}, deletionError: problem(ErrAuth, "Fixture original credential was revoked.", "Restore the credential.")}
	staleReplacement := &credentialRetirementRemote{fakeRemote: &fakeRemote{}, deletionError: problem(ErrAuth, "Fixture replacement credential was revoked.", "Restore the credential.")}
	restoredOriginal := &credentialRetirementRemote{fakeRemote: &fakeRemote{}}
	restored := false
	m.RemoteFactory = func(conn Connection) (Remote, error) {
		if conn.Credential.Env != original {
			return staleReplacement, nil
		}
		if restored {
			return restoredOriginal, nil
		}
		return staleOriginal, nil
	}
	if _, err := m.remote(*m.Store.View().Pools[oldID]); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Update(func(s *Snapshot) error {
		s.Pools[oldID].Phase = Suspended
		s.Pools[oldID].Problem = problem(ErrAuth, "Fixture original credential was revoked.", "Replace the credential.")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c.Connections[0].Credential.Env = "RUNMOOR_REPLACEMENT_CREDENTIAL"
	writeSuspensionReload(t, m, c)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.retirePool(context.Background(), oldID)
	if s := m.Store.View(); s.Pools[oldID].Phase != Draining || s.RetirementAuthority[oldID].Credential.Env != c.Connections[0].Credential.Env {
		t.Fatal("failed replacement cleanup did not retain its authority")
	}
	restored = true
	c.Connections[0].Credential.Env = original
	writeSuspensionReload(t, m, c)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if s.RetirementAuthority[oldID].Credential.Env != original {
		t.Fatal("validated rollback did not replace the stale cleanup authority")
	}
	m.retirePool(context.Background(), oldID)
	s = m.Store.View()
	if s.Pools[oldID].Phase != Retired || restoredOriginal.deleted != 1 || staleOriginal.deleted != 0 || staleReplacement.deleted != 0 {
		t.Fatal("rollback kept a revoked cleanup client")
	}
}
