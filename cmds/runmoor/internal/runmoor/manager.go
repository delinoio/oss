package runmoor

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/actions/scaleset"
)

type Manager struct {
	// Service reload starts from committed configuration without revoking Stop.
	PreserveStop    bool
	ResolveCapacity func(context.Context, Config) (Config, error)
	Store           *Store
	ConfigPath      string
	Log             *slog.Logger
	RemoteFactory   func(Connection) (Remote, error)
	Drivers         DriverFactory
	Images          *ImageManager
	Power           PowerController
	ReleaseClient   *http.Client
	RunnerBuilder   RunnerImageBuilder
	mu              sync.Mutex
	workers         map[string]context.CancelFunc
	poolLoops       map[string]context.CancelFunc
	poolLocks       map[string]*sync.Mutex
	remotes         map[string]Remote
	remoteKeys      map[string]string
	wg              sync.WaitGroup
	imageMu         sync.Mutex
	reloadMu        sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
}

func NewManager(store *Store, path string, l *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{Store: store, ConfigPath: path, Log: l, RemoteFactory: NewGitHub, Drivers: defaultDriver, Images: &ImageManager{Store: store, Tart: &TartDriver{Exec: OSCommand{}}}, Power: &PowerManager{}, workers: map[string]context.CancelFunc{}, poolLoops: map[string]context.CancelFunc{}, poolLocks: map[string]*sync.Mutex{}, remotes: map[string]Remote{}, remoteKeys: map[string]string{}, ctx: ctx, cancel: cancel}
	m.Drivers = func(kind Backend) (Driver, error) {
		d, err := defaultDriver(kind)
		if h, ok := d.(*HostDriver); ok {
			h.Store = store
		}
		return d, err
	}
	m.ResolveCapacity = resolveDockerCapacity
	m.ReleaseClient = defaultReleaseClient()
	m.RunnerBuilder = &ManagedImageBuilder{Store: store, Images: m.Images, Client: m.ReleaseClient, Log: l}
	return m
}
func (m *Manager) poolLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.Store.View().Pools[id]; p == nil || p.Phase == Retired {
		return nil
	}
	if m.poolLocks[id] == nil {
		m.poolLocks[id] = &sync.Mutex{}
	}
	return m.poolLocks[id]
}
func (m *Manager) remote(p PoolState) (Remote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.Store.View()
	current := s.Pools[p.ID]
	if current == nil || current.Phase == Retired {
		return nil, problem(ErrRetry, "Pool generation has retired.", "Use the active pool generation shown by status.")
	}
	connection := current.Connection
	key := fingerprint(connection)
	if current.Phase == Draining {
		if replacement, ok := s.RetirementAuthority[p.ID]; ok && poolIdentity(current.Spec, replacement) == poolIdentity(current.Spec, current.Connection) {
			connection = replacement
			key = fingerprint(struct {
				Connection Connection
				Validation string
			}{replacement, s.RetirementValidation[p.ID]})
		}
	}
	prior := m.remotes[p.ID]
	if prior != nil && m.remoteKeys[p.ID] == key {
		return prior, nil
	}
	r, e := m.RemoteFactory(connection)
	if e == nil {
		if prior != nil && current.Phase == Draining {
			m.Log.Info("pool_cleanup_authority_switched", "pool", current.Spec.Name, "pool_id", p.ID)
		}
		m.remotes[p.ID] = r
		m.remoteKeys[p.ID] = key
	}
	return r, e
}
func (m *Manager) activate(c Config) error { return m.accept(c, true) }
func (m *Manager) accept(c Config, restart bool) error {
	return m.acceptWithValidatedReload(c, restart, false)
}
func (m *Manager) acceptWithValidatedReload(c Config, restart, validatedReload bool) error {
	return m.Store.Update(func(s *Snapshot) error {
		stopping := s.Stopping
		if validatedReload {
			recordValidatedManagedRecovery(s, c)
		}
		initializeManaged(s, c)
		if restart {
			s.ReleaseChecked = time.Time{}
			for _, q := range s.Managed {
				q.NextCheck = time.Time{}
			}
		}
		err := acceptSnapshotWithValidatedReload(s, managedConfig(*s), restart, validatedReload)
		if m.PreserveStop && stopping {
			s.Stopping = true
		}
		return err
	})
}
func acceptSnapshot(s *Snapshot, c Config, restart bool) error {
	return acceptSnapshotWithValidatedReload(s, c, restart, false)
}
func acceptSnapshotWithValidatedReload(s *Snapshot, c Config, restart, validatedReload bool) error {
	if validatedReload {
		refreshRetirementAuthority(s, c)
	}
	if s.Generation != "" && fingerprint(s.Config) == fingerprint(c) {
		if restart {
			s.Stopping = false
		}
		return nil
	}
	gen := newID()
	wanted := map[string]Pool{}
	for _, p := range c.Pools {
		wanted[p.Name] = p
	}
	matched := map[string]bool{}
	previous := map[string]PoolState{}
	for _, old := range s.Pools {
		if old.Phase == Retired || old.Phase == Draining {
			continue
		}
		p, ok := wanted[old.Spec.Name]
		conn := c.Connection(p.Connection)
		recover := ok && old.Phase == Suspended && !s.Paused && !s.Stopping && (validatedReload && suspensionCorrected(*old, p, conn, s.Config, c) || managedRecoveryMatches(s, old))
		if ok && fingerprint(old.Spec) == fingerprint(p) && fingerprint(old.Connection) == fingerprint(conn) && !recover {
			old.Generation = gen
			matched[p.Name] = true
			continue
		}
		previous[old.Spec.Name] = *old
		if validatedReload && poolIdentity(old.Spec, old.Connection) == poolIdentity(p, conn) && authChanged(old.Connection, conn) {
			s.RetirementAuthority[old.ID] = conn
			s.RetirementValidation[old.ID] = newID()
		}
		old.Phase = Draining
		old.Demand = 0
	}
	for _, p := range c.Pools {
		if matched[p.Name] {
			continue
		}
		id := newID()
		conn := c.Connection(p.Connection)
		next := &PoolState{ID: id, Generation: gen, Spec: p, Connection: conn, Phase: Ready, OwnerLabel: "runmoor-owner-" + s.Installation}
		if old, ok := previous[p.Name]; ok {
			switch old.Phase {
			case Paused:
				next.Phase = Paused
			case Suspended:
				if s.Paused || s.Stopping || !(validatedReload && suspensionCorrected(old, p, conn, s.Config, c) || managedRecoveryMatches(s, &old)) {
					next.Phase = Suspended
					next.PreparationFailures = old.PreparationFailures
					next.SuspensionSource = old.SuspensionSource
					if old.Problem != nil {
						copy := *old.Problem
						next.Problem = &copy
					}
				}
			}
			if recovery := s.ManagedRecovery[p.Name]; recovery != nil && recovery.PoolID == old.ID {
				delete(s.ManagedRecovery, p.Name)
			}
		}
		if q := s.Managed[p.Name]; q != nil && q.Paused {
			next.Phase = Paused
		}
		s.Pools[id] = next
	}
	s.Config = c
	s.Generation = gen
	s.Generations[gen] = c
	if restart {
		s.Stopping = false
	}
	return nil
}

func refreshRetirementAuthority(s *Snapshot, c Config) {
	if s.RetirementAuthority == nil {
		s.RetirementAuthority = map[string]Connection{}
	}
	if s.RetirementValidation == nil {
		s.RetirementValidation = map[string]string{}
	}
	wanted := map[string]Pool{}
	for _, p := range c.Pools {
		wanted[p.Name] = p
	}
	for _, old := range s.Pools {
		if old.Phase != Draining {
			continue
		}
		next, ok := wanted[old.Spec.Name]
		if !ok {
			continue
		}
		conn := c.Connection(next.Connection)
		if poolIdentity(old.Spec, old.Connection) != poolIdentity(next, conn) {
			continue
		}
		// A new validation must supersede both a revoked replacement and a
		// cached client for an older credential reference with the same name.
		s.RetirementAuthority[old.ID] = conn
		s.RetirementValidation[old.ID] = newID()
	}
}

func suspensionCorrected(old PoolState, next Pool, conn Connection, oldConfig, newConfig Config) bool {
	if old.Problem == nil {
		return false
	}
	oldPool := old.Spec
	switch old.Problem.Code {
	case ErrAuth:
		return authChanged(old.Connection, conn) || !strings.EqualFold(normalizedRunnerGroup(oldPool.RunnerGroup), normalizedRunnerGroup(next.RunnerGroup))
	case ErrOwnership:
		return old.SuspensionSource == SuspensionScaleSet && poolIdentity(oldPool, old.Connection) != poolIdentity(next, conn)
	case ErrImage:
		return imageChanged(oldPool, next)
	case ErrRunnerVersion:
		return runnerImageChanged(oldPool, next)
	case ErrPlatform:
		return oldPool.Backend != next.Backend || oldPool.Arch != next.Arch
	case ErrPreparation:
		if executionChanged(oldPool, next) {
			return true
		}
		switch oldPool.Backend {
		case Docker:
			return oldConfig.DockerSocket != newConfig.DockerSocket || oldConfig.Timeouts.DockerPreparation != newConfig.Timeouts.DockerPreparation
		case Host:
			return oldConfig.Timeouts.HostPreparation != newConfig.Timeouts.HostPreparation
		case Tart:
			return oldConfig.TartExecutable != newConfig.TartExecutable || oldConfig.Timeouts.TartPreparation != newConfig.Timeouts.TartPreparation
		}
	}
	return old.PreparationFailures >= 3 && executionChanged(oldPool, next)
}

func normalizedRunnerGroup(group string) string {
	if group == "" {
		return "default"
	}
	return group
}

func authChanged(a, b Connection) bool {
	return a.Target != b.Target || a.Auth != b.Auth || a.Credential != b.Credential || a.ClientID != b.ClientID || a.InstallationID != b.InstallationID
}
func imageChanged(a, b Pool) bool {
	return runnerImageChanged(a, b) || a.DaemonImage != b.DaemonImage
}
func runnerImageChanged(a, b Pool) bool {
	return a.Backend != b.Backend || a.Arch != b.Arch || a.Image != b.Image || fingerprint(a.ImageSource) != fingerprint(b.ImageSource) || a.RunnerPath != b.RunnerPath || a.RunnerVersion != b.RunnerVersion
}
func executionChanged(a, b Pool) bool {
	return imageChanged(a, b) || a.Mode != b.Mode || a.Resources != b.Resources || a.DaemonResources != b.DaemonResources
}
func (m *Manager) Run(ctx context.Context, c Config) error {
	if err := m.initializeRun(c); err != nil {
		return err
	}
	return m.runActivated(ctx)
}

// Complete startup before exposing control: a reload received immediately
// after readiness must not be overwritten by another startup acceptance.
func (m *Manager) initializeRun(c Config) error {
	if err := m.activate(c); err != nil {
		return err
	}
	// Session secrets are deliberately not persisted. A fresh session uses the
	// stable installation/pool owner and begins from its authoritative statistics.
	return m.Store.Update(func(s *Snapshot) error {
		for _, p := range s.Pools {
			p.Session = ""
			p.LastMessage = 0
		}
		return nil
	})
}

func (m *Manager) runActivated(ctx context.Context) error {
	defer func() {
		m.cancel()
		m.mu.Lock()
		for _, cancel := range m.workers {
			cancel()
		}
		for _, cancel := range m.poolLoops {
			cancel()
		}
		m.mu.Unlock()
		m.wg.Wait()
		m.Power.Release()
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	signal := ctx.Done()
	lastPrune := time.Time{}
	for {
		if e := m.step(); e != nil {
			return e
		}
		s := m.Store.View()
		if m.readyToStop() {
			m.Log.Info("manager_stopped", "pending_cleanup", pendingCleanup(s))
			return nil
		}
		if time.Since(lastPrune) > time.Minute {
			if e := m.Store.Prune(time.Now()); e != nil {
				return e
			}
			if e := PruneDiagnostics(diagnosticDir(s.Config), time.Now(), 0); e != nil {
				return e
			}
			lastPrune = time.Now()
		}
		select {
		case <-signal:
			signal = nil
			if e := m.Stop(false); e != nil {
				return e
			}
		case <-tick.C:
		case <-m.ctx.Done():
			return nil
		}
	}
}
func allTerminated(s Snapshot) bool {
	for _, a := range s.Artifacts {
		if a.Reserved || a.Phase == ArtifactRemoving {
			return false
		}
	}
	for _, im := range s.Images {
		if im.Phase == ImageOpen || im.Phase == ImageRemoving {
			return false
		}
	}
	for _, r := range s.Runners {
		if r.Phase != Completed && (!r.Terminated || !r.LocalCleaned) {
			return false
		}
	}
	return true
}
func (m *Manager) readyToStop() bool {
	// An image operation may still be publishing its durable reservation or
	// finishing cleanup. Keep lifetime ownership until it leaves this boundary.
	if !m.imageMu.TryLock() {
		return false
	}
	defer m.imageMu.Unlock()
	s := m.Store.View()
	return s.Stopping && allTerminated(s)
}
func pendingCleanup(s Snapshot) int {
	n := 0
	for _, a := range s.Artifacts {
		if a.Phase == ArtifactRemoving {
			n++
		}
	}
	for _, r := range s.Runners {
		if r.Phase == Cleaning || r.Phase == Quarantined || (r.Terminated && r.Phase != Completed) {
			n++
		}
	}
	return n
}
func (m *Manager) step() error {
	m.startManagedWork()
	s := m.Store.View()
	active := false
	for _, a := range s.Artifacts {
		if a.Reserved || a.Phase == ArtifactRemoving {
			active = true
		}
	}
	for _, r := range s.Runners {
		if !r.Terminated && (r.Phase == Busy || r.Phase == Preparing || r.Phase == Cleaning || r.Phase == Quarantined) {
			active = true
		}
	}
	for _, im := range s.Images {
		if im.Phase == ImageOpen || im.Phase == ImageRemoving {
			active = true
		}
	}
	if p := m.Power.Set(active); p != nil {
		logProblem(m.Log, "sleep_inhibition_failed", p)
		_ = m.Store.Update(func(s *Snapshot) error { s.PowerProblem = p; return nil })
	} else if s.PowerProblem != nil {
		_ = m.Store.Update(func(s *Snapshot) error { s.PowerProblem = nil; return nil })
	}
	for id, p := range s.Pools {
		if p.Phase == Retired {
			continue
		}
		if p.Phase != Suspended {
			m.ensurePoolLoop(id)
		}
		if p.Phase == Draining {
			total := 0
			for _, r := range s.Runners {
				if r.PoolID == id && r.Phase != Completed {
					total++
				}
			}
			if total == 0 {
				m.startWork("pool-"+id, func(ctx context.Context) { m.retirePool(ctx, id) })
			}
		}
	}
	retiring := map[string]bool{}
	for _, id := range retirementCandidates(s) {
		retiring[id] = true
		id := id
		m.startWork(id, func(ctx context.Context) { m.retireRunner(ctx, id) })
	}
	for id, r := range s.Runners {
		if r.Phase == Completed || r.Phase == Quarantined || retiring[id] {
			continue
		}
		id := id
		if r.Phase == Cleaning {
			m.startWork(id, func(ctx context.Context) { m.cleanup(ctx, id) })
			continue
		}
		m.mu.Lock()
		_, working := m.workers[id]
		m.mu.Unlock()
		if working {
			if r.Forced {
				m.mu.Lock()
				if cancel := m.workers[id]; cancel != nil {
					cancel()
				}
				m.mu.Unlock()
			}
			continue
		}
		m.startWork(id, func(ctx context.Context) { m.inspect(ctx, id) })
	}
	// Setup commands are serialized separately; inspecting their VM state must
	// not release a reservation while create/seal is still running.
	if m.imageMu.TryLock() {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		_ = m.Images.Reconcile(ctx, s.Config)
		cancel()
		m.imageMu.Unlock()
	}
	if s.Stopping || s.Paused {
		return nil
	}
	if e := diskCheck(s.Config); e != nil {
		p := classify(e, ErrDisk, "Disk reserve is unavailable.", "Free disk space.")
		return m.Store.Update(func(v *Snapshot) error {
			for _, pool := range v.Pools {
				if pool.Phase == Ready {
					q := *p
					q.Pool = pool.Spec.Name
					pool.Problem = &q
				}
			}
			return nil
		})
	}
	var created []string
	var capacityProblems []*Problem
	err := m.Store.Update(func(v *Snapshot) error {
		for _, poolID := range Schedule(*v) {
			p := v.Pools[poolID]
			id := newID()
			r := &Runner{ID: id, PoolID: poolID, Generation: p.Generation, Name: "runmoor-" + id, Phase: Preparing, Backend: p.Spec.Backend, Resources: p.Spec.Cost(), Image: p.Spec.Image, CreatedAt: nowUTC(), Deadline: time.Now().Add(v.Config.Preparation(p.Spec.Backend)).UTC()}
			v.Runners[id] = r
			p.Problem = nil
			created = append(created, id)
		}

		for _, p := range v.Pools {
			if !eligible(*v, p) {
				continue
			}
			count, busy := liveCount(*v, p.ID)
			target := max(p.Demand, busy+p.Spec.MinIdle)
			if count < target {
				if p.Problem == nil || p.Problem.Code == ErrCapacity || p.Problem.Code == ErrDisk {
					q := problem(ErrCapacity, "Available host or pool capacity is below requested demand or the idle target.", "Wait for owned jobs or image setup to finish, or adjust explicit budgets; running work is preserved.")
					q.Pool = p.Spec.Name
					if p.Problem == nil || p.Problem.Code != ErrCapacity {
						capacityProblems = append(capacityProblems, q)
					}
					p.Problem = q
				}
			} else if p.Problem != nil && (p.Problem.Code == ErrCapacity || p.Problem.Code == ErrDisk) {
				p.Problem = nil
			}
		}
		v.Cursor++
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range capacityProblems {
		logProblem(m.Log, "capacity_wait", p)
	}
	for _, id := range created {
		id := id
		m.startWork(id, func(ctx context.Context) { m.prepare(ctx, id) })
	}
	return nil
}
func (m *Manager) startWork(id string, fn func(context.Context)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[id]; ok {
		return false
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.workers[id] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); delete(m.workers, id); m.mu.Unlock() }()
		fn(ctx)
	}()
	return true
}
func (m *Manager) ensurePoolLoop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.poolLoops[id]; ok {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.poolLoops[id] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); delete(m.poolLoops, id); m.mu.Unlock() }()
		m.poolLoop(ctx, id)
	}()
}
func (m *Manager) poolProblem(id string, err error, suspend bool) {
	m.poolProblemWithSource(id, err, suspend, SuspensionUnknown)
}
func (m *Manager) poolProblemWithSource(id string, err error, suspend bool, source SuspensionSource) {
	p := classify(err, ErrRetry, "Pool dependency is unavailable.", "Inspect status and retry after restoring the dependency.")
	_ = m.Store.Update(func(s *Snapshot) error {
		v := s.Pools[id]
		if v == nil || v.Phase == Retired {
			return nil
		}
		p.Pool = v.Spec.Name
		v.Problem = p
		v.SuspensionSource = SuspensionUnknown
		if suspend && v.Phase != Draining {
			v.Phase = Suspended
			if p.Code == ErrOwnership {
				v.SuspensionSource = source
			}
		}
		return nil
	})
	logProblem(m.Log, "pool_dependency_failed", p)
}
func (m *Manager) poolLoop(ctx context.Context, id string) {
	attempt := 0
	for ctx.Err() == nil {
		s := m.Store.View()
		p := s.Pools[id]
		if p == nil || p.Phase == Retired || p.Phase == Suspended {
			return
		}
		if p.Phase == Ready && p.Spec.Backend == Docker && s.Config.DockerCapacityPending && !s.Stopping {
			if err := m.retryDockerCapacity(ctx); err != nil {
				m.poolProblem(id, err, false)
				if !waitContext(ctx, retryDelay(attempt, err)) {
					return
				}
				attempt++
			}
			continue
		}
		blocked := false
		for _, other := range s.Pools {
			if other.ID != id && other.Phase != Retired && other.Phase == Draining && poolIdentity(other.Spec, other.Connection) == poolIdentity(p.Spec, p.Connection) {
				blocked = true
			}
		}
		if blocked {
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		remote, e := m.remote(*p)
		if e == nil {
			driver, er := m.Drivers(p.Spec.Backend)
			if er != nil {
				e = er
			} else if p.Phase == Ready {
				probe, cancel := context.WithTimeout(ctx, 30*time.Second)
				e = driver.Validate(probe, s.Config, p.Spec, s)
				cancel()
			}
		}
		source := SuspensionUnknown
		if e == nil {
			probe, cancel := context.WithTimeout(ctx, 60*time.Second)
			p, e = m.ensureScaleSet(probe, id, remote)
			cancel()
			if e != nil {
				source = SuspensionScaleSet
			}
			if e == nil && (p == nil || p.ScaleSetID == 0) {
				return
			}
		}
		if e != nil {
			code := classify(e, ErrRetry, "Pool initialization failed.", "Run doctor.").Code
			suspend := code == ErrAuth || code == ErrOwnership || code == ErrImage || code == ErrPlatform || code == ErrRunnerVersion
			m.poolProblemWithSource(id, e, suspend, source)
			if suspend {
				return
			}
			if !waitContext(ctx, retryDelay(attempt, e)) {
				return
			}
			attempt++
			continue
		}
		session, e := remote.Session(ctx, *p, s.Installation+"/"+p.ID)
		if e == nil {
			e = m.Store.Update(func(v *Snapshot) error {
				q := v.Pools[id]
				if q == nil || q.Phase == Retired || ctx.Err() != nil {
					return problem(ErrRetry, "Pool session initialization was interrupted.", "Use the active pool generation shown by status.")
				}
				q.Session = session.ID()
				q.LastMessage = 0
				q.Demand = session.InitialDemand()
				return nil
			})
		}
		if e == nil {
			attempt = 0
			e = m.listen(ctx, id, session)
		}
		if session != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = session.Close(closeCtx)
			cancel()
		}
		_ = m.Store.Update(func(v *Snapshot) error {
			if p := v.Pools[id]; p != nil {
				p.Session = ""
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			p := classify(e, ErrRetry, "GitHub session interrupted.", "Wait for reconnect.")
			m.poolProblem(id, e, p.Code == ErrAuth)
			if p.Code == ErrAuth {
				return
			}
			m.Log.Info("github_retry_scheduled", "pool", id, "attempt", attempt)
			if !waitContext(ctx, retryDelay(attempt, e)) {
				return
			}
			attempt++
		}
	}
}
func (m *Manager) ensureScaleSet(ctx context.Context, id string, remote Remote) (*PoolState, error) {
	lock := m.poolLock(id)
	if lock == nil {
		return nil, nil
	}
	lock.Lock()
	defer lock.Unlock()
	return m.ensureScaleSetLocked(ctx, id, remote)
}

// Initialization and retirement share this lock through both the remote call
// and its durable publication. Reload can still drain immediately; the intent
// transaction below rechecks that phase before allowing a new remote creation.
func (m *Manager) ensureScaleSetLocked(ctx context.Context, id string, remote Remote) (*PoolState, error) {
	p := m.Store.View().Pools[id]
	if p == nil || p.Phase == Retired || p.Phase == Suspended {
		return nil, nil
	}
	if p.Phase == Draining && p.ScaleSetID == 0 && !p.CreatePending {
		return p, nil
	}
	scaleID, err := remote.Ensure(ctx, *p, func() error {
		return m.Store.Update(func(s *Snapshot) error {
			p := s.Pools[id]
			if s.Stopping || p == nil || (p.Phase != Ready && p.Phase != Paused) {
				return problem(ErrRetry, "Pool initialization was interrupted by drain or stop.", "Wait for the previous pool generation to finish cleanup.")
			}
			p.CreatePending = true
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if err = m.Store.Update(func(s *Snapshot) error {
		p := s.Pools[id]
		p.ScaleSetID = scaleID
		p.CreatePending = false
		p.Problem = nil
		return nil
	}); err != nil {
		return nil, err
	}
	return m.Store.View().Pools[id], nil
}
func (m *Manager) listen(ctx context.Context, id string, session RemoteSession) error {
	for ctx.Err() == nil {
		s := m.Store.View()
		p := s.Pools[id]
		if p == nil || p.Phase == Retired || p.Phase == Suspended {
			return nil
		}
		capacity := acquisitionCapacity(s, p)
		msg, e := session.Poll(ctx, p.LastMessage, capacity)
		if e != nil {
			return e
		}
		if msg == nil {
			continue
		}
		if e = m.Store.Update(func(s *Snapshot) error { return applyMessage(s, id, session.ID(), msg) }); e != nil {
			return e
		}
		// Acquiring offered jobs never changes demand locally. Only the next
		// authoritative statistics snapshot changes the scheduler's target.
		current := m.Store.View()
		if capacity = min(capacity, acquisitionCapacity(current, current.Pools[id])); capacity > 0 {
			ids := []int64{}
			for _, job := range msg.JobAvailableMessages {
				if len(ids) >= capacity {
					break
				}
				ids = append(ids, job.RunnerRequestID)
			}
			if e = session.Acquire(ctx, ids); e != nil {
				return e
			}
		}
		if e = session.Ack(ctx, msg.MessageID); e != nil {
			return e
		}
	}
	return ctx.Err()
}
func applyMessage(s *Snapshot, poolID, session string, msg *scaleset.RunnerScaleSetMessage) error {
	p := s.Pools[poolID]
	if p == nil || p.Session != session {
		return problem(ErrRetry, "A stale GitHub session attempted to update state.", "Reconnect the pool session.")
	}
	if msg.Statistics == nil || msg.Statistics.TotalAssignedJobs < 0 {
		return problem(ErrRetry, "GitHub returned invalid demand statistics.", "Wait for a valid statistics snapshot.")
	}
	if msg.MessageID < p.LastMessage {
		return nil
	}
	p.Demand = msg.Statistics.TotalAssignedJobs
	p.LastMessage = msg.MessageID
	match := func(name string, id int) *Runner {
		for _, r := range s.Runners {
			if r.PoolID == poolID && r.Name == name && (r.GitHubID == 0 || r.GitHubID == id) {
				return r
			}
		}
		return nil
	}
	for _, job := range msg.JobStartedMessages {
		if r := match(job.RunnerName, job.RunnerID); r != nil && r.Phase != Completed && !r.CompletedJob && !r.Forced && !r.RemoteRemoved && r.Phase != Quarantined {
			r.GitHubID = job.RunnerID
			if r.StartedAt.IsZero() {
				r.StartedAt = nowUTC()
				if !job.RunnerAssignTime.IsZero() && job.RunnerAssignTime.After(r.CreatedAt) && job.RunnerAssignTime.Before(r.StartedAt) {
					r.StartedAt = job.RunnerAssignTime
				}
				r.Deadline = r.StartedAt.Add(s.Generations[r.Generation].JobTimeout())
			}
			r.Phase = Busy
		}
	}
	for _, job := range msg.JobCompletedMessages {
		if r := match(job.RunnerName, job.RunnerID); r != nil && r.Phase != Completed {
			r.Phase = Cleaning
			r.CompletedJob = true
		}
	}
	return nil
}
func (m *Manager) prepare(ctx context.Context, id string) {
	s := m.Store.View()
	r := s.Runners[id]
	// Scheduling records intent before registering the worker. A force-stop
	// can commit in that gap, so a new worker must validate the durable phase
	// even if its newly created context has never received cancellation.
	if r == nil || r.Forced || r.Phase != Preparing || ctx.Err() != nil {
		return
	}
	p := s.Pools[r.PoolID]
	c := s.Generations[r.Generation]
	ctx, cancel := context.WithDeadline(ctx, r.Deadline)
	defer cancel()
	m.Log.Info("runner_preparing", "pool", p.Spec.Name, "runner", id, "backend", r.Backend)
	remote, e := m.remote(*p)
	var jit string
	var gitID int
	if e == nil && ctx.Err() != nil {
		e = ctx.Err()
	}
	if e == nil {
		gitID, jit, e = remote.JIT(ctx, *p, *r)
	}
	if e == nil {
		e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].GitHubID = gitID; return nil })
	}
	if e == nil && ctx.Err() != nil {
		e = ctx.Err()
	}
	if e == nil {
		driver, er := m.Drivers(r.Backend)
		e = er
		if e == nil {
			// A stop or lifecycle event may commit while JIT registration is in
			// flight, before the worker context receives cancellation. Keep the
			// returned registration journaled, but never provision from stale intent.
			s = m.Store.View()
			r = s.Runners[id]
			if r == nil || r.Forced || r.Phase != Preparing {
				m.Log.Info("runner_preparation_cancelled", "pool", p.Spec.Name, "runner", id)
				return
			}
			if e = ctx.Err(); e != nil {
				m.failPreparation(id, e)
				return
			}
			e = driver.Prepare(ctx, c, p.Spec, *r, s, jit, func(h Handle) error {
				return m.Store.Update(func(s *Snapshot) error {
					s.Runners[id].Handle = h
					if h.PID > 0 {
						if s.RunnerTartStarts == nil {
							s.RunnerTartStarts = map[string]string{}
						}
						s.RunnerTartStarts[id] = h.tartProcessStart
					}
					return nil
				})
			})
		}
	}
	if e != nil {
		m.failPreparation(id, e)
		return
	}
	cancelled := false
	if e = m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		if r.Forced {
			cancelled = true
			return nil
		}
		if r.Phase == Preparing {
			r.Phase = Idle
			r.Deadline = r.CreatedAt.Add(c.JobTimeout())
		}
		s.Pools[r.PoolID].PreparationFailures = 0
		return nil
	}); e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	if cancelled {
		m.Log.Info("runner_preparation_cancelled", "pool", p.Spec.Name, "runner", id)
	} else {
		m.Log.Info("runner_prepared", "pool", p.Spec.Name, "runner", id, "duration_ms", time.Since(r.CreatedAt).Milliseconds())
	}
}
func (m *Manager) failPreparation(id string, err error) {
	p := classify(err, ErrPreparation, "Runner preparation failed.", "Inspect doctor and image compatibility, then resume the pool.")
	cancelled := false
	_ = m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		p.Runner = id
		p.Pool = s.Pools[r.PoolID].Spec.Name
		if r.Forced {
			// Cancellation is an operator/deadline decision, not evidence that
			// the pool image is broken. Preserve any existing timeout diagnostic.
			r.Phase = Cleaning
			cancelled = true
			return nil
		}
		r.Problem = p
		if r.Phase != Busy || r.Forced {
			r.Phase = Cleaning
		}
		pool := s.Pools[r.PoolID]
		pool.PreparationFailures++
		if pool.Phase != Draining && (pool.PreparationFailures >= 3 || p.Code == ErrAuth || p.Code == ErrRunnerVersion || p.Code == ErrOwnership) {
			pool.Phase = Suspended
			pool.Problem = p
			pool.SuspensionSource = SuspensionUnknown
		}
		return nil
	})
	if cancelled {
		m.Log.Info("runner_preparation_cancelled", "pool", p.Pool, "runner", id)
	} else {
		logProblem(m.Log, "runner_preparation_failed", p)
	}
}
func (m *Manager) runnerProblem(id string, err error, quarantine bool) {
	p := classify(err, ErrRetry, "Runner reconciliation failed.", "Restore the dependency and retry cleanup.")
	_ = m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		if r == nil {
			return nil
		}
		p.Runner = id
		if pool := s.Pools[r.PoolID]; pool != nil {
			p.Pool = pool.Spec.Name
		}
		r.Problem = p
		if quarantine || p.Code == ErrOwnership {
			r.Phase = Quarantined
		}
		return nil
	})
	logProblem(m.Log, "runner_reconciliation_failed", p)
}

// recordBusyRemoval applies a GitHub busy response only while the runner still
// has the lifecycle phase that initiated removal. A completion or operator
// transition may commit while the request is in flight, so the response must
// not roll that newer state back to Busy.
func recordBusyRemoval(s *Snapshot, id string, expected RunnerPhase, handle *Handle) {
	r := s.Runners[id]
	if r == nil || r.ID != id || r.Phase != expected || r.Forced || r.CompletedJob || r.RemoteRemoved || r.Terminated {
		return
	}
	if r.StartedAt.IsZero() {
		c, ok := s.Generations[r.Generation]
		if !ok {
			return
		}
		// Without an observed start, creation time is the conservative bound.
		r.StartedAt = r.CreatedAt
		r.Deadline = r.CreatedAt.Add(c.JobTimeout())
	}
	if handle != nil {
		r.Handle = *handle
	}
	r.Phase = Busy
}

func runnerInspectionPending(r *Runner) bool {
	return r != nil && !r.CompletedJob && !r.RemoteRemoved && !r.Terminated &&
		(r.Phase == Preparing || r.Phase == Idle || r.Phase == Busy)
}

func (m *Manager) inspect(ctx context.Context, id string) {
	s := m.Store.View()
	r := s.Runners[id]
	// Work selected by step may already have yielded to completion or cleanup
	// before its worker starts. Forced active work still takes the path below.
	if !runnerInspectionPending(r) {
		return
	}
	c := s.Generations[r.Generation]
	p := s.Pools[r.PoolID]
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if r.Forced || (r.Phase != Idle && time.Now().After(r.Deadline)) {
		_ = m.Store.Update(func(s *Snapshot) error {
			r := s.Runners[id]
			if !r.Forced && (r.Phase == Idle || !time.Now().After(r.Deadline)) {
				return nil
			}
			// An expired preparation may have accepted work while we were
			// offline. Only a known busy-job timeout authorizes forced cleanup.
			r.Forced = r.Forced || r.Phase == Busy
			r.Phase = Cleaning
			if r.Problem == nil {
				r.Problem = problem(ErrTimeout, "Runner exceeded its persisted deadline.", "Inspect the failed run in GitHub; Runmoor never reruns jobs.")
			}
			return nil
		})
		return
	}
	driver, e := m.Drivers(r.Backend)
	if e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	obs, e := driver.Inspect(ctx, c, *r, s)
	if e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	if !obs.Exists || !obs.Running {
		_ = m.Store.Update(func(v *Snapshot) error { v.Runners[id].Handle = obs.Handle; v.Runners[id].Phase = Cleaning; return nil })
		return
	}
	remote, e := m.remote(*p)
	if e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	exists, e := remote.Find(ctx, *p, *r)
	if e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	if !exists {
		q := problem(ErrOwnership, "A live execution has no matching GitHub registration.", "Inspect the job and explicitly force-stop only after confirming it is owned.")
		q.Runner, q.Pool = id, p.Spec.Name
		applied := false
		err := m.Store.Update(func(s *Snapshot) error {
			r := s.Runners[id]
			// Ephemeral registration can disappear during completion. Check in
			// the same commit as quarantine so a stale absence cannot undo cleanup.
			// Actual identity errors still use runnerProblem above and quarantine.
			if !runnerInspectionPending(r) || r.Forced {
				return nil
			}
			r.Problem, r.Phase = q, Quarantined
			applied = true
			return nil
		})
		if err != nil {
			q = classify(err, ErrState, "Cannot persist runner inspection.", "Restore state storage and retry reconciliation.")
			q.Runner, q.Pool = id, p.Spec.Name
			logProblem(m.Log, "runner_reconciliation_failed", q)
		} else if applied {
			logProblem(m.Log, "runner_reconciliation_failed", q)
		}
		return
	}
	if r.Phase == Preparing {
		er := remote.Remove(ctx, *p, *r)
		if er == nil {
			_ = m.Store.Update(func(v *Snapshot) error {
				rr := v.Runners[id]
				rr.Handle = obs.Handle
				rr.RemoteRemoved = true
				rr.Phase = Cleaning
				return nil
			})
			return
		}
		if q, ok := er.(*Problem); !ok || q.Code != ErrBusy {
			m.runnerProblem(id, er, false)
			return
		}
		_ = m.Store.Update(func(v *Snapshot) error {
			recordBusyRemoval(v, id, Preparing, &obs.Handle)
			return nil
		})
		return
	}
	_ = m.Store.Update(func(v *Snapshot) error {
		rr := v.Runners[id]
		rr.Handle = obs.Handle
		if rr.Phase == Preparing {
			rr.Phase = Idle
			rr.Deadline = rr.CreatedAt.Add(c.JobTimeout())
		}
		rr.Problem = nil
		return nil
	})
}
func (m *Manager) retireRunner(ctx context.Context, id string) {
	s := m.Store.View()
	r := s.Runners[id]
	if r == nil || r.Phase != Idle {
		return
	}
	p := s.Pools[r.PoolID]
	remote, e := m.remote(*p)
	if e == nil {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		e = remote.Remove(ctx, *p, *r)
	}
	if e != nil {
		if q, ok := e.(*Problem); ok && q.Code == ErrBusy {
			_ = m.Store.Update(func(s *Snapshot) error {
				recordBusyRemoval(s, id, Idle, nil)
				return nil
			})
			return
		}
		m.runnerProblem(id, e, false)
		return
	}
	_ = m.Store.Update(func(s *Snapshot) error { r := s.Runners[id]; r.RemoteRemoved = true; r.Phase = Cleaning; return nil })
}
func (m *Manager) cleanup(ctx context.Context, id string) {
	s := m.Store.View()
	r := s.Runners[id]
	if r == nil {
		return
	}
	p := s.Pools[r.PoolID]
	c := s.Generations[r.Generation]
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	driver, e := m.Drivers(r.Backend)
	if e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	// Completion events and explicit force/timeout authorize termination. Idle
	// scale-down instead removes registration first, preserving assignment races.
	if !r.Forced && !r.CompletedJob && !r.RemoteRemoved && !r.Terminated {
		remote, er := m.remote(*p)
		e = er
		if e == nil {
			e = remote.Remove(ctx, *p, *r)
		}
		if e != nil {
			if q, ok := e.(*Problem); ok && q.Code == ErrBusy {
				_ = m.Store.Update(func(s *Snapshot) error {
					recordBusyRemoval(s, id, Cleaning, nil)
					return nil
				})
				return
			}
			m.runnerProblem(id, e, false)
			return
		}
		if e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].RemoteRemoved = true; return nil }); e != nil {
			return
		}
		r.RemoteRemoved = true
	}
	if !r.Terminated {
		if e = driver.Stop(ctx, c, *r, s); e != nil {
			m.runnerProblem(id, e, false)
			return
		}
		if e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].Terminated = true; return nil }); e != nil {
			m.runnerProblem(id, e, false)
			return
		}
	}
	if !r.DiagnosticsSaved {
		obs, _ := driver.Inspect(ctx, c, *r, s)
		if e = SaveRunnerDiagnostic(c, *r, obs.ExitCode); e != nil {
			m.runnerProblem(id, e, false)
			return
		}
		if e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].DiagnosticsSaved = true; return nil }); e != nil {
			return
		}
	}
	if e = driver.Cleanup(ctx, c, *r, s); e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	if e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].LocalCleaned = true; return nil }); e != nil {
		return
	}
	if !r.RemoteRemoved {
		remote, er := m.remote(*p)
		e = er
		if e == nil {
			e = remote.Remove(ctx, *p, *r)
		}
		if e != nil {
			m.runnerProblem(id, e, false)
			return
		}
		if e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].RemoteRemoved = true; return nil }); e != nil {
			return
		}
	}
	if e = m.Store.Update(func(s *Snapshot) error {
		r := s.Runners[id]
		r.Phase = Completed
		r.CompletedAt = nowUTC()
		r.Problem = nil
		return nil
	}); e != nil {
		m.runnerProblem(id, e, false)
		return
	}
	m.Log.Info("runner_cleaned", "pool", p.Spec.Name, "runner", id)
}
func (m *Manager) retirePool(ctx context.Context, id string) {
	lock := m.poolLock(id)
	if lock == nil {
		return
	}
	lock.Lock()
	defer lock.Unlock()
	s := m.Store.View()
	p := s.Pools[id]
	if p == nil || p.Phase != Draining {
		return
	}
	if p.ScaleSetID != 0 || p.CreatePending {
		remote, e := m.remote(*p)
		if e == nil && p.CreatePending {
			// Recover an interrupted creation by lookup only. An absent result
			// clears the intent; a verified owned result must be deleted before
			// retirement can release this remote identity to a new generation.
			p, e = m.ensureScaleSetLocked(ctx, id, remote)
		}
		if e == nil && p.ScaleSetID != 0 {
			e = remote.DeletePool(ctx, *p)
		}
		if e != nil {
			m.poolProblem(id, e, false)
			return
		}
	}
	if err := m.Store.Update(func(s *Snapshot) error {
		p := s.Pools[id]
		p.Phase, p.Session, p.Demand = Retired, "", 0
		delete(s.RetirementAuthority, id)
		delete(s.RetirementValidation, id)
		return nil
	}); err != nil {
		m.poolProblem(id, err, false)
		return
	}
	m.mu.Lock()
	// Existing lock waiters retain this mutex until they see Retired. The
	// current-state guards above prevent late work from recreating either cache.
	delete(m.remotes, id)
	delete(m.remoteKeys, id)
	delete(m.poolLocks, id)
	if cancel := m.poolLoops[id]; cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	m.Log.Info("pool_retired", "pool", p.Spec.Name, "pool_id", id)
}
func (m *Manager) Stop(force bool) error {
	err := m.Store.Update(func(s *Snapshot) error {
		s.Stopping = true
		if force {
			for _, r := range s.Runners {
				if r.Phase != Completed {
					r.Forced = true
					r.Phase = Cleaning
				}
			}
		}
		return nil
	})
	if err == nil {
		if force {
			m.cancelForcedWorkers("")
			m.mu.Lock()
			if cancel := m.workers["runner-update"]; cancel != nil {
				cancel()
			}
			m.mu.Unlock()
		}
		m.Log.Info("manager_stop_requested", "force", force)
	}
	// Serialize the response with image operations without delaying forced job
	// cancellation. Waiters must not miss an operation still recording intent.
	m.imageMu.Lock()
	m.imageMu.Unlock()
	return err
}
func (m *Manager) StopPool(name string, force bool) error {
	err := m.Store.Update(func(s *Snapshot) error {
		found := false
		if q := s.Managed[name]; q != nil {
			q.Paused = true
			found = true
		}
		for _, p := range s.Pools {
			if p.Spec.Name != name || p.Phase == Retired {
				continue
			}
			found = true
			if p.Phase != Draining {
				p.Phase = Paused
			}
			if force {
				for _, r := range s.Runners {
					if r.PoolID == p.ID && r.Phase != Completed {
						r.Forced = true
						r.Phase = Cleaning
					}
				}
			}
		}
		if !found {
			return problem(ErrConfig, "No active pool has that name.", "Choose a pool shown by status.")
		}
		return nil
	})
	if err == nil && force {
		m.cancelForcedWorkers(name)
		// Cancel only this pool's in-flight candidate. The shared worker may be
		// updating another pool, whose operation must remain unaffected.
		if q := m.Store.View().Managed[name]; q != nil && q.Candidate != "" {
			m.mu.Lock()
			if cancel := m.workers["runner-update"]; cancel != nil {
				cancel()
			}
			m.mu.Unlock()
		}
	}
	return err
}
func (m *Manager) cancelForcedWorkers(name string) {
	s := m.Store.View()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range s.Runners {
		if !r.Forced || (name != "" && s.Pools[r.PoolID].Spec.Name != name) {
			continue
		}
		if cancel := m.workers[id]; cancel != nil {
			cancel()
		}
	}
}
func (m *Manager) Pause(name string) error {
	return m.Store.Update(func(s *Snapshot) error {
		if name == "" {
			s.Paused = true
			return nil
		}
		found := false
		if q := s.Managed[name]; q != nil {
			q.Paused = true
			found = true
		}
		for _, p := range s.Pools {
			if p.Spec.Name == name && p.Phase != Retired && p.Phase != Draining {
				p.Phase = Paused
				found = true
			}
		}
		if !found {
			return problem(ErrConfig, "No active pool has that name.", "Choose a pool shown by status.")
		}
		return nil
	})
}
func (m *Manager) Resume(ctx context.Context, name string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	s := m.Store.View()
	ids := []string{}
	for id, p := range s.Pools {
		if (name == "" || name == p.Spec.Name) && p.Phase != Retired && p.Phase != Draining {
			if e := m.validatePool(ctx, s.Config, p.Spec, s); e != nil {
				return e
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 && !(name != "" && s.Managed[name] != nil) && !(name == "" && len(s.Managed) > 0) {
		return problem(ErrConfig, "No resumable pool matches.", "Inspect status and the active configuration.")
	}
	m.mu.Lock()
	for _, id := range ids {
		delete(m.remotes, id)
		if cancel := m.poolLoops[id]; cancel != nil {
			cancel()
		}
	}
	m.mu.Unlock()
	return m.Store.Update(func(s *Snapshot) error {
		if name == "" {
			s.Paused = false
		} else if s.Paused {
			s.Paused = false
			for _, q := range s.Managed {
				q.Paused = true
			}
			for _, p := range s.Pools {
				if p.Phase == Ready {
					p.Phase = Paused
				}
			}
		}
		for key, q := range s.Managed {
			if name == "" || key == name {
				q.Paused = false
				q.NextCheck = time.Time{}
			}
		}
		for _, id := range ids {
			s.Pools[id].Phase = Ready
			s.Pools[id].Session = ""
			s.Pools[id].PreparationFailures = 0
			s.Pools[id].Problem = nil
			s.Pools[id].SuspensionSource = SuspensionUnknown
		}
		return nil
	})
}
func (m *Manager) validatePool(ctx context.Context, c Config, p Pool, s Snapshot) error {
	driver, e := m.Drivers(p.Backend)
	if e != nil {
		return e
	}
	if e = driver.Validate(ctx, c, p, s); e != nil {
		return e
	}
	remote, e := m.RemoteFactory(c.Connection(p.Connection))
	if e != nil {
		return e
	}
	return remote.Check(ctx, p)
}
func (m *Manager) Reload(ctx context.Context) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	c, e := LoadConfig(m.ConfigPath)
	if e != nil {
		return e
	}
	s := m.Store.View()
	c, e = validateReloadCandidate(ctx, c, s, m.ResolveCapacity, m.Drivers, m.RemoteFactory)
	if e != nil {
		return e
	}
	if e = m.acceptWithValidatedReload(c, false, true); e != nil {
		return e
	}
	committed := m.Store.View()
	for _, old := range s.Pools {
		if old.Phase != Suspended || old.Problem == nil {
			continue
		}
		for _, next := range committed.Pools {
			if next.Generation == committed.Generation && next.Spec.Name == old.Spec.Name && next.Phase == Ready && next.ID != old.ID {
				m.Log.Info("pool_resumed_after_validated_reload", "pool", next.Spec.Name, "previous_error_code", old.Problem.Code, "generation", next.Generation)
			}
		}
	}
	m.Log.Info("configuration_accepted", "generation", committed.Generation)
	return nil
}

// Preflight is also used by the newer CLI before replacing a legacy manager.
// It reads a snapshot and probes dependencies without publishing a generation.
func validateReloadCandidate(ctx context.Context, c Config, s Snapshot, capacity func(context.Context, Config) (Config, error), drivers DriverFactory, remotes func(Connection) (Remote, error)) (Config, error) {
	if capacity != nil {
		var err error
		c, err = capacity(ctx, c)
		if err != nil {
			return c, err
		}
	}
	if c.Storage != s.Config.Storage {
		return c, problem(ErrConfig, "Storage locations cannot change during reload.", "Drain and stop before restoring a complete installation into new locations.")
	}
	for _, p := range c.Pools {
		if !managesRunner(p) {
			driver, err := drivers(p.Backend)
			if err != nil {
				return c, err
			}
			if err = driver.Validate(ctx, c, p, s); err != nil {
				return c, err
			}
		}
		remote, err := remotes(c.Connection(p.Connection))
		if err != nil {
			return c, err
		}
		if err = remote.Check(ctx, p); err != nil {
			return c, err
		}
	}
	return c, nil
}
func sortedPools(s Snapshot) []*PoolState {
	v := make([]*PoolState, 0, len(s.Pools))
	for _, p := range s.Pools {
		v = append(v, p)
	}
	sort.Slice(v, func(i, j int) bool {
		if v[i].Spec.Name == v[j].Spec.Name {
			return v[i].ID < v[j].ID
		}
		return v[i].Spec.Name < v[j].Spec.Name
	})
	return v
}
