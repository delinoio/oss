package credentials

import (
	"log/slog"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// OpenExisting is the read-only inspection boundary. It cannot initialize a
// scope, install missing locks/pins, reconcile scratch files or write/delete
// secrets. Like normal Vault operations it holds the exact private OS scope
// lock, and callers must close it after their bounded inspection.
func OpenExisting(root string, scope domain.ID, logger *slog.Logger) (*Vault, error) {
	native, err := newNative()
	if err != nil {
		return nil, err
	}
	return openExisting(root, scope, native, logger)
}
func readOnlyFailure() error {
	return domain.Fail(domain.PermissionDenied, "The credential scope is open for inspection only.", "Use the dedicated account lifecycle operation to change credentials.")
}
func openExisting(root string, scope domain.ID, native nativeStore, logger *slog.Logger) (*Vault, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if native == nil {
		return nil, unavailable()
	}
	if err := security.CheckPrivateDir(root); err != nil {
		return nil, safe(err)
	}
	path := filepath.Join(root, "vault.lock")
	if err := security.RegularPrivate(path); err != nil {
		return nil, recovery()
	}
	lock, err := security.TryLockExisting(path)
	if err != nil {
		return nil, safe(err)
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	raw, err := security.ReadPrivate(filepath.Join(root, "scope.json"), 1024)
	if err != nil {
		return nil, recovery()
	}
	var pin struct {
		Scope domain.ID `json:"scope"`
	}
	if domain.Decode(raw, &pin) != nil || pin.Scope != scope {
		return nil, recovery()
	}
	if logger == nil {
		logger = slog.Default()
	}
	success = true
	return &Vault{root: root, scope: scope, native: native, logger: logger, gate: make(chan struct{}, 1), lock: lock, readOnly: true}, nil
}
