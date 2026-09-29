package userservice

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type fixtureBackend struct {
	inspectErr                error
	mu                        sync.Mutex
	m                         *Manager
	present, enabled, foreign bool
	pid                       int
	writes                    map[Action]int
	startErr                  error
	runErr                    error
	stopGate                  chan struct{}
	done                      chan error
	cancel                    context.CancelFunc
}

func (f *fixtureBackend) Inspect(context.Context, Spec) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.foreign {
		return Observation{}, failure()
	}
	if f.inspectErr != nil {
		return Observation{}, f.inspectErr
	}
	return Observation{Present: f.present, Enabled: f.enabled, PID: f.pid}, nil
}
func (f *fixtureBackend) Install(context.Context, Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[Install]++
	f.present = true
	f.enabled = false
	return nil
}
func (f *fixtureBackend) Enable(context.Context, Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[Start]++
	if f.startErr != nil {
		return f.startErr
	}
	f.enabled = true
	return nil
}
func (f *fixtureBackend) Start(_ context.Context, s Spec) error {
	f.mu.Lock()
	if f.pid != 0 {
		f.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	done := f.done
	f.mu.Unlock()
	go func() {
		err := f.m.Run(ctx, s.ID, func(child context.Context, _ Spec, _ bool) error {
			lock, err := security.TryLock(filepath.Join(f.m.Root, string(f.m.Kind)+".lock"))
			if err != nil {
				return err
			}
			defer lock.Close()
			f.mu.Lock()
			f.pid = os.Getpid()
			gate := f.stopGate
			runErr := f.runErr
			f.mu.Unlock()
			<-child.Done()
			if gate != nil {
				<-gate
			}
			return runErr
		})
		f.mu.Lock()
		f.pid = 0
		f.mu.Unlock()
		if err != nil {
			f.m.Logger.Warn("fixture controller failed", "code", domain.SafeError(err).Code)
		}
		done <- err
	}()
	return nil
}
func (f *fixtureBackend) Disable(context.Context, Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[Stop]++
	f.enabled = false
	return nil
}
func (f *fixtureBackend) Remove(context.Context, Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[Remove]++
	f.present = false
	return nil
}
func fixture(t *testing.T) (*Manager, *fixtureBackend) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	m := New(root, Worker, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	f := &fixtureBackend{m: m, writes: map[Action]int{}}
	m.Backend = f
	t.Cleanup(func() {
		f.mu.Lock()
		cancel := f.cancel
		gate := f.stopGate
		f.stopGate = nil
		done := f.done
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		if cancel != nil {
			cancel()
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("fixture controller did not join")
			}
		}
	})
	return m, f
}
func control(t *testing.T, m *Manager, action Action, revision uint64) Result {
	t.Helper()
	r, e := m.Control(context.Background(), action, domain.NewID(), revision, "fixture-user")
	if e != nil {
		t.Fatalf("%s: %+v %v", action, r, e)
	}
	return r
}
func waitStopped(t *testing.T, m *Manager) Status {
	t.Helper()
	s, e := m.WaitStopped(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestRepeatedLifecycleAndReceiptReplayPreserveNewStart(t *testing.T) {
	m, f := fixture(t)
	first := control(t, m, Install, 0)
	if first.Status.State != Stopped || first.Status.LoginEnabled {
		t.Fatalf("install started service: %+v", first)
	}
	duplicate := control(t, m, Install, 1)
	if duplicate.Status.ID != first.Status.ID || f.writes[Install] != 1 {
		t.Fatal("repeat install replaced registration")
	}
	started := control(t, m, Start, 2)
	if started.Status.State != Running || !started.Status.LoginEnabled {
		t.Fatal(started)
	}
	originalStop := domain.NewID()
	stopped, e := m.Control(context.Background(), Stop, originalStop, 3, "fixture-user")
	if e != nil {
		t.Fatal(e)
	}
	if stopped.Status.State == Stopped && !stopped.Status.CleanupConfirmed {
		t.Fatal("uncertain cleanup reported stopped")
	}
	waitStopped(t, m)
	restarted := control(t, m, Start, 4)
	replay, e := m.Control(context.Background(), Stop, originalStop, 3, "fixture-user")
	if e != nil || !replay.Replayed || replay.Status.State != Running || replay.Status.Revision != restarted.Status.Revision {
		t.Fatalf("old Stop canceled replacement: %+v %v", replay, e)
	}
	if _, e = m.Control(context.Background(), Remove, originalStop, 3, "fixture-user"); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("changed receipt accepted")
	}
	control(t, m, Stop, 5)
	waitStopped(t, m)
	removed := control(t, m, Remove, 6)
	if removed.Status.State != Absent {
		t.Fatal(removed)
	}
	repeated := control(t, m, Remove, 7)
	if repeated.Status.State != Absent || f.writes[Remove] != 1 {
		t.Fatal("repeat removal changed native state")
	}
	if _, e := m.Control(context.Background(), Start, domain.NewID(), 8, "fixture-user"); domain.SafeError(e).Code != domain.NotFound {
		t.Fatal("removed registration accepted Start", e)
	}
	reinstalled := control(t, m, Install, 8)
	if reinstalled.Status.ID == first.Status.ID || reinstalled.Status.State != Stopped {
		t.Fatal("reinstall reused old runtime authority")
	}
	r, e := m.load()
	if e != nil || len(r.Events) != 9 || len(r.Receipts) != 9 {
		t.Fatal("history lost", e)
	}
}
func TestStopBlocksDelayedLoginAndWaitsForJoinedCleanup(t *testing.T) {
	m, f := fixture(t)
	install := control(t, m, Install, 0)
	f.stopGate = make(chan struct{})
	control(t, m, Start, 1)
	control(t, m, Stop, 2)
	status, e := m.Status(context.Background())
	if e != nil || status.State != Stopping || status.CleanupConfirmed || status.LoginEnabled {
		t.Fatalf("Stop claimed premature cleanup: %+v %v", status, e)
	}
	if _, e = m.Control(context.Background(), Remove, domain.NewID(), 3, "fixture-user"); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("removed active registration")
	}
	f.mu.Lock()
	gate := f.stopGate
	f.mu.Unlock()
	close(gate)
	waitStopped(t, m)
	invoked := false
	if e = m.Run(context.Background(), install.Status.ID, func(context.Context, Spec, bool) error { invoked = true; return nil }); e == nil || invoked {
		t.Fatal("relogin bypassed durable Stop")
	}
}
func TestForeignNativeDefinitionAndPIDAreUntouched(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	control(t, m, Start, 1)
	f.mu.Lock()
	f.foreign = true
	f.mu.Unlock()
	if _, e := m.Control(context.Background(), Stop, domain.NewID(), 2, "fixture-user"); e == nil {
		t.Fatal("foreign definition controlled")
	}
	f.mu.Lock()
	f.foreign = false
	originalPID := f.pid
	f.pid = originalPID + 100000
	f.mu.Unlock()
	if _, e := m.Control(context.Background(), Stop, domain.NewID(), 2, "fixture-user"); e == nil {
		t.Fatal("foreign PID controlled")
	}
	f.mu.Lock()
	f.pid = originalPID
	writes := f.writes[Stop]
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("foreign service mutated")
	}
	r, e := m.load()
	if e != nil || r.Revision != 2 || r.Desired != Running {
		t.Fatal("foreign state changed")
	}
	control(t, m, Stop, 2)
	waitStopped(t, m)
}
func TestUnconfirmedNativeWriteIsNotReplayed(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	f.startErr = errors.New("private native output must stay redacted")
	id := domain.NewID()
	r, e := m.Control(context.Background(), Start, id, 1, "fixture-user")
	if e == nil || r.Status.State != Uncertain {
		t.Fatal("native failure claimed success")
	}
	count := f.writes[Start]
	r, e = m.Control(context.Background(), Start, id, 1, "fixture-user")
	if e == nil || !r.Replayed || f.writes[Start] != count {
		t.Fatal("uncertain native write repeated")
	}
	if _, e = m.Control(context.Background(), Start, id, 1, "other-user"); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("receipt actor changed")
	}
	// A separate explicit Stop can suppress an accepted Start whose enable failed.
	control(t, m, Stop, 2)
	waitStopped(t, m)
}
func TestForegroundAndServiceShareExclusivity(t *testing.T) {
	m, f := fixture(t)
	lock, e := security.TryLock(filepath.Join(m.Root, "worker.lock"))
	if e != nil {
		t.Fatal(e)
	}
	control(t, m, Install, 0)
	if _, e = m.Control(context.Background(), Start, domain.NewID(), 1, "fixture-user"); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("competing service startup accepted")
	}
	if f.writes[Start] != 0 {
		t.Fatal("native Start attempted against foreground owner")
	}
	if e = lock.Close(); e != nil {
		t.Fatal(e)
	}
	control(t, m, Start, 1)
	if lock, e = security.TryLock(filepath.Join(m.Root, "worker.lock")); e == nil {
		lock.Close()
		t.Fatal("foreground stole service owner")
	}
	control(t, m, Stop, 2)
	waitStopped(t, m)
}
func TestChangedExecutableAndScopeFailBeforeNativeControl(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	r, e := m.load()
	if e != nil {
		t.Fatal(e)
	}
	executable := filepath.Join(t.TempDir(), "installed")
	if e = os.WriteFile(executable, []byte("original executable"), 0700); e != nil {
		t.Fatal(e)
	}
	identity, hash, e := binaryIdentity(executable)
	if e != nil {
		t.Fatal(e)
	}
	r.Spec.Binary = executable
	r.Spec.BinaryIdentity = identity
	r.Spec.BinaryDigest = hash
	if e = m.save(r); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(executable, []byte("replacement"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Control(context.Background(), Stop, domain.NewID(), 1, "fixture-user"); e == nil {
		t.Fatal("replaced executable accepted")
	}
	if f.writes[Stop] != 0 {
		t.Fatal("foreign executable controlled")
	}
	root := m.Root
	if e = os.Rename(root, root+"-original"); e != nil {
		t.Fatal(e)
	}
	if e = security.PrivateDir(root); e != nil {
		t.Fatal(e)
	}
	raw, e := security.ReadPrivate(filepath.Join(root+"-original", "user-service-worker.json"), 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e = security.WriteAtomic(m.path(".json"), raw); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Control(context.Background(), Remove, domain.NewID(), 1, "fixture-user"); e == nil || f.writes[Remove] != 0 {
		t.Fatal("replacement scope controlled")
	}
}
func TestControllerFailureRetainsCleanupUncertainty(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	f.runErr = failure()
	control(t, m, Start, 1)
	control(t, m, Stop, 2)
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		pid := f.pid
		f.mu.Unlock()
		if pid == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controller did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, e := m.Status(context.Background())
	if e != nil || s.State != Uncertain || s.CleanupConfirmed {
		t.Fatal("missing cleanup inferred from PID absence", s, e)
	}
	if _, e = m.Control(context.Background(), Remove, domain.NewID(), 3, "fixture-user"); e == nil || f.writes[Remove] != 0 {
		t.Fatal("uncertain controller registration removed")
	}
}
func TestAuthorizationAndCancellationPrecedeNativeWrites(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	m.Authorize = func(context.Context) error { return domain.Fail(domain.Unauthenticated, "Revoked.", "Pair again.") }
	if _, e := m.Control(context.Background(), Start, domain.NewID(), 1, "fixture-user"); domain.SafeError(e).Code != domain.Unauthenticated || f.writes[Start] != 0 {
		t.Fatal("revoked actor controlled service")
	}
	m.Authorize = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Control(ctx, Start, domain.NewID(), 1, "fixture-user"); e == nil || f.writes[Start] != 0 {
		t.Fatal("canceled service action started")
	}
}

func TestMissingNativeSessionRetainsStopWithoutClaimingCleanup(t *testing.T) {
	m, f := fixture(t)
	control(t, m, Install, 0)
	f.mu.Lock()
	f.inspectErr = unavailable()
	f.mu.Unlock()
	result, err := m.Control(context.Background(), Stop, domain.NewID(), 1, "fixture-user")
	if domain.SafeError(err).Code != domain.Unavailable || result.Status.State != Uncertain || result.Status.Desired != Stopped {
		t.Fatal("missing session claimed Stop", result, err)
	}
	r, err := m.load()
	if err != nil || r.Desired != Stopped || r.Revision != 2 || r.Receipts[1].Done {
		t.Fatal("offline suppression missing", err)
	}
	f.mu.Lock()
	f.inspectErr = nil
	writes := f.writes[Stop]
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("unverified native session controlled")
	}
	control(t, m, Stop, 2)
	waitStopped(t, m)
}

func TestManagedIntentRetainsNativeSupervisorOwnership(t *testing.T) {
	m, _ := fixture(t)
	if managed, _, err := ManagedIntent(m.Root, m.Kind); err != nil || managed {
		t.Fatal("uninstalled scope claimed supervision")
	}
	control(t, m, Install, 0)
	if managed, stopped, err := ManagedIntent(m.Root, m.Kind); err != nil || !managed || !stopped {
		t.Fatal("stopped registration lost suppression")
	}
	control(t, m, Start, 1)
	if managed, stopped, err := ManagedIntent(m.Root, m.Kind); err != nil || !managed || stopped {
		t.Fatal("running native supervisor lost exclusivity")
	}
	control(t, m, Stop, 2)
	waitStopped(t, m)
	control(t, m, Remove, 3)
	if managed, _, err := ManagedIntent(m.Root, m.Kind); err != nil || managed {
		t.Fatal("removed registration retained supervisor ownership")
	}
}
