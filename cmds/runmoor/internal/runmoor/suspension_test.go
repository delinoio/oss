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
