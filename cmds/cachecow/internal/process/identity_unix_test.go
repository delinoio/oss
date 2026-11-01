//go:build linux || darwin

package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeBoot(t *testing.T, id string, err error) {
	t.Helper()
	previous := currentBootID
	currentBootID = func() (string, error) { return id, err }
	t.Cleanup(func() { currentBootID = previous })
}

func TestReconcileRecoversJournalFromPreviousBoot(t *testing.T) {
	fakeBoot(t, "boot-two", nil)
	s := writeJournal(t, Scope{Version: 2, BootID: "boot-one", OwnerPID: 1, Started: true, ExitCode: 125})
	reason, err := Recover(s)
	if err != nil || reason != RecoveryNativeCompletion {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
	saved, err := LoadScope(s.Directory)
	if err != nil || !saved.Complete || saved.ExitCode != 125 || saved.Version != 2 {
		t.Fatalf("journal not completed: %+v %v", saved, err)
	}
	if err = Reconcile(s); err != nil {
		t.Fatalf("completed journal rejected: %v", err)
	}
}

func TestReconcileFailsClosedWithoutBootProof(t *testing.T) {
	cases := []struct {
		name   string
		scope  Scope
		boot   string
		bootEr error
		reason Recovery
	}{
		{"same boot with dead owner", Scope{Version: 2, BootID: "boot-one", OwnerPID: 2147483647}, "boot-one", nil, RecoverySameBoot},
		{"same boot with live owner", Scope{Version: 2, BootID: "boot-one", OwnerPID: os.Getpid()}, "boot-one", nil, RecoveryOwnerRunning},
		{"empty identity", Scope{Version: 2, OwnerPID: 2147483647}, "boot-two", nil, RecoveryNoNativeIdentity},
		{"version 1 with boot field", Scope{Version: 1, BootID: "boot-one", OwnerPID: 2147483647}, "boot-two", nil, RecoveryNoNativeIdentity},
		{"identity check error", Scope{Version: 2, BootID: "boot-one", OwnerPID: 2147483647}, "", errors.New("unreadable"), RecoveryIdentityCheckFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeBoot(t, tc.boot, tc.bootEr)
			tc.scope.Started = true
			s := writeJournal(t, tc.scope)
			path := filepath.Join(s.Directory, "scope.json")
			before, _ := os.ReadFile(path)
			reason, err := Recover(s)
			if !errors.Is(err, ErrReconciliation) || reason != tc.reason {
				t.Fatalf("reason=%q err=%v", reason, err)
			}
			if !errors.Is(Reconcile(s), ErrReconciliation) {
				t.Fatal("Reconcile released a journal without proof")
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatal("kept journal was rewritten")
			}
		})
	}
}
