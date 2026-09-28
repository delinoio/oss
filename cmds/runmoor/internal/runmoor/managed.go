package runmoor

import (
	"context"
	"net/http"
	"sort"
	"time"
)

func initializeManaged(s *Snapshot, c Config) {
	if s.Managed == nil {
		s.Managed = map[string]*ManagedPool{}
	}
	if s.Artifacts == nil {
		s.Artifacts = map[string]*RunnerArtifact{}
	}
	names := map[string]bool{}
	for _, p := range c.Pools {
		if !managesRunner(p) {
			continue
		}
		names[p.Name] = true
		hash := fingerprint(p)
		sourceHash := fingerprint(struct {
			Backend Backend
			Image   string
			Source  *ImageSource
			Path    string
		}{p.Backend, p.Image, p.ImageSource, p.RunnerPath})
		managed := s.Managed[p.Name]
		if managed == nil {
			managed = &ManagedPool{Name: p.Name, Phase: UpdatePending}
			s.Managed[p.Name] = managed
			for _, old := range s.Pools {
				if old.Spec.Name == p.Name && old.Phase != Retired && old.Phase != Draining && old.Spec.Backend == p.Backend && fingerprint(old.Connection) == fingerprint(c.Connection(p.Connection)) {
					copy := old.Spec
					managed.Current = &copy
					managed.Paused = old.Phase == Paused
				}
			}
		}
		if managed.SourceHash != sourceHash {
			managed.SourceHash = sourceHash
			managed.BaseImage = ""
		}
		managed.Mode = runnerMode(p)
		managed.Resources = p.Cost()
		managed.MaxRunners = p.MaxRunners
		if managed.DesiredHash != hash {
			managed.DesiredHash = hash
			managed.NextCheck = time.Time{}
			managed.Phase = UpdatePending

		}
	}
	for name := range s.Managed {
		if !names[name] {
			delete(s.Managed, name)
		}
	}
	s.Requested = c
}
func managedConfig(s Snapshot) Config {
	c := s.Requested
	c.Pools = nil
	for _, p := range s.Requested.Pools {
		if !managesRunner(p) {
			c.Pools = append(c.Pools, p)
			continue
		}
		if managed := s.Managed[p.Name]; managed != nil && managed.Current != nil {
			c.Pools = append(c.Pools, *managed.Current)
		}
	}
	return c
}
func (m *Manager) requestRunnerUpdate(name string) error {
	return m.Store.Update(func(s *Snapshot) error {
		if s.Stopping {
			return problem(ErrControl, "The manager is stopping.", "Restart the manager before requesting an update.")
		}
		found := false
		for key, v := range s.Managed {
			if name == "" || name == key {
				v.NextCheck = time.Time{}
				found = true
			}
		}
		if !found {
			return problem(ErrConfig, "No automatically managed pool matches.", "Use runner_version = latest or a managed image source, then reload.")
		}
		s.ReleaseChecked = time.Time{}
		if s.ReleaseProblem == nil || s.ReleaseProblem.HTTPStatus != 429 && s.ReleaseProblem.HTTPStatus != 403 {
			s.ReleaseRetryAt = time.Time{}
		}
		return nil
	})
}
func (m *Manager) startManagedWork() {
	s := m.Store.View()
	// One worker serializes release acquisition, candidate preparation and cleanup.
	// It is lifecycle-tracked like runner workers and cannot outlive shutdown.
	due := ""
	names := make([]string, 0, len(s.Managed))
	for name := range s.Managed {
		names = append(names, name)
	}
	sort.Strings(names)
	if !s.Stopping {
		for _, name := range names {
			v := s.Managed[name]
			if !time.Now().Before(v.NextCheck) {
				due = name
				break
			}
		}
	}
	cleanup := false
	for id, a := range s.Artifacts {
		if !artifactReferenced(s, id) && !time.Now().Before(a.NextCleanup) {
			cleanup = true
		}
	}
	if due != "" || cleanup {
		m.startWork("runner-update", func(ctx context.Context) { m.updateManaged(ctx, due) })
	}
}
func (m *Manager) updateManaged(ctx context.Context, name string) {
	if ctx.Err() != nil {
		return
	}
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	if err := m.cleanupManaged(ctx); err != nil {
		if name != "" {
			m.managedFailure(name, err)
		}
		return
	}
	if name == "" {
		return
	}
	s := m.Store.View()
	managed := s.Managed[name]
	if managed == nil || s.Stopping {
		return
	}
	desired, ok := requestedPool(s.Requested, name)
	if !ok {
		return
	}
	if !managed.Paused && !s.Paused && desired.Backend == Docker && (s.Requested.DockerCapacityPending || !validResources(s.Requested.DockerBudget)) && m.ResolveCapacity != nil {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		resolved, err := m.ResolveCapacity(probe, s.Requested)
		cancel()
		if err != nil {
			m.managedFailure(name, err)
			return
		}
		if fingerprint(resolved) != fingerprint(s.Requested) {
			before := fingerprint(s.Requested)
			if err = m.Store.Update(func(v *Snapshot) error {
				if v.Stopping || v.Paused || fingerprint(v.Requested) != before {
					return staleUpdate()
				}
				initializeManaged(v, resolved)
				return acceptSnapshot(v, managedConfig(*v), false)
			}); err != nil {
				return
			}
			s = m.Store.View()
			managed = s.Managed[name]
			desired, ok = requestedPool(s.Requested, name)
			if managed == nil || !ok {
				return
			}
		}
	}
	hash := managed.DesiredHash
	if err := m.Store.Update(func(v *Snapshot) error {
		if q := v.Managed[name]; q != nil && q.DesiredHash == hash {
			if q.Phase != UpdateWaiting {
				q.Phase = UpdateChecking
			}
		}
		return nil
	}); err != nil {
		return
	}
	releases := s.Releases
	if len(releases) == 0 || s.ReleaseChecked.IsZero() || time.Since(s.ReleaseChecked) >= time.Hour {
		if time.Now().Before(s.ReleaseRetryAt) && s.ReleaseProblem != nil {
			m.managedFailure(name, s.ReleaseProblem)
			return
		}
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		releases, err = fetchRunnerReleases(probe, m.ReleaseClient)
		cancel()
		if err == nil && len(releases) == 0 {
			err = releaseProblem()
		}
		if err != nil {
			p := classify(err, ErrRetry, "Runner release lookup failed.", "Wait for the next automatic retry.")
			_ = m.Store.Update(func(v *Snapshot) error {
				v.ReleaseAttempts++
				v.ReleaseRetryAt = time.Now().Add(managedRetryDelay(v.ReleaseAttempts, p))
				p.RetryAt = v.ReleaseRetryAt
				v.ReleaseProblem = p
				return nil
			})
			m.managedFailure(name, p)
			return
		}
		if err = m.Store.Update(func(v *Snapshot) error {
			v.Releases = releases
			v.ReleaseChecked = nowUTC()
			v.ReleaseRetryAt = time.Time{}
			v.ReleaseProblem = nil
			v.ReleaseAttempts = 0
			return nil
		}); err != nil {
			m.managedFailure(name, err)
			return
		}
	}
	if len(releases) == 0 {
		m.managedFailure(name, releaseProblem())
		return
	}
	selected := releases[0]
	if desired.RunnerVersion != LatestRunner {
		found := false
		for _, release := range releases {
			if release.Version() == desired.RunnerVersion {
				selected = release
				found = true
				break
			}
		}
		if !found {
			m.managedFailure(name, problem(ErrRunnerVersion, "The pinned runner is not a known supported release.", "Select latest or a current exact release."))
			return
		}
	}
	if err := m.Store.Update(func(v *Snapshot) error {
		q := v.Managed[name]
		if q == nil || q.DesiredHash != hash {
			return staleUpdate()
		}
		q.LastCheck = v.ReleaseChecked
		q.CandidateVersion = selected.Version()
		if q.Current != nil {
			deadline := updateDeadline(releases, q.Current.RunnerVersion)
			if !deadline.IsZero() && (q.Expires.IsZero() || deadline.Before(q.Expires)) {
				q.Expires = deadline
			}
			found := false
			for _, r := range releases {
				found = found || r.Version() == q.Current.RunnerVersion
			}
			if !found {
				q.Expires = nowUTC()
			}
		}
		return nil
	}); err != nil {
		return
	}
	if current := m.Store.View(); current.Paused || current.Managed[name] == nil || current.Managed[name].Paused {
		_ = m.Store.Update(func(v *Snapshot) error {
			q := v.Managed[name]
			if q == nil || q.DesiredHash != hash {
				return staleUpdate()
			}
			q.Phase = managed.Phase
			if !v.Paused && !q.Paused {
				q.NextCheck = time.Time{}
				return nil
			}
			q.NextCheck = time.Now().Add(time.Hour)
			if !q.Expires.IsZero() && !time.Now().Before(q.Expires) {
				q.Phase = UpdateExpired
			}
			return nil
		})
		return
	}
	if managed.Current != nil && managed.Current.RunnerVersion == selected.Version() && managed.AppliedHash == hash {
		current := m.Store.View()
		err := m.validateManagedImage(ctx, current.Config, *managed.Current, current)
		repair := false
		if err != nil {
			code := classify(err, ErrRetry, "Managed runner image validation failed.", "Wait for the next automatic retry.").Code
			if code != ErrImage && code != ErrRunnerVersion {
				m.managedFailure(name, err)
				return
			}
			repair = true
		}
		// Image presence alone cannot disprove a previously observed runner
		// version failure. Rebuild and verify it in the preparation environment.
		for _, p := range current.Pools {
			if fingerprint(p.Spec) == fingerprint(*managed.Current) && managedImageFailure(p) {
				repair = true
			}
		}
		if !repair {
			_ = m.Store.Update(func(v *Snapshot) error {
				q := v.Managed[name]
				if q == nil || q.DesiredHash != hash || q.Paused || v.Paused || v.Stopping || ctx.Err() != nil {
					return staleUpdate()
				}
				q.Phase = UpdateReady
				if !q.Expires.IsZero() && !time.Now().Before(q.Expires) {
					q.Phase = UpdateExpired
				}
				q.NextCheck = time.Now().Add(time.Hour)
				q.Attempts = 0
				q.Problem = nil
				return nil
			})
			return
		}
		m.Log.Info("runner_image_repair_required", "pool", name, "version", selected.Version(), "backend", desired.Backend)
	}
	m.Log.Info("runner_update_preparing", "pool", name, "version", selected.Version(), "backend", desired.Backend)
	artifact := RunnerArtifact{ID: newID(), Pool: name, Backend: desired.Backend, Phase: ArtifactPreparing, Resources: desired.Resources, Reserved: true, CreatedAt: nowUTC()}
	if err := m.Store.Update(func(v *Snapshot) error {
		q := v.Managed[name]
		if q == nil || q.DesiredHash != hash || q.Paused || v.Paused || v.Stopping || ctx.Err() != nil {
			return staleUpdate()
		}
		q.Phase = UpdateWaiting
		if !preparationFits(*v, artifact) {
			return nil
		}
		q.Candidate = artifact.ID
		q.Phase = UpdatePreparing
		v.Artifacts[artifact.ID] = &artifact
		return nil
	}); err != nil {
		return
	}
	if m.Store.View().Artifacts[artifact.ID] == nil {
		_ = m.Store.Update(func(v *Snapshot) error {
			if q := v.Managed[name]; q != nil {
				q.NextCheck = time.Now().Add(time.Second)
				q.Problem = problem(ErrCapacity, "Runner update is waiting for preparation capacity.", "Running jobs will finish normally; the next available slot is reserved for the update.")
			}
			return nil
		})
		return
	}
	buildCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	resolved, err := m.RunnerBuilder.Prepare(buildCtx, s.Requested, desired, artifact, selected)
	if err == nil {
		err = m.validateManagedImage(buildCtx, s.Requested, resolved, m.Store.View())
	}
	cancel()
	if err != nil {
		m.managedFailure(name, err)
		m.abandonArtifact(ctx, artifact.ID)
		return
	}
	activated := false
	err = m.Store.Update(func(v *Snapshot) error {
		a := v.Artifacts[artifact.ID]
		if a == nil {
			return staleUpdate()
		}
		a.Image = resolved.Image
		a.Reserved = false
		a.Phase = ArtifactReady
		q := v.Managed[name]
		if q == nil || q.DesiredHash != hash || v.Stopping || v.Paused || q.Paused || ctx.Err() != nil {
			if q != nil && q.DesiredHash == hash {
				q.Phase = UpdatePending
				q.Candidate = ""
				q.CandidateVersion = ""
			}
			return nil
		}
		activated = true
		recoverManagedImage(v, q.Current)
		q.PreviousArtifact = q.CurrentArtifact
		q.CurrentArtifact = artifact.ID
		q.Current = &resolved
		q.AppliedHash = hash
		q.Candidate = ""
		q.CandidateVersion = ""
		q.Phase = UpdateReady
		q.Problem = nil
		q.Attempts = 0
		q.NextCheck = time.Now().Add(time.Hour)
		q.Expires = updateDeadline(releases, resolved.RunnerVersion)
		if desired.Backend == Tart && q.BaseImage == "" {
			q.BaseImage = resolved.Image
		}
		return acceptSnapshot(v, managedConfig(*v), false)
	})
	if err != nil {
		m.managedFailure(name, err)
		return
	}
	if activated {
		m.Log.Info("runner_update_ready", "pool", name, "version", resolved.RunnerVersion, "artifact", artifact.ID)
	} else {
		m.Log.Info("runner_update_deferred", "pool", name, "artifact", artifact.ID)
	}
	_ = m.cleanupManaged(ctx)
}

// Validation is read-only and uses no management credentials or JIT data.
func (m *Manager) validateManagedImage(ctx context.Context, c Config, p Pool, s Snapshot) error {
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := probe.Err(); err != nil {
		return err
	}
	driver, err := m.Drivers(p.Backend)
	if err != nil {
		return err
	}
	return driver.Validate(probe, c, p, s)
}

func managedImageFailure(p *PoolState) bool {
	return p.Phase == Suspended && p.Problem != nil && (p.Problem.Code == ErrImage || p.Problem.Code == ErrRunnerVersion || p.Problem.Code == ErrPreparation && p.PreparationFailures >= 3)
}

func recoverManagedImage(s *Snapshot, previous *Pool) {
	if previous == nil {
		return
	}
	for _, p := range s.Pools {
		if fingerprint(p.Spec) != fingerprint(*previous) || !managedImageFailure(p) {
			continue
		}
		// Only the verified replacement authorizes clearing an image or startup failure.
		// Operator pauses, draining generations and unrelated failures survive.
		p.Phase = Ready
		p.Problem = nil
		p.PreparationFailures = 0
		p.Session = ""
		p.LastMessage = 0
	}
}

func staleUpdate() *Problem {
	return problem(ErrRetry, "Runner update was superseded or stopped.", "The current configuration and existing jobs remain authoritative.")
}
func (m *Manager) managedFailure(name string, err error) {
	p := classify(err, ErrRetry, "Managed runner preparation failed.", "Inspect status; Runmoor will retry automatically.")
	_ = m.Store.Update(func(s *Snapshot) error {
		if q := s.Managed[name]; q != nil {
			q.Phase = UpdateRetry
			q.Attempts++
			q.Problem = p
			q.NextCheck = time.Now().Add(managedRetryDelay(q.Attempts, p))
			if !q.Expires.IsZero() && !time.Now().Before(q.Expires) {
				q.Phase = UpdateExpired
			}
		}
		return nil
	})
	logProblem(m.Log, "runner_update_failed", p)
}
func preparationFits(s Snapshot, a RunnerArtifact) bool {
	used, count, vms := usage(s)
	if used.CPU+a.Resources.CPU > s.Config.Host.CPU || used.MemoryMiB+a.Resources.MemoryMiB > s.Config.Host.MemoryMiB || count >= s.Config.Host.MaxRunners || (a.Backend == Tart && vms >= 2) {
		return false
	}
	if a.Backend == Docker && validResources(s.Config.DockerBudget) {
		used := dockerUsage(s)
		return used.CPU+a.Resources.CPU <= s.Config.DockerBudget.CPU && used.MemoryMiB+a.Resources.MemoryMiB <= s.Config.DockerBudget.MemoryMiB
	}
	return true
}
func (m *Manager) abandonArtifact(ctx context.Context, id string) {
	_ = m.Store.Update(func(s *Snapshot) error {
		if a := s.Artifacts[id]; a != nil {
			a.Phase = ArtifactRemoving
		}
		return nil
	})
	// Cancellation must not erase cleanup ownership. A separate bounded attempt
	// either confirms cleanup or leaves the reservation for the next reconciliation.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = m.cleanupManaged(cleanup)
}
func artifactReferenced(s Snapshot, id string) bool {
	a := s.Artifacts[id]
	if a == nil {
		return false
	}
	for _, q := range s.Managed {
		if q.CurrentArtifact == id || q.PreviousArtifact == id || q.BaseImage == a.Image && a.Image != "" {
			return true
		}
	}
	for _, p := range s.Pools {
		if p.Phase != Retired && p.Spec.Image == a.Image && a.Image != "" {
			return true
		}
	}
	for _, r := range s.Runners {
		if r.Phase != Completed && r.Image == a.Image && a.Image != "" {
			return true
		}
	}
	// An explicit user pin is also a live reference, including a paused pool.
	for _, p := range s.Requested.Pools {
		if p.Image == a.Image && a.Image != "" {
			return true
		}
	}
	return false
}
func (m *Manager) cleanupManaged(ctx context.Context) error {
	s := m.Store.View()
	ids := make([]string, 0, len(s.Artifacts))
	for id := range s.Artifacts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s = m.Store.View()
		a := s.Artifacts[id]
		if a == nil || artifactReferenced(s, id) || time.Now().Before(a.NextCleanup) {
			continue
		}
		if err := m.Store.Update(func(v *Snapshot) error {
			if a := v.Artifacts[id]; a != nil {
				a.Phase = ArtifactRemoving
			}
			return nil
		}); err != nil {
			return err
		}
		if err := m.RunnerBuilder.Cleanup(ctx, s.Config, *a); err != nil {
			_ = m.Store.Update(func(v *Snapshot) error {
				if a := v.Artifacts[id]; a != nil {
					a.Problem = classify(err, ErrCleanup, "Managed artifact cleanup is pending.", "Restore backend connectivity; ownership and reservations are retained.")
					a.CleanupAttempts++
					a.NextCleanup = time.Now().Add(managedRetryDelay(a.CleanupAttempts, a.Problem))
				}
				return nil
			})
			return err
		}
		if err := m.Store.Update(func(v *Snapshot) error {
			delete(v.Artifacts, id)
			for _, q := range v.Managed {
				if q.Candidate == id {
					q.Candidate = ""
					if q.Phase == UpdatePreparing {
						q.NextCheck = time.Time{}
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
func defaultReleaseClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 || req.URL.Scheme != "https" || req.URL.User != nil {
			return releaseProblem()
		}
		req.Header.Del("Authorization")
		return nil
	}}
}

func displayResolved(s Snapshot) Config {
	c := s.Requested
	c.Pools = append([]Pool(nil), c.Pools...)
	for i, p := range c.Pools {
		if q := s.Managed[p.Name]; q != nil && q.Current != nil {
			c.Pools[i] = *q.Current
		}
	}
	return c
}

func managedRetryDelay(attempt int, p *Problem) time.Duration {
	delay := min(time.Hour, 5*time.Second*time.Duration(1<<min(max(attempt, 0), 10)))
	if p != nil && !p.RetryAt.IsZero() {
		delay = max(delay, time.Until(p.RetryAt))
	}
	return delay
}
