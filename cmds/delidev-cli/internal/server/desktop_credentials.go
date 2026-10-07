// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

type DesktopCredentialAction string

const (
	DesktopCredentialBegin  DesktopCredentialAction = "begin"
	DesktopCredentialStatus DesktopCredentialAction = "status"
	DesktopCredentialRetry  DesktopCredentialAction = "retry"
	DesktopCredentialSkip   DesktopCredentialAction = "skip"
)

type DesktopCredentialState string

const (
	DesktopCredentialChecking  DesktopCredentialState = "checking"
	DesktopCredentialSucceeded DesktopCredentialState = "succeeded"
	DesktopCredentialFailed    DesktopCredentialState = "failed"
	DesktopCredentialSkipped   DesktopCredentialState = "skipped"
)

type DesktopCredentialIssue string

const (
	credentialConfirmation DesktopCredentialIssue = "confirmation-required"
	credentialUnavailable  DesktopCredentialIssue = "unavailable"
	credentialRecovery     DesktopCredentialIssue = "recovery-required"
	credentialChanged      DesktopCredentialIssue = "executable-changed"
	credentialInvalid      DesktopCredentialIssue = "executable-invalid"
	credentialDenied       DesktopCredentialIssue = "permission-denied"
)

// Only non-secret, closed presentation values cross the resident host pipe.
type DesktopCredentialResult struct {
	AttemptID domain.ID              `json:"attempt_id"`
	State     DesktopCredentialState `json:"state"`
	Issue     DesktopCredentialIssue `json:"issue,omitempty"`
}

// DesktopCredentialAccess is attached only to an app-owned server. It shares
// that server's vault and joins the original native call before vault closure.
// A canceled OS prompt may remain open; cancellation stops subsequent reads.
type DesktopCredentialAccess struct {
	mu       sync.Mutex
	service  *Service
	lifetime context.Context
	actor    domain.ID
	result   DesktopCredentialResult
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
	platform string // Tests select a platform without accessing a real keychain.
}

func (c *DesktopCredentialAccess) attach(ctx context.Context, s *Service) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.service, c.lifetime, c.closed = s, ctx, false
}

func (c *DesktopCredentialAccess) close() {
	c.mu.Lock()
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (c *DesktopCredentialAccess) Handle(ctx context.Context, action DesktopCredentialAction, attempt, previous, device domain.ID) (DesktopCredentialResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case DesktopCredentialBegin, DesktopCredentialStatus, DesktopCredentialRetry, DesktopCredentialSkip:
	default:
		return DesktopCredentialResult{}, domain.Fail(domain.InvalidArgument, "Unknown desktop credential action.", "Use the closed desktop control.")
	}
	if attempt.Validate() != nil || device.Validate() != nil {
		return DesktopCredentialResult{}, domain.Fail(domain.InvalidArgument, "Invalid desktop credential operation.", "Retain the original desktop attempt.")
	}
	if c.service == nil {
		return DesktopCredentialResult{AttemptID: attempt, State: DesktopCredentialSkipped}, nil
	}
	if c.closed || c.lifetime.Err() != nil {
		return DesktopCredentialResult{}, domain.SafeError(context.Canceled)
	}
	actor := domain.Principal{Type: domain.ClientDevice, DeviceID: device}
	if err := c.service.Store.Read(ctx, func(tx *store.Tx) error { return subscriptionActorValid(tx, actor) }); err != nil {
		return DesktopCredentialResult{}, err
	}
	if c.actor != "" && c.actor != device {
		sameAttempt := attempt == c.result.AttemptID && c.result.AttemptID != ""
		explicitRetry := action == DesktopCredentialRetry && previous == c.result.AttemptID && c.result.State == DesktopCredentialFailed
		if (!sameAttempt && !explicitRetry) || desktopCredentialPreviousActorRevoked(ctx, c.service, c.actor) != nil {
			return DesktopCredentialResult{}, subscriptionDenied()
		}
		// Only the authenticated replacement client can continue the exact
		// retained attempt after the original paired client is confirmed revoked.
		c.actor = device
	}
	if c.result.AttemptID == attempt {
		if action == DesktopCredentialSkip {
			if c.cancel != nil {
				c.cancel()
			}
			if c.result.State != DesktopCredentialSkipped {
				c.service.logger.Info("desktop_credential_access", "request_id", attempt, "phase", "skipped")
			}
			c.result.State, c.result.Issue = DesktopCredentialSkipped, ""
		} else if action != DesktopCredentialBegin && action != DesktopCredentialStatus && action != DesktopCredentialRetry {
			return DesktopCredentialResult{}, domain.Fail(domain.InvalidArgument, "Unknown desktop credential action.", "Use the closed desktop control.")
		}
		return c.result, nil
	}
	if action != DesktopCredentialBegin && action != DesktopCredentialRetry && action != DesktopCredentialSkip {
		return DesktopCredentialResult{}, domain.Fail(domain.Conflict, "The desktop credential attempt changed.", "Observe its original attempt.")
	}
	if c.result.AttemptID != "" {
		if (action != DesktopCredentialRetry && action != DesktopCredentialSkip) || previous != c.result.AttemptID || c.result.State != DesktopCredentialFailed {
			return DesktopCredentialResult{}, domain.Fail(domain.Conflict, "The desktop credential attempt is already retained.", "Retry only an explicitly observed failure.")
		}
		select {
		case <-c.done:
		default:
			return DesktopCredentialResult{}, domain.Fail(domain.Conflict, "The original keychain call has not completed.", "Wait for the original native operation.")
		}
	} else if (action != DesktopCredentialBegin && action != DesktopCredentialSkip) || previous != "" {
		return DesktopCredentialResult{}, domain.Fail(domain.Conflict, "No original desktop credential attempt exists.", "Begin the original attempt first.")
	}
	c.actor = device
	c.result = DesktopCredentialResult{AttemptID: attempt, State: DesktopCredentialChecking}
	// A lost Begin/Retry reply may mean the original request never arrived.
	// Retain its explicit skip before any delayed request can open the vault.
	if action == DesktopCredentialSkip {
		c.result.State = DesktopCredentialSkipped
		c.service.logger.Info("desktop_credential_access", "request_id", attempt, "phase", "skipped")
		return c.result, nil
	}
	platform := c.platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "darwin" {
		c.result.State = DesktopCredentialSkipped
		return c.result, nil
	}
	run, cancel := context.WithCancel(c.lifetime)
	c.cancel, c.done = cancel, make(chan struct{})
	done, service := c.done, c.service
	service.logger.Info("desktop_credential_access", "request_id", attempt, "phase", "started")
	go func() {
		defer close(done)
		defer cancel()
		currentActor := func() domain.Principal {
			c.mu.Lock()
			defer c.mu.Unlock()
			return domain.Principal{Type: domain.ClientDevice, DeviceID: c.actor}
		}
		err := service.checkDesktopCredentials(run, currentActor)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.result.AttemptID != attempt || c.result.State == DesktopCredentialSkipped {
			return
		}
		c.result.State = DesktopCredentialSucceeded
		if err != nil {
			c.result.State, c.result.Issue = DesktopCredentialFailed, desktopCredentialIssue(err)
			service.logger.Warn("desktop_credential_access", "request_id", attempt, "phase", "failed", "error_code", domain.SafeError(err).Code)
		} else {
			service.logger.Info("desktop_credential_access", "request_id", attempt, "phase", "completed")
		}
	}()
	return c.result, nil
}

func desktopCredentialIssue(err error) DesktopCredentialIssue {
	p := domain.SafeError(err)
	switch p.Cause {
	case credentials.ExecutableChangedCause:
		return credentialChanged
	case credentials.ExecutableInvalidCause:
		return credentialInvalid
	}
	switch p.Code {
	case domain.ConfirmationRequired:
		return credentialConfirmation
	case domain.PermissionDenied, domain.Unauthenticated:
		return credentialDenied
	case domain.RecoveryRequired:
		return credentialRecovery
	default:
		return credentialUnavailable
	}
}

func desktopCredentialPreviousActorRevoked(ctx context.Context, s *Service, id domain.ID) error {
	return s.Store.Read(ctx, func(tx *store.Tx) error {
		row, err := tx.Get(domain.DeviceKind, id)
		if err != nil {
			return subscriptionDenied()
		}
		device, err := store.Decode[domain.Device](row)
		if err != nil || device.Type != domain.ClientDevice || device.MachineID != "" || !device.Revoked {
			return subscriptionDenied()
		}
		return nil
	})
}

func (s *Service) checkDesktopCredentials(ctx context.Context, actor func() domain.Principal) error {
	filter := store.Filter{Kind: domain.AccountKind, AccountType: domain.APIAccount, Limit: store.MaxPage}
	for {
		rows, more, err := s.Store.ListPage(ctx, filter)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.checkDesktopCredential(ctx, actor, row.ID); err != nil {
				return err
			}
			filter.After = row.ID
		}
		if !more {
			return nil
		}
	}
}

func (s *Service) checkDesktopCredential(ctx context.Context, actor func() domain.Principal, id domain.ID) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var ref credentials.Ref
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := subscriptionActorValid(tx, actor()); err != nil {
			return err
		}
		row, err := tx.Get(domain.AccountKind, id)
		if err != nil && domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		a, err := store.Decode[domain.Account](row)
		if err != nil {
			return err
		}
		if a.Type != domain.APIAccount || a.Connection == nil || a.Removal != nil {
			return nil
		}
		row, err = tx.Get(domain.ProviderKind, a.ProviderID)
		if err != nil {
			return err
		}
		providerProfile, err := store.Decode[domain.Provider](row)
		p := providerProfile
		if err == nil {
			p, err = providers.ResolveAccountProfile(providerProfile, a)
		}
		if err == nil {
			err = p.Validate()
		}
		if err != nil {
			return err
		}
		if a.Connection.Authentication != p.Authentication {
			return oauthCredentialProblem()
		}
		if p.Authentication == domain.KeylessAuth {
			return nil
		}
		referenceID := a.Connection.CredentialReferenceID()
		ref = credentials.Ref{Owner: id, ID: referenceID, Purpose: credentials.AccountAPI}
		metadata, oauth, err := tx.AccountOAuthCredential(id, referenceID)
		if err != nil {
			return err
		}
		if oauth {
			// A readiness read must never refresh or clean old generations.
			profile, err := s.oauthProfile(providerProfile)
			if err != nil || metadata.ProviderID != a.ProviderID || metadata.Preset != profile.preset || metadata.ClientDigest != profile.digest() {
				return oauthCredentialProblem()
			}
			ref.ID = metadata.TokenID
		}
		return nil
	})
	if err != nil || ref.ID == "" {
		return err
	}
	vault := s.accountSecrets
	if vault == nil {
		// Startup readiness is read-only. Opening a missing or damaged scope
		// must not initialize locks, pins, or cleanup state.
		existing, err := credentials.OpenExisting(filepath.Join(s.Store.Root(), "secrets"), s.Identity.ServerID, s.logger)
		if err != nil {
			return err
		}
		defer existing.Close()
		vault = existing
	}
	secret, err := vault.Get(ctx, ref)
	clear(secret)
	if err != nil {
		return err
	}
	// Revocation/cancellation while a synchronous native prompt was open
	// cannot turn its eventual approval into current readiness evidence.
	return errors.Join(ctx.Err(), s.Store.Read(ctx, func(tx *store.Tx) error { return subscriptionActorValid(tx, actor()) }))
}
