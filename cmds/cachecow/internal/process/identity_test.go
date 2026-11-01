package process

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootChangedRequiresKnownDifferentBoot(t *testing.T) {
	current := func(id string, err error) func() (string, error) {
		return func() (string, error) { return id, err }
	}
	cases := []struct {
		name    string
		scope   Scope
		current func() (string, error)
		changed bool
		failed  bool
	}{
		{"different boot", Scope{Version: 2, BootID: "old"}, current("new", nil), true, false},
		{"same boot", Scope{Version: 2, BootID: "same"}, current("same", nil), false, false},
		{"empty stored identity", Scope{Version: 2}, current("new", nil), false, false},
		{"version 1 never uses boot identity", Scope{Version: 1, BootID: "old"}, current("new", nil), false, false},
		{"current boot unreadable", Scope{Version: 2, BootID: "old"}, current("", errors.New("boom")), false, true},
		{"current boot empty", Scope{Version: 2, BootID: "old"}, current("", nil), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := bootChanged(tc.scope, tc.current)
			if changed != tc.changed || (err != nil) != tc.failed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestOwnerIdentityGoneRequiresCreationTime(t *testing.T) {
	const created = int64(133000000000000000)
	probe := func(p ownerProbe, err error) func(int) (ownerProbe, error) {
		return func(int) (ownerProbe, error) { return p, err }
	}
	v2 := Scope{Version: 2, OwnerPID: 42, OwnerCreated: created}
	cases := []struct {
		name   string
		scope  Scope
		probe  func(int) (ownerProbe, error)
		gone   bool
		failed bool
	}{
		{"no such process", v2, probe(ownerProbe{}, nil), true, false},
		{"pid reused by newer process", v2, probe(ownerProbe{Found: true, Created: created + 1}, nil), true, false},
		{"same process exited", v2, probe(ownerProbe{Found: true, Created: created, Exited: true}, nil), true, false},
		{"same process running", v2, probe(ownerProbe{Found: true, Created: created}, nil), false, false},
		{"probe error", v2, probe(ownerProbe{}, errors.New("access denied")), false, true},
		{"missing creation time", Scope{Version: 2, OwnerPID: 42}, probe(ownerProbe{}, nil), false, false},
		{"version 1", Scope{Version: 1, OwnerPID: 42, OwnerCreated: created}, probe(ownerProbe{}, nil), false, false},
		{"invalid pid", Scope{Version: 2, OwnerCreated: created}, probe(ownerProbe{}, nil), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gone, err := ownerIdentityGone(tc.scope, tc.probe)
			if gone != tc.gone || (err != nil) != tc.failed {
				t.Fatalf("gone=%v err=%v", gone, err)
			}
		})
	}
}

func writeJournal(t *testing.T, s Scope) Scope {
	t.Helper()
	dir := t.TempDir()
	s.ID, s.Directory = filepath.Base(dir), dir
	if err := save(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVersionOneJournalLoadsUnchanged(t *testing.T) {
	s := writeJournal(t, Scope{Version: 1, OwnerPID: 2147483647, Started: true, ExitCode: 125})
	raw, err := os.ReadFile(filepath.Join(s.Directory, "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "boot_id") || strings.Contains(string(raw), "owner_created") {
		t.Fatalf("version 1 journal gained identity fields: %s", raw)
	}
	loaded, err := LoadScope(s.Directory)
	if err != nil || loaded.Version != 1 || loaded.BootID != "" || loaded.OwnerCreated != 0 {
		t.Fatalf("load: %+v %v", loaded, err)
	}
	reason, err := Recover(loaded)
	if !errors.Is(err, ErrReconciliation) || reason != RecoveryNoNativeIdentity {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
	if after, _ := os.ReadFile(filepath.Join(s.Directory, "scope.json")); string(after) != string(raw) {
		t.Fatal("kept journal was rewritten")
	}
}

func TestRecoverReportsJournalOutcomes(t *testing.T) {
	complete := writeJournal(t, Scope{Version: 2, OwnerPID: 1, Complete: true})
	if reason, err := Recover(complete); err != nil || reason != RecoveryJournalComplete {
		t.Fatalf("complete: %q %v", reason, err)
	}
	missing := Scope{Version: 1, ID: "missing", Directory: filepath.Join(t.TempDir(), "missing")}
	if reason, err := Recover(missing); !errors.Is(err, ErrReconciliation) || reason != RecoveryJournalUnreadable {
		t.Fatalf("missing: %q %v", reason, err)
	}
	empty := writeJournal(t, Scope{Version: 2, OwnerPID: 2147483647, Started: true})
	if reason, err := Recover(empty); !errors.Is(err, ErrReconciliation) || reason != RecoveryNoNativeIdentity {
		t.Fatalf("empty identity: %q %v", reason, err)
	}
}
