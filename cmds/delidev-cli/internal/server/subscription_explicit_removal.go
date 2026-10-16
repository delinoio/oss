// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Explicit credential removal.
//
// Routine server subscription operations (login, refresh, observation and the
// Claude setup-token lifecycle) retain the account-owned credential file of the
// account's auth profile and every protected AccountLogin vault generation. Only
// the explicit lifecycle ends below remove them:
//
//   - explicit server logout (ChatGPT, Claude and Grok),
//   - explicit account deletion.
//
// The removal is complete: it enumerates every unremoved reference of the owner
// without a count limit, validates owner and purpose before the first deletion,
// observes cancellation between items and verifies the final enumeration. The
// shared Worker Finish helper (cleanupSubscriptionReferences) is a separate,
// budgeted mechanism and is never used here.

// credentialRemovalResult is the closed result vocabulary of removal logs.
type credentialRemovalResult string

const (
	credentialRemovalConfirmed credentialRemovalResult = "confirmed"
	credentialRemovalRetained  credentialRemovalResult = "retained"
)

// credentialRemovalStage is the closed stage vocabulary of removal logs.
type credentialRemovalStage string

const (
	credentialRemovalStageFiles      credentialRemovalStage = "files"
	credentialRemovalStageReferences credentialRemovalStage = "references"
)

// removeAccountLoginReferences removes every unremoved AccountLogin reference of
// owner. A reference of another owner or purpose denies the whole removal before
// any deletion, so an unrelated credential is never touched. Deletion is per
// item and observes ctx between items. Replay is idempotent: removed references
// are tombstoned by the vault and no longer enumerated.
func removeAccountLoginReferences(ctx context.Context, vault accountSecrets, owner domain.ID) (int, error) {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		// The read-only Claude usage references of the same account are removed
		// with its execution generations; any other purpose still denies.
		if ref.Owner != owner || ref.Purpose != credentials.AccountLogin && ref.Purpose != credentials.ClaudeUsage || ref.ID.Validate() != nil {
			return 0, subscriptionDenied()
		}
	}
	removed := 0
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return removed, domain.SafeError(err)
		}
		if err := vault.Delete(ctx, ref); err != nil {
			return removed, err
		}
		removed++
	}
	remaining, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return removed, err
	}
	if len(remaining) != 0 {
		return removed, subscriptionDenied()
	}
	return removed, nil
}

// removeAccountCredentials is the explicit removal of one server-owned
// subscription account's retained credentials. includeFiles selects the
// credential files of the account's native auth profiles (ChatGPT and Claude);
// a Grok account owns no native profile. legacyOperation names the historical
// per-operation runtime whose credential file is removed with the profile files
// (empty when none applies). The caller holds the account gate and has already
// proven that the original native process is closed.
func (s *Service) removeAccountCredentials(ctx context.Context, id, legacyOperation domain.ID, includeFiles bool) (err error) {
	stage := credentialRemovalStageFiles
	removed := 0
	defer func() {
		if err != nil {
			s.logger.WarnContext(ctx, "server_credential_removal", "account_id", id, "operation_id", legacyOperation, "result", credentialRemovalRetained, "stage", stage, "references_removed", removed, "code", domain.SafeError(err).Code)
			return
		}
		s.logger.InfoContext(ctx, "server_credential_removal", "account_id", id, "operation_id", legacyOperation, "result", credentialRemovalConfirmed, "files", includeFiles, "references_removed", removed)
	}()
	// The Claude usage connection ends with the account's execution credentials:
	// its reads stop, live work is cancelled and its native storage and profile
	// are removed before the execution generations are.
	if err = s.retireClaudeUsage(ctx, id); err != nil {
		return err
	}
	if includeFiles {
		if err = retireServerLoginCredentials(ctx, s.scopeFor(id), legacyRuntimeHome(s.scopeFor(id), legacyOperation), legacyOperation); err != nil {
			return err
		}
	}
	stage = credentialRemovalStageReferences
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	removed, err = removeAccountLoginReferences(ctx, vault, id)
	return err
}

// legacyRuntimeHome is the historical per-operation auth home of one server
// operation. New operations never create it; its credential file is removed only
// by the explicit lifecycle. An empty operation selects no legacy home.
func legacyRuntimeHome(scope nativeScope, operation domain.ID) string {
	if operation == "" {
		return ""
	}
	return filepath.Join(scope.Root, "subscription-runtime", "auth", string(operation))
}

// credentialRemovalFailure marks a failure of the explicit credential removal
// that account deletion owns. It keeps the original closed error reachable
// through errors.As, so callers classify the retained outcome as unconfirmed
// cleanup instead of a resource reference.
type credentialRemovalFailure struct{ error }

func (e credentialRemovalFailure) Unwrap() error { return e.error }
