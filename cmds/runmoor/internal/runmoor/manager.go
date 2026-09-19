package runmoor

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/actions/scaleset"
)

type Manager struct {
	Store         *Store
	ConfigPath    string
	Log           *slog.Logger
	RemoteFactory func(Connection) (Remote, error)
	Drivers       DriverFactory
	Images        *ImageManager
	Power         PowerController
	mu            sync.Mutex
	workers       map[string]context.CancelFunc
	poolLoops     map[string]context.CancelFunc
	poolLocks     map[string]*sync.Mutex
	remotes       map[string]Remote
	wg            sync.WaitGroup
	imageMu       sync.Mutex
	reloadMu      sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewManager(store *Store, path string, l *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{Store: store, ConfigPath: path, Log: l, RemoteFactory: NewGitHub, Drivers: defaultDriver, Images: &ImageManager{Store: store, Tart: &TartDriver{Exec: OSCommand{}}}, Power: &PowerManager{}, workers: map[string]context.CancelFunc{}, poolLoops: map[string]context.CancelFunc{}, poolLocks: map[string]*sync.Mutex{}, remotes: map[string]Remote{}, ctx: ctx, cancel: cancel}
}
func (m *Manager) poolLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poolLocks[id] == nil {
		m.poolLocks[id] = &sync.Mutex{}
	}
	return m.poolLocks[id]
}
func (m *Manager) remote(p PoolState) (Remote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.remotes[p.ID]; r != nil {
		return r, nil
	}
	r, e := m.RemoteFactory(p.Connection)
	if e == nil {
		m.remotes[p.ID] = r
	}
	return r, e
}
func (m *Manager) activate(c Config) error { return m.accept(c, true) }
func (m *Manager) accept(c Config, restart bool) error {
	return m.Store.Update(func(s *Snapshot) error {
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
		for _, old := range s.Pools {
			if old.Phase == Retired || old.Phase == Draining {
				continue
			}
			p, ok := wanted[old.Spec.Name]
			conn := c.Connection(p.Connection)
			if ok && fingerprint(old.Spec) == fingerprint(p) && fingerprint(old.Connection) == fingerprint(conn) {
				old.Generation = gen
				matched[p.Name] = true
				continue
			}
			old.Phase = Draining
			old.Demand = 0
		}
		for _, p := range c.Pools {
			if matched[p.Name] {
				continue
			}
			id := newID()
			s.Pools[id] = &PoolState{ID: id, Generation: gen, Spec: p, Connection: c.Connection(p.Connection), Phase: Ready, OwnerLabel: "runmoor-owner-" + s.Installation}
		}
		s.Config = c
		s.Generation = gen
		s.Generations[gen] = c
		if restart {
			s.Stopping = false
		}
		return nil
	})
}
func (m *Manager) Run(ctx context.Context, c Config) error {
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
	if e := m.activate(c); e != nil {
		return e
	}
	// Session secrets are deliberately not persisted. A fresh session uses the
	// stable installation/pool owner and begins from its authoritative statistics.
	if e := m.Store.Update(func(s *Snapshot) error {
		for _, p := range s.Pools {
			p.Session = ""
			p.LastMessage = 0
		}
		return nil
	}); e != nil {
		return e
	}
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
	for _, r := range s.Runners {
		if r.Phase == Cleaning || r.Phase == Quarantined || (r.Terminated && r.Phase != Completed) {
			n++
		}
	}
	return n
}
func (m *Manager) step() error {
	s := m.Store.View()
	active := false
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
	p := classify(err, ErrRetry, "Pool dependency is unavailable.", "Inspect status and retry after restoring the dependency.")
	_ = m.Store.Update(func(s *Snapshot) error {
		v := s.Pools[id]
		if v == nil || v.Phase == Retired {
			return nil
		}
		p.Pool = v.Spec.Name
		v.Problem = p
		if suspend && v.Phase != Draining {
			v.Phase = Suspended
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
		if e == nil {
			probe, cancel := context.WithTimeout(ctx, 60*time.Second)
			p, e = m.ensureScaleSet(probe, id, remote)
			cancel()
			if e == nil && (p == nil || p.ScaleSetID == 0) {
				return
			}
		}
		if e != nil {
			code := classify(e, ErrRetry, "Pool initialization failed.", "Run doctor.").Code
			suspend := code == ErrAuth || code == ErrOwnership || code == ErrImage || code == ErrPlatform || code == ErrRunnerVersion
			m.poolProblem(id, e, suspend)
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
		capacity := p.Spec.MaxRunners
		if !eligible(s, p) {
			capacity = 0
		}
		if capacity > s.Config.Host.MaxRunners {
			capacity = s.Config.Host.MaxRunners
		}
		cost := p.Spec.Cost()
		if n := s.Config.Host.CPU / cost.CPU; capacity > n {
			capacity = n
		}
		if n := int(s.Config.Host.MemoryMiB / cost.MemoryMiB); capacity > n {
			capacity = n
		}
		if p.Spec.Backend == Tart && capacity > 2 {
			capacity = 2
		}
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
		if eligible(current, current.Pools[id]) {
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
	if r == nil {
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
	if e == nil {
		gitID, jit, e = remote.JIT(ctx, *p, *r)
	}
	if e == nil {
		e = m.Store.Update(func(s *Snapshot) error { s.Runners[id].GitHubID = gitID; return nil })
	}
	if e == nil {
		driver, er := m.Drivers(r.Backend)
		e = er
		if e == nil {
			e = driver.Prepare(ctx, c, p.Spec, *r, s, jit, func(h Handle) error {
				return m.Store.Update(func(s *Snapshot) error { s.Runners[id].Handle = h; return nil })
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
func (m *Manager) inspect(ctx context.Context, id string) {
	s := m.Store.View()
	r := s.Runners[id]
	if r == nil {
		return
	}
	c := s.Generations[r.Generation]
	p := s.Pools[r.PoolID]
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if r.Forced || (r.Phase != Idle && time.Now().After(r.Deadline)) {
		_ = m.Store.Update(func(s *Snapshot) error {
			r := s.Runners[id]
			r.Forced = true
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
		m.runnerProblem(id, problem(ErrOwnership, "A live execution has no matching GitHub registration.", "Inspect the job and explicitly force-stop only after confirming it is owned."), true)
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
			rr := v.Runners[id]
			rr.Handle = obs.Handle
			rr.Phase = Busy
			if rr.StartedAt.IsZero() {
				rr.StartedAt = rr.CreatedAt
				rr.Deadline = rr.CreatedAt.Add(c.JobTimeout())
			}
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
				r := s.Runners[id]
				if r.Phase == Idle {
					r.Phase = Busy
					if r.StartedAt.IsZero() {
						r.StartedAt = nowUTC()
						r.Deadline = r.StartedAt.Add(s.Generations[r.Generation].JobTimeout())
					}
				}
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
					rr := s.Runners[id]
					rr.Phase = Busy
					if rr.StartedAt.IsZero() {
						rr.StartedAt = nowUTC()
						rr.Deadline = rr.StartedAt.Add(c.JobTimeout())
					}
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
	_ = m.Store.Update(func(s *Snapshot) error { s.Pools[id].Phase = Retired; return nil })
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
	if len(ids) == 0 {
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
			for _, p := range s.Pools {
				if p.Phase == Ready {
					p.Phase = Paused
				}
			}
		}
		for _, id := range ids {
			s.Pools[id].Phase = Ready
			s.Pools[id].Session = ""
			s.Pools[id].PreparationFailures = 0
			s.Pools[id].Problem = nil
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
	if c.Storage != s.Config.Storage {
		return problem(ErrConfig, "Storage locations cannot change during reload.", "Drain and stop before restoring a complete installation into new locations.")
	}
	for _, p := range c.Pools {
		if e = m.validatePool(ctx, c, p, s); e != nil {
			return e
		}
	}
	if e = m.accept(c, false); e != nil {
		return e
	}
	m.Log.Info("configuration_accepted", "generation", m.Store.View().Generation)
	return nil
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
