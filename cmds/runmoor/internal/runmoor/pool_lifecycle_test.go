package runmoor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type initializingRemote struct {
	*fakeRemote
	afterIntent bool
	entered     chan struct{}
	release     chan struct{}
	deleted     int
}

func (r *initializingRemote) Ensure(ctx context.Context, _ PoolState, mark func() error) (int, error) {
	if r.afterIntent {
		if err := mark(); err != nil {
			return 0, err
		}
	}
	close(r.entered)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-r.release:
	}
	if !r.afterIntent {
		if err := mark(); err != nil {
			return 0, err
		}
	}
	r.mu.Lock()
	r.created++
	r.mu.Unlock()
	return 17, nil
}
func (r *initializingRemote) DeletePool(_ context.Context, p PoolState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = p.ScaleSetID
	return nil
}

func TestPoolRetirementSerializesWithInitializationAcrossReload(t *testing.T) {
	for _, afterIntent := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload before creation intent", true: "reload after creation intent"}[afterIntent], func(t *testing.T) {
			m, c, _, _, pool := testManager(t)
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[pool].ScaleSetID = 0
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			r := &initializingRemote{fakeRemote: &fakeRemote{}, afterIntent: afterIntent, entered: make(chan struct{}), release: make(chan struct{})}
			m.RemoteFactory = func(Connection) (Remote, error) { return r, nil }
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var release sync.Once
			defer release.Do(func() { close(r.release) })
			initialized := make(chan error, 1)
			go func() { _, err := m.ensureScaleSet(ctx, pool, r); initialized <- err }()
			select {
			case <-r.entered:
			case <-ctx.Done():
				t.Fatal("initialization did not reach the controlled boundary")
			}
			c.Pools[0].MaxRunners++
			if err := m.accept(c, false); err != nil {
				t.Fatal(err)
			}
			retired := make(chan struct{})
			go func() { m.retirePool(ctx, pool); close(retired) }()
			select {
			case <-retired:
				t.Fatal("retirement overtook initialization's remote outcome")
			case <-time.After(50 * time.Millisecond):
			}
			release.Do(func() { close(r.release) })
			err := <-initialized
			if afterIntent && err != nil {
				t.Fatal(err)
			}
			if !afterIntent {
				requireCode(t, err, ErrRetry)
			}
			select {
			case <-retired:
			case <-ctx.Done():
				t.Fatal("retirement did not finish after initialization")
			}
			p := m.Store.View().Pools[pool]
			r.mu.Lock()
			created, deleted := r.created, r.deleted
			r.mu.Unlock()
			if p.Phase != Retired || p.CreatePending || (afterIntent && (created != 1 || deleted != 17)) || (!afterIntent && (created != 0 || deleted != 0)) {
				t.Fatal("reload orphaned a remote scale set or accepted a new creation")
			}
			// A late error from the old loop cannot resurrect a retired pool.
			m.poolProblem(pool, problem(ErrAuth, "Revoked.", "Replace credential."), true)
			if m.Store.View().Pools[pool].Phase != Retired {
				t.Fatal("late initialization failure resurrected the retired generation")
			}
			if p, err = m.ensureScaleSet(ctx, pool, r); err != nil || p != nil {
				t.Fatal("retired pool reentered initialization", err)
			}
		})
	}
}

type recoveringRemote struct {
	*fakeRemote
	found   int
	failure error
	deleted int
}

func (r *recoveringRemote) Ensure(_ context.Context, p PoolState, _ func() error) (int, error) {
	if p.Phase != Draining || !p.CreatePending {
		return 0, errors.New("recovery lost the creation journal")
	}
	return r.found, r.failure
}
func (r *recoveringRemote) DeletePool(_ context.Context, p PoolState) error {
	r.deleted = p.ScaleSetID
	return nil
}

func TestRetirementResolvesPendingCreationBeforeReleasingIdentity(t *testing.T) {
	for _, outcome := range []string{"never initialized", "absent", "owned", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			m, _, _, _, pool := testManager(t)
			if err := m.Store.Update(func(s *Snapshot) error {
				p := s.Pools[pool]
				p.Phase, p.ScaleSetID, p.CreatePending = Draining, 0, outcome != "never initialized"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			r := &recoveringRemote{fakeRemote: &fakeRemote{}}
			if outcome == "owned" {
				r.found = 17
			}
			if outcome == "unavailable" {
				r.failure = problem(ErrRetry, "Unavailable.", "Retry later.")
			}
			m.RemoteFactory = func(Connection) (Remote, error) { return r, nil }
			m.retirePool(context.Background(), pool)
			p := m.Store.View().Pools[pool]
			if outcome == "unavailable" {
				if p.Phase != Draining || !p.CreatePending || p.Problem == nil {
					t.Fatal("ambiguous creation was forgotten")
				}
			} else if p.Phase != Retired || p.CreatePending || r.deleted != r.found {
				t.Fatal("pending creation was not resolved before retirement")
			}
		})
	}
}

func TestRetirementReleasesCachesAndRejectsStaleGenerations(t *testing.T) {
	m, c, _, _, pool := testManager(t)
	for i := 0; i < 12; i++ {
		old := *m.Store.View().Pools[pool]
		if _, err := m.remote(old); err != nil {
			t.Fatal(err)
		}
		if m.poolLock(pool) == nil {
			t.Fatal("active pool has no serialization boundary")
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.poolLoops[pool] = cancel
		c.Pools[0].MaxRunners++
		if err := m.accept(c, false); err != nil {
			t.Fatal(err)
		}
		m.retirePool(context.Background(), pool)
		if ctx.Err() == nil {
			t.Fatal("retirement left its session loop alive")
		}
		delete(m.poolLoops, pool) // Simulate the canceled loop's deferred exit.
		if p := m.Store.View().Pools[pool]; p.Phase != Retired || p.Session != "" {
			t.Fatal("retirement did not publish the terminal state")
		}
		for _, prune := range []bool{false, true} {
			if prune {
				if err := m.Store.Prune(time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.remote(old); err == nil || m.poolLock(pool) != nil {
				t.Fatal("stale generation recreated cached credentials or a mutex")
			}
			if len(m.remotes) != 0 || len(m.poolLocks) != 0 {
				t.Fatal("retired pool caches accumulated across reloads")
			}
		}
		for id, p := range m.Store.View().Pools {
			if p.Phase == Ready {
				pool = id
			}
		}
	}
}

type retiringRemote struct {
	*fakeRemote
	delete func()
}

func (r *retiringRemote) DeletePool(context.Context, PoolState) error {
	r.delete()
	return nil
}

func TestRetirementKeepsCachesWhenDurableCommitFails(t *testing.T) {
	m, _, remote, _, pool := testManager(t)
	m.RemoteFactory = func(Connection) (Remote, error) {
		return &retiringRemote{fakeRemote: remote, delete: func() { _ = m.Store.Close() }}, nil
	}
	if err := m.Store.Update(func(s *Snapshot) error { s.Pools[pool].Phase = Draining; return nil }); err != nil {
		t.Fatal(err)
	}
	lock := m.poolLock(pool)
	m.retirePool(context.Background(), pool)
	if m.Store.View().Pools[pool].Phase != Draining || m.remotes[pool] == nil || m.poolLocks[pool] != lock {
		t.Fatal("failed durable retirement discarded retry state")
	}
}

type lateSessionRemote struct {
	*fakeRemote
	entered, release, closed chan struct{}
}

func (r *lateSessionRemote) Session(context.Context, PoolState, string) (RemoteSession, error) {
	close(r.entered)
	<-r.release
	return &closingSession{fakeSession: r.session, closed: r.closed}, nil
}

type closingSession struct {
	*fakeSession
	closed chan struct{}
}

func (s *closingSession) Close(context.Context) error { close(s.closed); return nil }

func TestLateSessionAfterRetirementAndPruningIsClosed(t *testing.T) {
	m, c, remote, _, pool := testManager(t)
	r := &lateSessionRemote{fakeRemote: remote, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	m.RemoteFactory = func(Connection) (Remote, error) { return r, nil }
	var release sync.Once
	defer release.Do(func() { close(r.release) })
	m.ensurePoolLoop(pool)
	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("session creation did not start")
	}
	c.Pools[0].MaxRunners++
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	m.retirePool(context.Background(), pool)
	if err := m.Store.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(r.release) })
	select {
	case <-r.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("late session was not closed")
	}
	m.wg.Wait()
	if len(m.remotes) != 0 || len(m.poolLocks) != 0 || len(m.poolLoops) != 0 || m.Store.View().Pools[pool] != nil {
		t.Fatal("late session resurrected a retired generation")
	}
}
