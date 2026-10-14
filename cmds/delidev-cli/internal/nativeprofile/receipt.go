package nativeprofile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ReceiptKind is a closed set of legacy runtime shapes a migration receipt can settle.
type ReceiptKind string

const (
	// ReceiptLegacyQuota settles a historical access-only quota runtime.
	ReceiptLegacyQuota ReceiptKind = "legacy_access_only_quota"
	// ReceiptLegacyCredit settles a historical access-only credit runtime.
	ReceiptLegacyCredit ReceiptKind = "legacy_access_only_credit"
)

// ReceiptStep orders the idempotent migration. A step never moves backwards.
type ReceiptStep string

const (
	// ReceiptVerified records that every retained-directory proof held.
	ReceiptVerified ReceiptStep = "verified"
	// ReceiptSettled records that the original operation was settled once.
	ReceiptSettled ReceiptStep = "settled"
)

const maxReceiptBytes = 4096

// Receipt is the private versioned proof of one legacy migration. Its identity
// fields are compared exactly on every replay so a changed account, connection,
// generation or operation can never be settled by an older receipt.
type Receipt struct {
	Version      int         `json:"version"`
	Kind         ReceiptKind `json:"kind"`
	Operation    domain.ID   `json:"operation"`
	Account      domain.ID   `json:"account"`
	Connection   domain.ID   `json:"connection,omitempty"`
	Generation   domain.ID   `json:"generation,omitempty"`
	Step         ReceiptStep `json:"step"`
	RetainedPath string      `json:"retained_path,omitempty"`
}

func (r Receipt) validate() error {
	if r.Version != Version || (r.Kind != ReceiptLegacyQuota && r.Kind != ReceiptLegacyCredit) || (r.Step != ReceiptVerified && r.Step != ReceiptSettled) ||
		r.Operation.Validate() != nil || r.Account.Validate() != nil || (r.Connection != "" && r.Connection.Validate() != nil) || (r.Generation != "" && r.Generation.Validate() != nil) {
		return Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile migration receipt is invalid.", "Preserve the receipt and the retained directory.")
	}
	return nil
}

func (r *Registry) receiptPath(kind ReceiptKind, operation domain.ID) string {
	return filepath.Join(r.base, "migrations", string(kind)+"-"+string(operation)+".json")
}

func (r *Registry) prepareReceipts() error {
	if err := r.prepare(); err != nil {
		return err
	}
	if err := security.PrivateDir(filepath.Join(r.base, "migrations")); err != nil {
		return Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile migration directory is not private.", "Inspect the owner storage root before retrying.")
	}
	return nil
}

// ReadReceipt returns the stored receipt for one legacy operation, if any.
func (r *Registry) ReadReceipt(ctx context.Context, kind ReceiptKind, operation domain.ID) (Receipt, bool, error) {
	if operation.Validate() != nil {
		return Receipt{}, false, invalidKey()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.prepareReceipts(); err != nil {
		return Receipt{}, false, err
	}
	raw, err := security.ReadPrivate(r.receiptPath(kind, operation), maxReceiptBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile migration receipt is unreadable.", "Preserve the receipt and the retained directory.")
	}
	var peek struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &peek) == nil && peek.Version != Version {
		return Receipt{}, false, Failure(domain.Unsupported, ReasonIncompatible, "The native profile migration receipt version is not supported.", "Use the DeliDev version that created this storage root.")
	}
	var receipt Receipt
	if err := domain.DecodeBounded(raw, &receipt, maxReceiptBytes); err != nil || receipt.validate() != nil || receipt.Kind != kind || receipt.Operation != operation {
		return Receipt{}, false, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile migration receipt is invalid.", "Preserve the receipt and the retained directory.")
	}
	return receipt, true, nil
}

// WriteReceipt records a migration step idempotently. An existing receipt must
// keep the exact identity and may only keep or advance its step; any other
// difference retains the original fence by returning a key conflict.
func (r *Registry) WriteReceipt(ctx context.Context, receipt Receipt) error {
	receipt.Version = Version
	if err := receipt.validate(); err != nil {
		return err
	}
	existing, ok, err := r.ReadReceipt(ctx, receipt.Kind, receipt.Operation)
	if err != nil {
		return err
	}
	if ok {
		same := existing.Account == receipt.Account && existing.Connection == receipt.Connection && existing.Generation == receipt.Generation && existing.RetainedPath == receipt.RetainedPath
		if !same || existing.Step == ReceiptSettled && receipt.Step == ReceiptVerified {
			return Failure(domain.Conflict, ReasonKeyConflict, "The native profile migration receipt belongs to a different identity.", "Keep the original operation fenced and inspect it.")
		}
		if existing.Step == receipt.Step {
			return nil
		}
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.prepareReceipts(); err != nil {
		return err
	}
	if err := security.WriteAtomic(r.receiptPath(receipt.Kind, receipt.Operation), raw); err != nil {
		return err
	}
	if r.config.Logger != nil {
		r.config.Logger.Info("native_profile_migration", "kind", receipt.Kind, "operation_id", receipt.Operation, "step", receipt.Step, "reason", migrationReason(receipt.Step))
	}
	return nil
}

func migrationReason(step ReceiptStep) Reason {
	if step == ReceiptSettled {
		return ReasonMigrationSettled
	}
	return ReasonMigrationVerified
}
