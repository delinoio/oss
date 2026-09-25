package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func controllerScope(t *testing.T) (Config, string) {
	t.Helper()
	c := config(t, "streams")
	if err := security.PrivateDir(c.Directory); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(c.Directory, string(c.OwnerID))
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	return c, filepath.Join(owner, string(domain.NewID()))
}

func TestControllerCreationFailureRemovesOnlyEmptyScopeAndAllowsRetry(t *testing.T) {
	c, scope := controllerScope(t)
	failure := errors.New("injected controller creation failure")
	synchronized := false
	controller, err := prepareController(scope, func(path string) (*security.Lock, error) {
		if path != filepath.Join(scope, "controller.lock") {
			t.Fatal("unexpected controller path")
		}
		return nil, failure
	}, func(path string) error {
		if path != scope {
			t.Fatal("synchronized another scope")
		}
		if _, err := os.Lstat(scope); !os.IsNotExist(err) {
			t.Fatal("scope was not removed before synchronization")
		}
		synchronized = true
		return security.SyncParent(path)
	})
	if controller != nil || !errors.Is(err, failure) || !synchronized {
		t.Fatal("pre-launch failure did not retain its cause and synchronize cleanup", err)
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal("empty pre-launch scope blocked recovery", err)
	}
	// A real subsequent process must still obtain its own scope and lifecycle.
	err = Run(context.Background(), c)
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatal("subsequent native work failed", err)
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal("subsequent cleanup failed", err)
	}
}

func TestControllerCreationFailurePreservesNonemptyScope(t *testing.T) {
	for _, name := range []string{"controller.lock", "ownership.json", "unexpected"} {
		t.Run(name, func(t *testing.T) {
			c, scope := controllerScope(t)
			controller, err := prepareController(scope, func(string) (*security.Lock, error) {
				if err := os.WriteFile(filepath.Join(scope, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
				return nil, errors.New("injected partial creation")
			}, func(string) error {
				t.Fatal("nonempty scope was removed")
				return nil
			})
			if controller != nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unexpected evidence did not retain recovery", err)
			}
			if info, err := os.Lstat(filepath.Join(scope, name)); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
				t.Fatal("pre-launch evidence changed", err)
			}
			if err := ReconcileOwner(c.Directory, c.OwnerID); err == nil {
				t.Fatal("unproven evidence became completion")
			}
		})
	}
}

func TestControllerPreparationNeverAdoptsExistingScope(t *testing.T) {
	_, scope := controllerScope(t)
	if err := security.PrivateDir(scope); err != nil {
		t.Fatal(err)
	}
	controller, err := prepareController(scope, func(string) (*security.Lock, error) {
		t.Fatal("adopted existing scope")
		return nil, nil
	}, func(string) error {
		t.Fatal("removed existing scope")
		return nil
	})
	if controller != nil || !os.IsExist(err) {
		t.Fatal("scope collision accepted", err)
	}
	if err := security.CheckPrivateDir(scope); err != nil {
		t.Fatal("existing empty scope was changed", err)
	}
}

func TestControllerRollbackSyncFailureRetainsRecovery(t *testing.T) {
	_, scope := controllerScope(t)
	controller, err := prepareController(scope, func(string) (*security.Lock, error) {
		return nil, errors.New("injected lock failure")
	}, func(string) error {
		return errors.New("injected sync failure")
	})
	if controller != nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unsynchronized removal reported safe retry", err)
	}
}

func TestControllerRollbackPreservesReplacedScope(t *testing.T) {
	for _, directory := range []bool{false, true} {
		_, scope := controllerScope(t)
		original := scope + ".original"
		controller, err := prepareController(scope, func(string) (*security.Lock, error) {
			if err := os.Rename(scope, original); err != nil {
				t.Fatal(err)
			}
			var err error
			if directory {
				err = security.PrivateDir(scope)
			} else {
				err = os.WriteFile(scope, nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			return nil, errors.New("injected replacement")
		}, func(string) error {
			t.Fatal("replacement scope was removed")
			return nil
		})
		if controller != nil || domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("replacement scope did not retain recovery", err)
		}
		if info, err := os.Lstat(scope); err != nil || info.IsDir() != directory {
			t.Fatal("replacement evidence was removed", err)
		}
		if err := security.CheckPrivateDir(original); err != nil {
			t.Fatal("original scope was changed", err)
		}
	}
}
