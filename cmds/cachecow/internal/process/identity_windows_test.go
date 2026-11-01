//go:build windows

package process

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func fakeOwner(t *testing.T, probe ownerProbe, err error) {
	t.Helper()
	previous := probeOwner
	probeOwner = func(int) (ownerProbe, error) { return probe, err }
	t.Cleanup(func() { probeOwner = previous })
}

func TestReconcileRecoversWhenSupervisorIdentityIsGone(t *testing.T) {
	created, err := ownerCreation()
	if err != nil {
		t.Fatal(err)
	}
	fakeOwner(t, ownerProbe{}, nil)
	s := writeJournal(t, Scope{Version: 2, OwnerPID: 2147483647, OwnerCreated: created, Started: true, ExitCode: 125})
	reason, err := Recover(s)
	if err != nil || reason != RecoveryNativeCompletion {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
	saved, err := LoadScope(s.Directory)
	if err != nil || !saved.Complete || saved.ExitCode != 125 {
		t.Fatalf("journal not completed: %+v %v", saved, err)
	}
}

func TestReconcileKeepsLiveOrUnknownSupervisor(t *testing.T) {
	created, err := ownerCreation()
	if err != nil {
		t.Fatal(err)
	}
	// The real probe must see this running test process as the supervisor.
	s := writeJournal(t, Scope{Version: 2, OwnerPID: os.Getpid(), OwnerCreated: created, Started: true})
	if reason, err := Recover(s); !errors.Is(err, ErrReconciliation) || reason != RecoveryOwnerRunning {
		t.Fatalf("live owner: %q %v", reason, err)
	}
	zero := writeJournal(t, Scope{Version: 2, OwnerPID: os.Getpid(), Started: true})
	if reason, err := Recover(zero); !errors.Is(err, ErrReconciliation) || reason != RecoveryNoNativeIdentity {
		t.Fatalf("zero creation time: %q %v", reason, err)
	}
	fakeOwner(t, ownerProbe{}, windows.ERROR_ACCESS_DENIED)
	denied := writeJournal(t, Scope{Version: 2, OwnerPID: 2147483647, OwnerCreated: created, Started: true})
	if reason, err := Recover(denied); !errors.Is(err, ErrReconciliation) || reason != RecoveryIdentityCheckFailed {
		t.Fatalf("probe error: %q %v", reason, err)
	}
}
