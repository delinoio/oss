// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// accountGate permits only one unfinished profile deletion. Persist the
// original receipt input before SQL denial so cancellation or actor revocation
// cannot make native cleanup unreachable. Current authorization is independent
// of that historical input; recovery never impersonates its original actor.
type networkDeleteIntent struct {
	Version        uint32           `json:"version"`
	ServerID       domain.ID        `json:"server_id"`
	RequestID      domain.ID        `json:"request_id"`
	Input          integrationInput `json:"input"`
	Authentication string           `json:"authentication"`
}

func networkDeleteRecovery() error {
	return domain.Fail(domain.RecoveryRequired, "Proxy profile deletion requires cleanup recovery.", "An authorized owner or paired client can retry a network mutation to recover cleanup before changing network configuration.")
}

func (s *Service) networkDeleteIntentPath() string {
	return filepath.Join(s.Store.Root(), "network-delete-intent.json")
}

func (s *Service) networkDeleteAuthentication(intent networkDeleteIntent) string {
	intent.Authentication = ""
	raw, _ := json.Marshal(intent)
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/network/delete-intent/v1\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) writeNetworkDeleteIntent(intent networkDeleteIntent) error {
	intent.Authentication = s.networkDeleteAuthentication(intent)
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > 64<<10 {
		return networkDeleteRecovery()
	}
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return domain.SafeError(err)
	}
	if err := security.WriteAtomic(s.networkDeleteIntentPath(), raw); err != nil {
		return domain.SafeError(err)
	}
	return nil
}

func (s *Service) readNetworkDeleteIntent() (networkDeleteIntent, bool, error) {
	var intent networkDeleteIntent
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return intent, false, domain.SafeError(err)
	}
	raw, err := security.ReadPrivate(s.networkDeleteIntentPath(), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return intent, false, nil
	}
	if err != nil {
		return intent, false, domain.SafeError(err)
	}
	if domain.Decode(raw, &intent) != nil || intent.Version != 1 || intent.ServerID != s.Identity.ServerID || intent.RequestID.Validate() != nil || intent.Input.ID.Validate() != nil || intent.Input.Revision == 0 || !hmac.Equal([]byte(intent.Authentication), []byte(s.networkDeleteAuthentication(intent))) {
		return networkDeleteIntent{}, false, networkDeleteRecovery()
	}
	return intent, true, nil
}

func (s *Service) clearNetworkDeleteIntent() error {
	if err := security.CheckPrivateDir(s.Store.Root()); err != nil {
		return domain.SafeError(err)
	}
	if err := security.RegularPrivate(s.networkDeleteIntentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SafeError(err)
	}
	if err := os.Remove(s.networkDeleteIntentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SafeError(err)
	}
	if err := security.SyncParent(s.networkDeleteIntentPath()); err != nil {
		return domain.SafeError(err)
	}
	return nil
}

// Only an authoritative accepted deletion and an absent public profile permit
// native removal. Unknown receipt/state keeps the bounded obligation intact.
// A proved unaccepted deletion retires metadata without touching credentials.
func (s *Service) reconcileNetworkDeleteIntent(ctx context.Context) (*networkDeleteIntent, error) {
	intent, exists, err := s.readNetworkDeleteIntent()
	if err != nil || !exists {
		return nil, err
	}
	result, accepted, err := s.Store.Replay(ctx, intent.RequestID, "network.delete", intent.Input)
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, s.clearNetworkDeleteIntent()
	}
	var receipt networkReceipt
	if domain.Decode(result.Data, &receipt) != nil || !receipt.Deleted || receipt.ID != intent.Input.ID {
		return nil, networkDeleteRecovery()
	}
	if _, err := s.Store.Get(ctx, domain.NetworkProfileKind, intent.Input.ID); err == nil {
		return nil, networkDeleteRecovery()
	} else if domain.SafeError(err).Code != domain.NotFound {
		return nil, err
	}
	vault, err := s.secrets()
	if err == nil {
		var refs []credentials.Ref
		refs, err = vault.UnremovedReferences(ctx, intent.Input.ID)
		for _, ref := range refs {
			if err != nil {
				break
			}
			if ref.Purpose == credentials.NetworkProxy {
				if err = ctx.Err(); err == nil {
					err = vault.Delete(ctx, ref)
				}
			}
		}
	}
	if err != nil {
		s.logger.Warn("network_delete_cleanup_pending", "profile_id", intent.Input.ID, "request_id", intent.RequestID, "code", domain.SafeError(err).Code)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.clearNetworkDeleteIntent(); err != nil {
		return nil, err
	}
	s.logger.Info("network_delete_cleanup_completed", "profile_id", intent.Input.ID, "request_id", intent.RequestID)
	return &intent, nil
}
