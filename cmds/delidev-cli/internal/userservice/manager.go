package userservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func (m *Manager) authorize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if m.Authorize != nil {
		return m.Authorize(ctx)
	}
	return nil
}
func (m *Manager) runtime(id domain.ID) (runtimeRecord, error) {
	var r runtimeRecord
	b, e := security.ReadPrivate(m.path("-runtime-"+string(id)+".json"), 4096)
	if errors.Is(e, os.ErrNotExist) {
		return r, nil
	}
	if e != nil || domain.Decode(b, &r) != nil || r.ID.Validate() != nil || r.Instance.Validate() != nil || r.StartID.Validate() != nil || r.PID <= 0 || r.Birth == "" {
		return r, failure()
	}
	return r, nil
}
func (m *Manager) sharedIdle() bool {
	// These are the same locks used by foreground, detached and service processes.
	name := "server.lock"
	if m.Kind == Worker {
		name = "worker.lock"
	}
	lock, err := security.TryLock(filepath.Join(m.Root, name))
	if err != nil {
		return false
	}
	return lock.Close() == nil
}
func (m *Manager) observe(ctx context.Context, r record) (Status, error) {
	status := Status{Kind: m.Kind, Revision: r.Revision, State: Absent, Desired: Stopped, CleanupConfirmed: true}
	if r.Spec.Version == 0 {
		return status, nil
	}
	status.ID = r.Spec.ID
	status.Desired = r.Desired
	if r.Removed {
		identity, e := fileIdentity(m.Root)
		if e != nil || identity != r.Spec.RootIdentity {
			return status, failure()
		}
	} else if err := m.validate(r.Spec); err != nil {
		status.State = Uncertain
		status.CleanupConfirmed = false
		return status, err
	}
	observation, err := m.Backend.Inspect(ctx, r.Spec)
	if err != nil {
		status.State = Uncertain
		status.CleanupConfirmed = false
		return status, err
	}
	if r.Removed {
		if observation.Present {
			status.State = Uncertain
			status.CleanupConfirmed = false
			return status, failure()
		}
		return status, nil
	}
	if !observation.Present {
		status.State = Uncertain
		status.CleanupConfirmed = false
		return status, failure()
	}
	status.LoginEnabled = observation.Enabled
	running, err := m.runtime(r.Spec.ID)
	if err != nil || (running.ID != "" && running.ID != r.Spec.ID) {
		status.State = Uncertain
		status.CleanupConfirmed = false
		return status, failure()
	}
	lock, lockErr := security.TryLock(m.path("-runtime.lock"))
	if lock != nil {
		lock.Close()
	}
	if lockErr != nil && domain.SafeError(lockErr).Code != domain.Conflict {
		return status, failure()
	}
	active := lockErr != nil
	if observation.PID != 0 {
		// A native-manager PID is not authority by itself. Match the independently
		// published launch identity and query its executable, user and exact birth.
		if running.PID == observation.PID && running.Complete && !active && processAbsent(running.PID) && r.Desired == Stopped {
			// The controller's positive completion can precede the manager clearing its
			// cached PID. Preserve this publication barrier as stopping, without control
			// or cleanup authority; never infer completion for a reused/unknown PID.
			status.State = Stopping
			status.CleanupConfirmed = false
			return status, nil
		}
		if running.PID != observation.PID || verifyProcess(r.Spec, running) != nil {
			status.State = Uncertain
			status.CleanupConfirmed = false
			return status, failure()
		}
	}
	status.CleanupConfirmed = !active && observation.PID == 0 && (running.ID == "" || (running.Complete && m.sharedIdle()))
	switch {
	case r.Desired == Stopped && status.CleanupConfirmed && !observation.Enabled:
		status.State = Stopped
	case r.Desired == Stopped && (active || (running.Complete && observation.PID != 0)):
		status.State = Stopping
	case r.Desired == Stopped:
		status.State = Uncertain
	case active && observation.PID != 0:
		status.State = Running
	case active || running.ID == "":
		status.State = Starting
	default:
		status.State = Uncertain
	}
	return status, nil
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.check(); err != nil {
		return Status{}, err
	}
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	lock, err := m.stateLock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	r, err := m.load()
	if err != nil {
		return Status{Kind: m.Kind, State: Uncertain}, err
	}
	return m.observe(ctx, r)
}

// Control first atomically records intent, a metadata event and a once-only
// receipt claim. An interrupted native write is never blindly replayed. Stop is
// accepted independently of cleanup; WaitStopped is the local confirmation path.
func (m *Manager) Control(ctx context.Context, action Action, id domain.ID, revision uint64, actor string) (Result, error) {
	result := Result{RequestID: id}
	if err := m.check(); err != nil {
		return result, err
	}
	if !action.Valid() || id.Validate() != nil || actor == "" {
		return result, domain.Fail(domain.InvalidArgument, "A valid service action and request identity are required.", "Use install, start, stop or remove with a UUID-v7 request ID.")
	}
	if err := m.authorize(ctx); err != nil {
		return result, err
	}
	control, err := security.TryLock(m.path("-control.lock"))
	if err != nil {
		return result, err
	}
	defer control.Close()
	gate, err := m.stateLock(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		if gate != nil {
			gate.Close()
		}
	}()
	r, err := m.load()
	if err != nil {
		return result, err
	}
	input := digest(struct {
		Action   Action
		Revision uint64
		Actor    string
		Options  ServerOptions
	}{action, revision, actor, m.Options})
	for _, receipt := range r.Receipts {
		if receipt.ID != id {
			continue
		}
		if receipt.Digest != input {
			return result, conflict()
		}
		result.Replayed = true
		result.Status, err = m.observe(ctx, r)
		if !receipt.Done {
			return result, failure()
		}
		return result, err
	}
	if r.Revision != revision {
		return result, conflict()
	}
	if len(r.Receipts) >= 1024 {
		return result, domain.Fail(domain.ResourceExhausted, "The retained service receipt limit has been reached.", "Preserve original service evidence; do not discard uncertain requests.")
	}
	var before Status
	if r.Spec.Version != 0 {
		before, err = m.observe(ctx, r)
		if err != nil && !(action == Stop && domain.SafeError(err).Code == domain.Unavailable && m.validate(r.Spec) == nil) {
			return result, err
		}
		// A missing native session cannot authorize native control, but a positively
		// owned private record can still suppress its original delayed launches.
		err = nil
	} else if action != Install {
		return result, domain.Fail(domain.NotFound, "This scope has no installed user service.", "Install it explicitly before controlling it.")
	}
	if r.Removed && action != Install && action != Remove {
		return result, domain.Fail(domain.NotFound, "This user service registration has been removed.", "Install it explicitly before starting or stopping it.")
	}
	if action == Remove && !before.CleanupConfirmed {
		return result, domain.Fail(domain.Conflict, "Service cleanup is still pending.", "Stop the service and confirm its original controller exit before removing registration.")
	}
	if action == Install && (r.Spec.Version == 0 || r.Removed) {
		s, e := newSpec(m.Root, m.Kind, m.Options)
		if e != nil {
			return result, e
		}
		observation, e := m.Backend.Inspect(ctx, s)
		if e != nil {
			return result, e
		}
		if observation.Present {
			return result, failure()
		}
		// Reinstallation retains all receipts; old native claims remain historical.
		r.Spec = s
		r.Removed = false
		r.Desired = Stopped
	}
	if action == Start && before.State != Running && !m.sharedIdle() {
		return result, conflict()
	}
	if action == Start && before.State != Running && before.State != Starting && !before.CleanupConfirmed {
		return result, failure()
	}
	if action == Start {
		r.Desired = Running
	}
	if action == Stop || action == Remove {
		r.Desired = Stopped
	}
	r.Revision++
	r.Receipts = append(r.Receipts, Receipt{ID: id, Digest: input})
	r.Events = append(r.Events, Event{Revision: r.Revision, RequestID: id, Action: action, Desired: r.Desired})
	// Native observation and lock admission can outlive client revocation. Check
	// current authority again before intent can cancel an already running owner.
	if err = m.authorize(ctx); err != nil {
		return result, err
	}
	if err = m.save(r); err != nil {
		return result, err
	}
	if err = gate.Close(); err != nil {
		return result, failure()
	}
	gate = nil
	m.Logger.InfoContext(ctx, "user_service_intent_recorded", "kind", m.Kind, "action", action, "request_id", id, "revision", r.Revision)
	// Revalidate immediately before every mutation. A replaced definition,
	// executable, scope or native process never becomes signal/control authority.
	mutate := func(f func(context.Context, Spec) error, installing bool) error {
		if err := m.authorize(ctx); err != nil {
			return err
		}
		if err := m.validate(r.Spec); err != nil {
			return err
		}
		if !installing {
			if _, err := m.observe(ctx, r); err != nil {
				return err
			}
		}
		return f(ctx, r.Spec)
	}
	switch action {
	case Install:
		if before.ID == "" || before.State == Absent {
			err = mutate(m.Backend.Install, true)
		}
	case Start:
		if err = mutate(m.Backend.Enable, false); err == nil {
			err = mutate(m.Backend.Start, false)
		}
	case Stop:
		err = mutate(m.Backend.Disable, false)
	case Remove:
		if !r.Removed {
			if err = mutate(m.Backend.Disable, false); err == nil {
				err = mutate(m.Backend.Remove, false)
			}
		}
	}
	if err != nil {
		// Completion of a failed attempt is retained separately from a successful
		// native outcome. Original receipt replay still cannot repeat the write.
		if failedGate, e := m.stateLock(context.WithoutCancel(ctx)); e == nil {
			r.Receipts[len(r.Receipts)-1].Settled = true
			_ = m.save(r)
			failedGate.Close()
		}
		m.Logger.WarnContext(ctx, "user_service_control_unconfirmed", "kind", m.Kind, "action", action, "request_id", id, "code", domain.SafeError(err).Code)
		result.Status = Status{Kind: m.Kind, ID: r.Spec.ID, Revision: r.Revision, State: Uncertain, Desired: r.Desired}
		return result, err
	}
	gate, err = m.stateLock(ctx)
	if err != nil {
		return result, err
	}
	// Runner writes only the independent runtime journal. No concurrent service
	// command can change this record while the control lock is held.
	r.Receipts[len(r.Receipts)-1].Done = true
	r.Receipts[len(r.Receipts)-1].Settled = true
	if action == Remove {
		r.Removed = true
	}
	if err = m.save(r); err != nil {
		return result, err
	}
	if err = gate.Close(); err != nil {
		return result, failure()
	}
	gate = nil
	if action == Start {
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			result.Status, err = m.observe(ctx, r)
			if err == nil && result.Status.State == Running {
				break
			}
			select {
			case <-ctx.Done():
				return result, domain.SafeError(ctx.Err())
			case <-deadline.C:
				return result, failure()
			case <-ticker.C:
			}
		}
	} else if action == Stop {
		// Return bounded acceptance immediately after suppression/disable commit so
		// a server can flush its own Stop response before the observer cancels it.
		result.Status = Status{Kind: m.Kind, ID: r.Spec.ID, Revision: r.Revision, Desired: Stopped, State: Stopping}
		if before.CleanupConfirmed {
			result.Status.State = Stopped
			result.Status.CleanupConfirmed = true
		}
	} else {
		result.Status, err = m.observe(ctx, r)
	}
	return result, err
}
func (m *Manager) WaitStopped(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := m.Status(ctx)
		if err != nil {
			return status, err
		}
		if status.State == Stopped && status.CleanupConfirmed && !status.LoginEnabled {
			return status, nil
		}
		if status.State == Uncertain {
			return status, failure()
		}
		select {
		case <-ctx.Done():
			return status, failure()
		case <-ticker.C:
		}
	}
}
func (m *Manager) admitted(id domain.ID) (record, error) {
	r, err := m.load()
	if err != nil || r.Removed || r.Spec.ID != id || r.Desired != Running {
		return r, conflict()
	}
	return r, m.validate(r.Spec)
}

// Run is invoked only by the registered installed executable. The private lock
// excludes another service controller, while run holds the product's existing
// foreground lock. Native relaunches cannot reset durable Stop intent.
func (m *Manager) Run(ctx context.Context, id domain.ID, run func(context.Context, Spec, bool) error) (resultErr error) {
	stage := "admission"
	defer func() {
		if resultErr != nil {
			m.Logger.WarnContext(ctx, "user_service_controller_unconfirmed", "kind", m.Kind, "service_id", id, "stage", stage, "code", domain.SafeError(resultErr).Code)
		}
	}()
	if err := m.check(); err != nil {
		return err
	}
	gate, err := m.stateLock(ctx)
	if err != nil {
		return err
	}
	r, err := m.admitted(id)
	if err != nil {
		gate.Close()
		return err
	}
	runtimeLock, err := security.TryLock(m.path("-runtime.lock"))
	if err != nil {
		gate.Close()
		return err
	}
	defer runtimeLock.Close()
	prior, err := m.runtime(id)
	if err != nil || (prior.ID != "" && !prior.Complete) {
		gate.Close()
		return failure()
	}
	stage = "process_identity"
	p, err := process.ProcessIdentity(os.Getpid())
	if err != nil {
		gate.Close()
		return failure()
	}
	startID := domain.ID("")
	for _, e := range r.Events {
		if e.Action == Start {
			startID = e.RequestID
		}
	}
	if startID == "" {
		gate.Close()
		return conflict()
	}
	live := runtimeRecord{StartID: startID, ID: id, Instance: domain.NewID(), PID: p.PID, Birth: p.Birth}
	if err := verifyProcess(r.Spec, live); err != nil {
		gate.Close()
		return err
	}
	if err := m.saveRuntime(live); err != nil {
		gate.Close()
		return err
	}
	gate.Close()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		var stopSeen time.Time
		defer ticker.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-ticker.C:
				// Intent is an atomic immutable-file replacement, so observing it requires
				// no control lock and cannot deadlock a Stop waiting for controller exit.
				current, e := m.load()
				if e != nil || current.Spec.ID != id || current.Removed {
					cancel()
					return
				}
				if current.Desired != Running {
					if stopSeen.IsZero() {
						stopSeen = time.Now()
					}
					last := current.Receipts[len(current.Receipts)-1]
					// Durable suppression already rejects every replacement launch. Allow
					// the owning bounded native-disable attempt to finish before canceling
					// this server's RPC context; interrupted attempts get a fixed grace.
					if last.Settled || time.Since(stopSeen) >= 30*time.Second {
						cancel()
						return
					}
				}
			}
		}
	}()
	stage = "product_controller"
	err = run(child, r.Spec, prior.StartID != startID)
	cancel()
	<-done
	// A failed product controller may retain descendant uncertainty. Preserve the
	// claim instead of treating process exit or a free lock as joined cleanup.
	if err != nil {
		return err
	}
	stage = "cleanup_publication"
	live.Complete = true
	if m.saveRuntime(live) != nil {
		return failure()
	}
	return nil
}
func (m *Manager) saveRuntime(r runtimeRecord) error {
	b, _ := json.Marshal(r)
	if security.WriteAtomic(m.path("-runtime-"+string(r.ID)+".json"), b) != nil {
		return failure()
	}
	return nil
}

func (m *Manager) stateLock(ctx context.Context) (*security.Lock, error) {
	child, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := security.TryLock(m.path("-state.lock"))
		if err == nil || domain.SafeError(err).Code != domain.Conflict {
			return lock, err
		}
		select {
		case <-child.Done():
			return nil, domain.SafeError(child.Err())
		case <-ticker.C:
		}
	}
}

// ManagedIntent is a read-only guard for other automatic controllers. A native
// registration owns automatic restart; an ordinary ensure may reuse its live
// authenticated server but must never spawn a competing detached supervisor.
func ManagedIntent(root string, kind Kind) (installed, stopped bool, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return false, false, domain.SafeError(err)
	}
	if err = security.CheckPrivateDir(root); err != nil {
		return false, false, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return false, false, domain.SafeError(err)
	}
	m := New(root, kind, nil)
	r, err := m.load()
	if err != nil {
		return false, false, err
	}
	if r.Spec.Version == 0 {
		return false, false, nil
	}
	identity, err := fileIdentity(root)
	if err != nil || identity != r.Spec.RootIdentity {
		return false, false, failure()
	}
	return !r.Removed, r.Desired == Stopped, nil
}
