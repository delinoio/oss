// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type networkSaveState string

const (
	networkSavePending networkSaveState = "pending"
	networkSaveCleanup networkSaveState = "cleanup"
)

// Only one unpublished native generation may exist across network profiles.
// accountGate serializes this private, non-secret journal with every vault use.
// Keep it outside SQLite so a failed/canceled publication cannot lose the
// original actor/input/ref needed for exact retry or confirmed orphan cleanup.
type networkSaveIntent struct {
	Version        uint32           `json:"version"`
	ServerID       domain.ID        `json:"server_id"`
	RequestID      domain.ID        `json:"request_id"`
	Input          networkSaveInput `json:"input"`
	State          networkSaveState `json:"state"`
	Authentication string           `json:"authentication"`
}

func networkSaveRecovery() error {
	return domain.Fail(domain.RecoveryRequired, "The original proxy credential publication requires recovery.", "Retry the original network request; new credential writes wait for confirmed recovery.")
}

func (s *Service) networkSaveIntentPath() string {
	return filepath.Join(s.Store.Root(), "network-save-intent.json")
}

func (s *Service) networkSaveAuthentication(intent networkSaveIntent) string {
	intent.Authentication = ""
	raw, _ := json.Marshal(intent)
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/network/save-intent/v1\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) writeNetworkSaveIntent(intent networkSaveIntent) error {
	intent.Authentication = s.networkSaveAuthentication(intent)
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > 64<<10 {
		return networkSaveRecovery()
	}
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return domain.SafeError(err)
	}
	if err := security.WriteAtomic(s.networkSaveIntentPath(), raw); err != nil {
		return domain.SafeError(err)
	}
	return nil
}

func (s *Service) readNetworkSaveIntent() (networkSaveIntent, bool, error) {
	var intent networkSaveIntent
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return intent, false, domain.SafeError(err)
	}
	raw, err := security.ReadPrivate(s.networkSaveIntentPath(), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return intent, false, nil
	}
	if err != nil {
		return intent, false, domain.SafeError(err)
	}
	if domain.Decode(raw, &intent) != nil || intent.Version != 1 ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(intent.ServerID), intent.ServerID != s.Identity.ServerID) ||
		intent.RequestID.Validate() != nil || intent.Input.ID.Validate() != nil || intent.Input.Definition.Validate() != nil || len(intent.Input.Commitment) != 64 || intent.Input.Clear || (intent.State != networkSavePending && intent.State != networkSaveCleanup) || !hmac.Equal([]byte(intent.Authentication), []byte(s.networkSaveAuthentication(intent))) {
		return networkSaveIntent{}, false, networkSaveRecovery()
	}
	return intent, true, nil
}

func (s *Service) clearNetworkSaveIntent() error {
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return domain.SafeError(err)
	}
	if err := security.RegularPrivate(s.networkSaveIntentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SafeError(err)
	}
	if err := os.Remove(s.networkSaveIntentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SafeError(err)
	}
	if err := security.SyncParent(s.networkSaveIntentPath()); err != nil {
		return domain.SafeError(err)
	}
	return nil
}

// A failed response alone never authorizes deletion. Compare the original
// actor-bound SQL receipt under the shared gate before deciding whether the
// native reference was published. A new request may abandon a proved absent
// publication, but cleanup must finish before any replacement native write.
func (s *Service) reconcileNetworkSaveIntent(ctx context.Context, requestID domain.ID, input networkSaveInput) error {
	intent, exists, err := s.readNetworkSaveIntent()
	if err != nil || !exists {
		return err
	}
	if intent.RequestID == requestID {
		original, _ := json.Marshal(intent.Input)
		current, _ := json.Marshal(input)
		if !bytes.Equal(original, current) {
			return networkConflict()
		}
	}
	_, published, err := s.Store.Replay(ctx, intent.RequestID, "network.save", intent.Input)
	if err != nil {
		return err
	}
	if published {
		if intent.State != networkSavePending {
			return networkSaveRecovery()
		}
		return s.clearNetworkSaveIntent()
	}
	if intent.RequestID == requestID && intent.State == networkSavePending {
		return nil // Preserve the immutable generation for exact retry.
	}
	intent.State = networkSaveCleanup
	if err := s.writeNetworkSaveIntent(intent); err != nil {
		return err
	}
	vault, err := s.secrets()
	if err == nil {
		err = vault.Delete(ctx, proxyRef(intent.Input.ID, intent.RequestID))
	}
	if err != nil {
		s.logger.Warn("network_save_cleanup_pending", "profile_id", intent.Input.ID, "request_id", intent.RequestID, "code", domain.SafeError(err).Code)
		return err
	}
	if err := s.clearNetworkSaveIntent(); err != nil {
		return err
	}
	s.logger.Info("network_save_cleanup_completed", "profile_id", intent.Input.ID, "request_id", intent.RequestID)
	if intent.RequestID == requestID {
		return domain.Fail(domain.Conflict, "The unpublished proxy credential generation was abandoned.", "Use a new request ID after confirmed cleanup; an abandoned generation cannot be reused.")
	}
	return nil
}
