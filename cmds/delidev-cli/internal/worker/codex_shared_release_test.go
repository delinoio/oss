package worker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type releaseStart int

const (
	releaseVerifies releaseStart = iota
	releaseUnverifiable
)

type finishOutcome int

const (
	finishSucceeds finishOutcome = iota
	finishFails
	finishAmbiguous
)

type ownerFixture struct {
	config  Config
	machine domain.ID
	account domain.ID
	home    string
	gate    *codexAccountGate
	owner   *codexSharedOwner
	last    *codexSharedMember
	moved   string
}

func newOwnerFixture(t *testing.T, release releaseStart) *ownerFixture {
	t.Helper()
	config, machine := profileTestConfig(t)
	config.codexOwners = newCodexOwnerRegistry()
	account := domain.NewID()
	home, _ := seedSharedCodexHistory(t, config, account)
	hold, err := holdSharedCodexHome(context.Background(), config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	gate := config.codexOwners.gate(account)
	lease := &managedSubscriptionLease{account: account, response: &pb.TakeSubscriptionResponse{LeaseId: string(domain.NewID())}}
	last := &codexSharedMember{lease: lease}
	owner := &codexSharedOwner{gate: gate, config: config, lease: lease, profile: hold, clean: true, retiring: true, closed: true, members: map[domain.ID]*codexSharedMember{lease.leaseID(): last}, initial: []byte("initial"), latest: []byte("latest")}
	gate.owner = owner
	f := &ownerFixture{config: config, machine: machine, account: account, home: home, gate: gate, owner: owner, last: last, moved: home + "-moved"}
	if release == releaseUnverifiable {
		// The home is replaced by a link while the lock is held, so the release
		// closes the lock but cannot prove that the home can be locked again.
		if err := os.Rename(home, f.moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.moved, home); err != nil {
			t.Skip("symlinks unavailable")
		}
	}
	return f
}

func (f *ownerFixture) repair(t *testing.T) {
	t.Helper()
	if err := os.Remove(f.home); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.moved, f.home); err != nil {
		t.Fatal(err)
	}
}

func (f *ownerFixture) lockIsFree(t *testing.T) bool {
	t.Helper()
	hold, err := holdSharedCodexHome(context.Background(), f.config, f.machine, f.account)
	if err != nil {
		return false
	}
	if err := hold.release(); err != nil {
		t.Fatal(err)
	}
	return true
}

func (f *ownerFixture) publish(outcome finishOutcome) func() error {
	return func() error {
		switch outcome {
		case finishFails:
			return domain.Fail(domain.Unavailable, "finish failed", "retry")
		case finishAmbiguous:
			return &managedExecutionUncertain{errors.New("finish outcome unknown")}
		}
		return nil
	}
}

// Owner and last-member release timeline. Rows are {release verifies or does
// not} x {Finish succeeds, fails or is ambiguous}. Each row asserts the value
// returned through the member outcome, whether the local owner is retained or
// cleared, which retry state is kept, and the shared home lock state.
func TestOwnerFinishReleaseTimeline(t *testing.T) {
	type expectation struct {
		releaseFailure bool // the member outcome is the shared home release failure
		finishFailure  bool // the member outcome is the Finish error
		ownerCleared   bool
		pendingOwner   bool // the same Finish request is retried by the gate
		parked         bool // the unverified release moved to the account gate
		lockFree       bool // the lock can be taken immediately after the call
	}
	rows := []struct {
		name    string
		release releaseStart
		finish  finishOutcome
		want    expectation
	}{
		{"release verified, Finish succeeds", releaseVerifies, finishSucceeds, expectation{ownerCleared: true, lockFree: true}},
		{"release verified, Finish fails", releaseVerifies, finishFails, expectation{finishFailure: true, lockFree: true}},
		{"release verified, Finish ambiguous", releaseVerifies, finishAmbiguous, expectation{finishFailure: true, pendingOwner: true, lockFree: true}},
		{"release unverified, Finish succeeds", releaseUnverifiable, finishSucceeds, expectation{releaseFailure: true, ownerCleared: true, parked: true}},
		{"release unverified, Finish fails", releaseUnverifiable, finishFails, expectation{finishFailure: true}},
		{"release unverified, Finish ambiguous", releaseUnverifiable, finishAmbiguous, expectation{finishFailure: true, pendingOwner: true}},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			f := newOwnerFixture(t, row.release)
			published := 0
			err := f.owner.finishOwner(func() error { published++; return f.publish(row.finish)() }, f.last)
			if published != 1 {
				t.Fatalf("Finish was published %d times", published)
			}
			var releaseFailure *sharedHomeReleaseError
			gotRelease := errors.As(err, &releaseFailure)
			if gotRelease != row.want.releaseFailure {
				t.Fatalf("release failure returned = %v, want %v (%v)", gotRelease, row.want.releaseFailure, err)
			}
			if row.want.releaseFailure {
				if safe := domain.SafeError(err); safe.Code != domain.RecoveryRequired {
					t.Fatalf("release failure code = %s", safe.Code)
				}
			}
			if gotFinish := err != nil && !gotRelease; gotFinish != row.want.finishFailure {
				t.Fatalf("Finish failure returned = %v, want %v (%v)", gotFinish, row.want.finishFailure, err)
			}
			if err == nil && (row.want.releaseFailure || row.want.finishFailure) {
				t.Fatal("a failure was dropped")
			}
			f.gate.mu.Lock()
			cleared, pending, parked := f.gate.owner == nil, f.owner.pendingOwner != nil, f.gate.unverified != nil
			f.gate.mu.Unlock()
			if cleared != row.want.ownerCleared || pending != row.want.pendingOwner || parked != row.want.parked {
				t.Fatalf("owner cleared=%v pending=%v parked=%v, want %v %v %v", cleared, pending, parked, row.want.ownerCleared, row.want.pendingOwner, row.want.parked)
			}
			if row.want.ownerCleared && (string(f.owner.initial) == "initial" || string(f.owner.latest) == "latest") {
				t.Fatal("a retired owner kept its credential bundles")
			}
			if row.release == releaseVerifies && f.lockIsFree(t) != row.want.lockFree {
				t.Fatalf("lock free = %v, want %v", !row.want.lockFree, row.want.lockFree)
			}
			if row.release == releaseVerifies {
				return
			}
			// The home is still replaced: nothing may take the lock, and the
			// release stays unverified until the home is repaired.
			if f.lockIsFree(t) {
				t.Fatal("a replaced home was locked")
			}
			f.repair(t)
			if row.want.parked {
				// The next holder verifies the retained release before it locks.
				if err := f.config.codexOwners.settleUnverifiedRelease(f.account); err != nil {
					t.Fatalf("the repaired release was not verified: %v", err)
				}
				f.gate.mu.Lock()
				still := f.gate.unverified != nil
				f.gate.mu.Unlock()
				if still {
					t.Fatal("a verified release stayed parked")
				}
			} else {
				// The owner is still tracked, so the same Finish retry verifies the
				// release again and then retires the owner.
				if err := f.owner.finishOwner(f.publish(finishSucceeds), f.last); err != nil {
					t.Fatalf("the retry did not retire the owner: %v", err)
				}
				f.gate.mu.Lock()
				retired := f.gate.owner == nil && f.gate.unverified == nil && f.owner.pendingOwner == nil
				f.gate.mu.Unlock()
				if !retired {
					t.Fatal("the retry left owner state behind")
				}
			}
			if !f.lockIsFree(t) {
				t.Fatal("the lock stayed held after the release was verified")
			}
		})
	}
}

// The next holder is blocked, not surprised, by an unverified release: it
// reports the failure and takes the lock only after the release is verified.
func TestHoldSharedCodexHomeWaitsForAnUnverifiedRelease(t *testing.T) {
	f := newOwnerFixture(t, releaseUnverifiable)
	if err := f.owner.finishOwner(f.publish(finishSucceeds), f.last); err == nil {
		t.Fatal("an unverified release was not reported")
	}
	_, err := holdSharedCodexHome(context.Background(), f.config, f.machine, f.account)
	var releaseFailure *sharedHomeReleaseError
	if !errors.As(err, &releaseFailure) {
		t.Fatalf("the next holder was not told about the unverified release: %v", err)
	}
	f.repair(t)
	hold, err := holdSharedCodexHome(context.Background(), f.config, f.machine, f.account)
	if err != nil {
		t.Fatalf("a verified release still blocked the next holder: %v", err)
	}
	if err := hold.release(); err != nil {
		t.Fatal(err)
	}
}

// An exclusive continuation parks its unverified release the same way.
func TestExclusiveContinuationParksAnUnverifiedRelease(t *testing.T) {
	config, machine := profileTestConfig(t)
	config.codexOwners = newCodexOwnerRegistry()
	account := domain.NewID()
	home, _ := seedSharedCodexHistory(t, config, account)
	hold, err := holdSharedCodexHome(context.Background(), config, machine, account)
	if err != nil {
		t.Fatal(err)
	}
	moved := home + "-moved"
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, home); err != nil {
		t.Skip("symlinks unavailable")
	}
	releaseErr, publishErr := publishAfterRelease(hold, func() error { return nil })
	if releaseErr == nil || publishErr != nil {
		t.Fatalf("release=%v publish=%v", releaseErr, publishErr)
	}
	config.codexOwners.parkUnverifiedRelease(account, hold)
	if _, err := holdSharedCodexHome(context.Background(), config, machine, account); err == nil {
		t.Fatal("an unverified release did not block the next holder")
	}
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, home); err != nil {
		t.Fatal(err)
	}
	next, err := holdSharedCodexHome(context.Background(), config, machine, account)
	if err != nil {
		t.Fatalf("a verified release blocked the next holder: %v", err)
	}
	if err := next.release(); err != nil {
		t.Fatal(err)
	}
	// Nil registries and holds are no-ops.
	var none *codexOwnerRegistry
	none.parkUnverifiedRelease(account, hold)
	if err := none.settleUnverifiedRelease(account); err != nil {
		t.Fatal(err)
	}
}

// hookSharedHomeRelease installs release hooks for one test and restores them.
func hookSharedHomeRelease(t *testing.T, closeLease, closeProbe func(*nativeprofile.Lease) error, afterClose func()) {
	t.Helper()
	oldLease, oldProbe, oldAfter := closeSharedHomeLease, closeSharedHomeProbe, afterSharedHomeClose
	if closeLease != nil {
		closeSharedHomeLease = closeLease
	}
	if closeProbe != nil {
		closeSharedHomeProbe = closeProbe
	}
	if afterClose != nil {
		afterSharedHomeClose = afterClose
	}
	t.Cleanup(func() {
		closeSharedHomeLease, closeSharedHomeProbe, afterSharedHomeClose = oldLease, oldProbe, oldAfter
	})
}

// closeThenFail closes the lease for real, so the lock is free, and then
// reports a close error as a failing unlock would.
func closeThenFail(lease *nativeprofile.Lease) error {
	_ = lease.Close()
	return errors.New("injected close failure")
}

// admitContinuation models an exclusive continuation that was already retrying
// its Take: the moment the lock is closed it takes the shared home lock. The
// returned function releases the continuation's hold.
func admitContinuation(t *testing.T, f *ownerFixture) (release func()) {
	t.Helper()
	var admitted *sharedHomeHold
	hookSharedHomeRelease(t, nil, nil, func() {
		// One admission only: the continuation's own release must not admit another.
		afterSharedHomeClose = func() {}
		hold, err := holdSharedCodexHome(context.Background(), f.config, f.machine, f.account)
		if err != nil {
			t.Errorf("the admitted continuation could not take the freed lock: %v", err)
			return
		}
		admitted = hold
	})
	return func() {
		t.Helper()
		if admitted == nil {
			t.Fatal("no continuation was admitted")
		}
		if err := admitted.release(); err != nil {
			t.Fatal(err)
		}
	}
}

// Lock handoff timeline. An exclusive continuation that is already retrying
// takes the lock after the owner closed it and before the verification probe.
// That is a successful handoff, not an unverified release, for every Finish
// outcome: nothing is parked and the retiring member does not fail.
func TestOwnerFinishReleaseHandoffTimeline(t *testing.T) {
	rows := []struct {
		name         string
		finish       finishOutcome
		wantErr      bool
		ownerCleared bool
		pending      bool
	}{
		{"continuation takes the lock, Finish succeeds", finishSucceeds, false, true, false},
		{"continuation takes the lock, Finish fails", finishFails, true, false, false},
		{"continuation takes the lock, Finish ambiguous", finishAmbiguous, true, false, true},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			f := newOwnerFixture(t, releaseVerifies)
			releaseContinuation := admitContinuation(t, f)
			err := f.owner.finishOwner(f.publish(row.finish), f.last)
			var releaseFailure *sharedHomeReleaseError
			if errors.As(err, &releaseFailure) {
				t.Fatalf("a handoff was reported as an unverified release: %v", err)
			}
			if (err != nil) != row.wantErr {
				t.Fatalf("returned %v, want error=%v", err, row.wantErr)
			}
			f.gate.mu.Lock()
			cleared, parked, pending := f.gate.owner == nil, f.gate.unverified != nil, f.owner.pendingOwner != nil
			f.gate.mu.Unlock()
			if cleared != row.ownerCleared || parked || pending != row.pending {
				t.Fatalf("owner cleared=%v parked=%v pending=%v", cleared, parked, pending)
			}
			// The continuation owns the lock; nothing else may take it meanwhile.
			if f.lockIsFree(t) {
				t.Fatal("the continuation's lock was taken over")
			}
			releaseContinuation()
			if !f.lockIsFree(t) {
				t.Fatal("the lock stayed held after the continuation released it")
			}
			if row.ownerCleared {
				return
			}
			// The owner is still tracked: the retry finds its release verified and
			// publishes the Finish again without probing a second time.
			hookSharedHomeRelease(t, nil, nil, func() { t.Error("the retry released the lock a second time") })
			if err := f.owner.finishOwner(f.publish(finishSucceeds), f.last); err != nil {
				t.Fatalf("the retry did not retire the owner: %v", err)
			}
		})
	}
}

// Release verification timeline of one hold, one cell per row: whether closing
// the lock fails, what the probe meets, and whether closing the probe fails.
func TestSharedHomeReleaseVerificationTimeline(t *testing.T) {
	setup := func(t *testing.T) (*ownerFixture, *sharedHomeHold) {
		f := newOwnerFixture(t, releaseVerifies)
		return f, f.owner.profile
	}
	t.Run("clean close, probe takes and closes the lock: verified", func(t *testing.T) {
		_, hold := setup(t)
		if err := hold.release(); err != nil || hold.unverified {
			t.Fatalf("err=%v unverified=%v", err, hold.unverified)
		}
	})
	t.Run("clean close, another holder meets the probe: handoff is verified", func(t *testing.T) {
		f, hold := setup(t)
		releaseContinuation := admitContinuation(t, f)
		if err := hold.release(); err != nil || hold.unverified {
			t.Fatalf("a handoff was not verified: err=%v unverified=%v", err, hold.unverified)
		}
		releaseContinuation()
	})
	t.Run("close fails, probe takes and closes the lock: verified, reported once", func(t *testing.T) {
		_, hold := setup(t)
		hookSharedHomeRelease(t, closeThenFail, nil, nil)
		if err := hold.release(); err == nil {
			t.Fatal("a close failure was dropped")
		}
		if hold.unverified {
			t.Fatal("a lock proven free stayed unverified")
		}
		if err := hold.release(); err != nil {
			t.Fatalf("the repeated release failed: %v", err)
		}
	})
	t.Run("close fails, another holder meets the probe: unverified until the retry", func(t *testing.T) {
		f, hold := setup(t)
		hookSharedHomeRelease(t, closeThenFail, nil, nil)
		releaseContinuation := admitContinuation(t, f)
		err := hold.release()
		if err == nil || !hold.unverified {
			t.Fatalf("an unprovable release was accepted: err=%v unverified=%v", err, hold.unverified)
		}
		if reason, ok := nativeprofile.Reasoned(err); !ok || reason != nativeprofile.ReasonBusy {
			t.Fatalf("the busy cause was lost: %v", err)
		}
		hookSharedHomeRelease(t, func(lease *nativeprofile.Lease) error { return lease.Close() }, nil, nil)
		releaseContinuation()
		if err := hold.release(); err != nil || hold.unverified {
			t.Fatalf("the retry did not verify the release: err=%v unverified=%v", err, hold.unverified)
		}
	})
	t.Run("probe close fails: unverified and retained for retry", func(t *testing.T) {
		_, hold := setup(t)
		fail := true
		hookSharedHomeRelease(t, nil, func(probe *nativeprofile.Lease) error {
			err := probe.Close()
			if fail {
				return errors.New("injected probe close failure")
			}
			return err
		}, nil)
		if err := hold.release(); err == nil || !hold.unverified {
			t.Fatalf("a failed probe release was accepted: err=%v unverified=%v", err, hold.unverified)
		}
		if err := hold.release(); err == nil || !hold.unverified {
			t.Fatalf("the repeated release forgot the failed probe: err=%v unverified=%v", err, hold.unverified)
		}
		fail = false
		if err := hold.release(); err != nil || hold.unverified {
			t.Fatalf("the retry did not verify the release: err=%v unverified=%v", err, hold.unverified)
		}
	})
	t.Run("probe close fails, then another holder meets the retry: not a handoff", func(t *testing.T) {
		f, hold := setup(t)
		hookSharedHomeRelease(t, nil, func(probe *nativeprofile.Lease) error {
			_ = probe.Close()
			return errors.New("injected probe close failure")
		}, nil)
		if err := hold.release(); err == nil {
			t.Fatal("a failed probe release was accepted")
		}
		other, err := holdSharedCodexHome(context.Background(), f.config, f.machine, f.account)
		if err != nil {
			t.Fatal(err)
		}
		if err := hold.release(); err == nil || !hold.unverified {
			t.Fatalf("a holder after a failed probe close was taken as a handoff: err=%v unverified=%v", err, hold.unverified)
		}
		hookSharedHomeRelease(t, nil, func(probe *nativeprofile.Lease) error { return probe.Close() }, nil)
		if err := other.release(); err != nil {
			t.Fatal(err)
		}
		if err := hold.release(); err != nil || hold.unverified {
			t.Fatalf("the retry did not verify the release: err=%v unverified=%v", err, hold.unverified)
		}
	})
	t.Run("home replaced: unverified even after a clean close", func(t *testing.T) {
		f := newOwnerFixture(t, releaseUnverifiable)
		if err := f.owner.profile.release(); err == nil || !f.owner.profile.unverified {
			t.Fatalf("a replaced home was accepted: err=%v", err)
		}
	})
}
