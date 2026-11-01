package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileRequiresDurableCompletion(t *testing.T) {
	dir := t.TempDir()
	s := Scope{Version: 1, ID: filepath.Base(dir), Directory: dir, OwnerPID: 2147483647, Started: true}
	if err := save(s); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(Reconcile(s), ErrReconciliation) {
		t.Fatal("dead owner must not prove completion")
	}
	loaded, err := LoadScope(dir)
	if err != nil || loaded.ID != s.ID {
		t.Fatalf("load recovery scope: %+v, %v", loaded, err)
	}
	if _, err := LoadScope("relative"); !errors.Is(err, ErrReconciliation) {
		t.Fatal("relative recovery path accepted")
	}
	s.Complete = true
	if err := save(s); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(s); err != nil {
		t.Fatal(err)
	}
	s.Version = 2
	if err := save(s); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(Reconcile(s), ErrReconciliation) {
		t.Fatal("unknown journal version accepted")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":2`) {
		t.Fatal("unknown journal rewritten")
	}
}
func TestRequiredBarrier(t *testing.T) {
	_, err := Run(context.Background(), t.TempDir(), Command{}, Streams{}, Hooks{})
	if err == nil {
		t.Fatal("missing durable hooks accepted")
	}
}

func TestMain(m *testing.M) {
	h := SupervisorHooks{
		Ready: func(s Scope) error {
			return os.WriteFile(filepath.Join(s.Directory, "test-ready"), []byte("ready"), 0600)
		},
		BeforeStart: func(s Scope, c *Command) error {
			c.Env = append(c.Env, "CCOW_TEST_SUPERVISOR="+s.ID)
			return nil
		},
		Quiesce: func(s Scope) error {
			if Reconcile(s) == nil {
				return errors.New("completion written before IPC quiescence")
			}
			return os.WriteFile(filepath.Join(s.Directory, "test-quiesced"), []byte("quiesced"), 0600)
		},
		Drained: func(s Scope) error {
			if err := Reconcile(s); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(s.Directory, "test-drained"), []byte("drained"), 0600)
		},
	}
	if code, handled := SupervisorMain(h); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
