package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// Bounds of the retained-runtime secret-remnant check. The check reads only
// regular files that are not links and never follows a link.
const (
	remnantMaxDepth     = 16
	remnantMaxFileBytes = 64 << 10
	remnantMaxTotal     = 8 << 20
)

var (
	remnantJWT     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)
	remnantMarkers = [][]byte{[]byte("access_token"), []byte("refresh_token"), []byte("id_token")}
	remnantNames   = map[string]bool{"auth.json": true, ".credentials.json": true, "credentials.json": true}
)

// legacyAccessOnly identifies one historical access-only operation. Every field
// is compared against the stored receipt so a changed account, connection,
// generation or operation cannot reuse an earlier migration proof.
type legacyAccessOnly struct {
	Kind       nativeprofile.ReceiptKind
	Directory  string // "quota" or "credit"
	Purpose    nativeprofile.Purpose
	Account    domain.ID
	Connection domain.ID
	Generation domain.ID
	Operation  domain.ID
}

func (l legacyAccessOnly) receipt(step nativeprofile.ReceiptStep) nativeprofile.Receipt {
	return nativeprofile.Receipt{Version: nativeprofile.Version, Kind: l.Kind, Operation: l.Operation, Account: l.Account, Connection: l.Connection, Generation: l.Generation, Step: step, RetainedPath: "subscription-runtime/" + l.Directory + "/" + string(l.Operation)}
}

// scanRetainedRuntime is the bounded secret-remnant check of a retained
// historical runtime. Symlinks (including the native helper aliases) are
// counted but never followed or read; any managed credential file, unexpected
// special file, token-like content or exceeded bound keeps the original fence.
func scanRetainedRuntime(path string) error {
	files, total := 0, int64(0)
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > remnantMaxDepth {
			return subscriptionDenied()
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return subscriptionDenied()
		}
		for _, entry := range entries {
			files++
			if files > subscription.RuntimeFileLimit {
				return subscriptionDenied()
			}
			child := filepath.Join(dir, entry.Name())
			info, err := os.Lstat(child)
			if err != nil {
				return subscriptionDenied()
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				// Helper links are left untouched and never followed.
			case info.IsDir():
				if err := walk(child, depth+1); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				if remnantNames[entry.Name()] {
					return subscriptionDenied()
				}
				total += min(info.Size(), subscription.RuntimeByteLimit+1)
				if total > subscription.RuntimeByteLimit {
					return subscriptionDenied()
				}
				if err := scanRemnantFile(child); err != nil {
					return err
				}
			default:
				return subscriptionDenied()
			}
		}
		return nil
	}
	return walk(path, 0)
}

func scanRemnantFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return subscriptionDenied()
	}
	defer file.Close()
	buffer, err := io.ReadAll(io.LimitReader(file, remnantMaxFileBytes))
	if err != nil {
		return subscriptionDenied()
	}
	defer clear(buffer)
	for _, marker := range remnantMarkers {
		if bytes.Contains(buffer, marker) {
			return subscriptionDenied()
		}
	}
	if remnantJWT.Match(buffer) {
		return subscriptionDenied()
	}
	return nil
}

// reconcileAccessOnlyRuntime joins one retained prior-epoch access-only native
// owner and proves that no transient credential survives. The permanent
// profile and any historical per-operation runtime are retained: a historical
// runtime is verified once, recorded in an idempotent migration receipt and
// left untouched. Missing process, credential or ownership proof keeps the
// original fence.
func reconcileAccessOnlyRuntime(ctx context.Context, scope nativeScope, legacy legacyAccessOnly) error {
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	processRoot := filepath.Join(runtimeRoot, "processes")
	owner := legacy.Operation
	if owner.Validate() != nil || scope.Profiles == nil || security.CheckPrivateDir(runtimeRoot) != nil || security.CheckPrivateDir(processRoot) != nil {
		return subscriptionDenied()
	}
	// A missing index cannot prove no send. Require the original retained index
	// and independently exclude a still-live controller before process recovery.
	if err := lockFailedServerLoginControllers(ctx, processRoot, owner); err != nil {
		return err
	}
	if err := process.ReconcileOwnerContext(ctx, processRoot, owner); err != nil {
		return err
	}
	if err := cleanupFailedServerLoginProbes(ctx, runtimeRoot, owner, true); err != nil {
		return err
	}
	// The permanent access-only profile never holds managed authentication.
	key := nativeprofile.Key{Harness: domain.Codex, Purpose: legacy.Purpose, Account: legacy.Account}
	if info, ok, err := scope.Profiles.Lookup(ctx, key); err != nil {
		return subscriptionDenied()
	} else if ok {
		if present, _ := subscription.CredentialFilePresent(info.Root); present {
			return nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "A managed credential file is present in an access-only native profile.", "Reconcile the original operation before retrying.")
		}
	}
	home := legacyAccessOnlyDirectory(scope.Root, legacy.Directory, owner)
	info, err := os.Lstat(home)
	if errors.Is(err, os.ErrNotExist) {
		// Current-model operations have no per-operation runtime. Make a possibly
		// removed historical index durable before publishing the checkpoint.
		parent := filepath.Dir(home)
		if checkErr := security.CheckPrivateDir(parent); errors.Is(checkErr, os.ErrNotExist) {
			return security.SyncParent(parent)
		} else if checkErr != nil {
			return subscriptionDenied()
		}
		return security.SyncParent(home)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || security.CheckPrivateDir(home) != nil || security.CheckPrivateDir(filepath.Dir(home)) != nil {
		return subscriptionDenied()
	}
	return settleLegacyAccessOnly(ctx, scope, legacy, home)
}

// settleLegacyAccessOnly verifies a retained historical runtime and records the
// verified step of its idempotent receipt. It leaves the directory and every
// helper link untouched.
func settleLegacyAccessOnly(ctx context.Context, scope nativeScope, legacy legacyAccessOnly, home string) error {
	existing, ok, err := scope.Profiles.ReadReceipt(ctx, legacy.Kind, legacy.Operation)
	if err != nil {
		return err
	}
	if ok {
		want := legacy.receipt(existing.Step)
		if existing != want {
			return nativeprofile.Failure(domain.Conflict, nativeprofile.ReasonKeyConflict, "The native profile migration receipt belongs to a different identity.", "Keep the original operation fenced and inspect it.")
		}
		if existing.Step == nativeprofile.ReceiptSettled {
			return nil
		}
	}
	if err := scanRetainedRuntime(home); err != nil {
		return err
	}
	return scope.Profiles.WriteReceipt(ctx, legacy.receipt(nativeprofile.ReceiptVerified))
}

// markLegacyAccessOnlySettled records the terminal step once the original
// operation has been settled. A missing receipt (the current model) is a no-op.
func markLegacyAccessOnlySettled(ctx context.Context, scope nativeScope, legacy legacyAccessOnly) error {
	if scope.Profiles == nil {
		return nil
	}
	existing, ok, err := scope.Profiles.ReadReceipt(ctx, legacy.Kind, legacy.Operation)
	if err != nil || !ok {
		return err
	}
	if existing != legacy.receipt(existing.Step) {
		return nativeprofile.Failure(domain.Conflict, nativeprofile.ReasonKeyConflict, "The native profile migration receipt belongs to a different identity.", "Keep the original operation fenced and inspect it.")
	}
	return scope.Profiles.WriteReceipt(ctx, legacy.receipt(nativeprofile.ReceiptSettled))
}
