// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"time"
)

type apiCredential struct {
	key          []byte
	quotaProject string
}

// Callers first validate their inspection/execution lease. This resolver then
// binds the exact current account/connection/provider before protected access.
// HTTP never holds accountGate; one original refresh claim survives restarts.
func (s *Service) resolveAPICredential(ctx context.Context, account, connection, providerID domain.ID) (apiCredential, error) {
	requestedConnection := connection
	for {
		unlock, err := s.lockAccounts(ctx)
		if err != nil {
			return apiCredential{}, err
		}
		var metadata domain.AccountOAuthCredential
		var oauth bool
		var provider domain.Provider
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			row, e := tx.Get(domain.AccountKind, account)
			if e != nil {
				return e
			}
			a, e := store.Decode[domain.Account](row)
			if e != nil {
				return e
			}
			a = a.ForConnection(requestedConnection)
			if a.Type != domain.APIAccount || a.Connection == nil || a.Removal != nil || a.Connection.ID != requestedConnection || a.ProviderID != providerID {
				return domain.Fail(domain.PermissionDenied, "The original account connection is no longer active.", "Use its current explicit connection.")
			}
			row, e = tx.Get(domain.ProviderKind, providerID)
			if e != nil {
				return e
			}
			provider, e = store.Decode[domain.Provider](row)
			if e != nil {
				return e
			}
			connection = a.Connection.CredentialReferenceID()
			metadata, oauth, e = tx.AccountOAuthCredential(account, connection)
			return e
		})
		if err != nil {
			unlock()
			return apiCredential{}, err
		}
		s.oauthRefreshMu.Lock()
		done := s.oauthRefreshes[connection]
		s.oauthRefreshMu.Unlock()
		if done != nil {
			unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return apiCredential{}, domain.SafeError(ctx.Err())
			}
		}
		vault, err := s.secrets()
		if err != nil {
			unlock()
			return apiCredential{}, err
		}
		if !oauth {
			key, e := vault.Get(ctx, credentials.Ref{Owner: account, ID: connection, Purpose: credentials.AccountAPI})
			unlock()
			return apiCredential{key: key}, e
		}
		profile, err := s.oauthProfile(provider)
		if err != nil || metadata.ProviderID != providerID || metadata.Preset != profile.preset || metadata.ClientDigest != profile.digest() || metadata.RefreshState != domain.OAuthRefreshIdle {
			unlock()
			return apiCredential{}, oauthCredentialProblem()
		}
		if len(metadata.Cleanup) > 0 {
			for _, id := range metadata.Cleanup {
				if err = vault.Delete(ctx, credentials.Ref{Owner: account, ID: id, Purpose: credentials.AccountAPI}); err != nil {
					break
				}
			}
			if err != nil {
				unlock()
				return apiCredential{}, domain.SafeError(err)
			}
			before := metadata.Revision
			metadata.Revision++
			metadata.Cleanup = nil
			_, err = s.Store.Mutate(ctx, domain.NewID(), "oauth.cleanup-old-generations", connection, func(tx *store.Tx) (any, error) { return nil, tx.PutAccountOAuthCredential(metadata, before) })
			if err != nil {
				unlock()
				return apiCredential{}, err
			}
		}
		raw, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: metadata.TokenID, Purpose: credentials.AccountAPI})
		if err != nil {
			unlock()
			return apiCredential{}, err
		}
		tokens, err := decodeOAuthTokens(raw)
		clear(raw)
		if err != nil {
			unlock()
			return apiCredential{}, err
		}
		if time.Now().Add(time.Minute).Before(metadata.ExpiresAt) {
			clear(tokens.Refresh)
			unlock()
			return apiCredential{key: tokens.Access, quotaProject: metadata.QuotaProject}, nil
		}
		if len(tokens.Refresh) == 0 {
			if time.Now().Before(metadata.ExpiresAt) {
				clear(tokens.Refresh)
				unlock()
				return apiCredential{key: tokens.Access, quotaProject: metadata.QuotaProject}, nil
			}
			tokens.clear()
			unlock()
			return apiCredential{}, domain.Fail(domain.PermissionDenied, "The OAuth access token expired.", "Reconnect this account explicitly.")
		}
		original := metadata
		before := metadata.Revision
		metadata.Revision++
		metadata.RefreshState = domain.OAuthRefreshClaimed
		metadata.RefreshID = domain.NewID()
		_, err = s.Store.Mutate(ctx, domain.NewID(), "oauth.claim-refresh", struct{ Account, Connection, Token, Refresh domain.ID }{account, connection, original.TokenID, metadata.RefreshID}, func(tx *store.Tx) (any, error) { return nil, tx.PutAccountOAuthCredential(metadata, before) })
		if err != nil {
			tokens.clear()
			unlock()
			return apiCredential{}, err
		}
		s.oauthRefreshMu.Lock()
		if s.oauthRefreshes == nil {
			s.oauthRefreshes = map[domain.ID]chan struct{}{}
		}
		done = make(chan struct{})
		s.oauthRefreshes[connection] = done
		s.oauthRefreshMu.Unlock()
		client := s.oauthTokenClient
		if client == nil {
			client = ownedOAuthTokenClient{route: s.outboundResolver()}
		}
		unlock()
		s.logger.InfoContext(ctx, "account_oauth_refresh_claimed", "account_id", account, "connection_id", connection, "refresh_id", metadata.RefreshID)
		next, refreshErr := client.Refresh(ctx, profile, tokens.Refresh)
		if refreshErr == nil && len(next.tokens.Refresh) == 0 {
			next.tokens.Refresh = bytes.Clone(tokens.Refresh)
		}
		tokens.clear()
		settle, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
		release, lockErr := s.lockAccounts(settle)
		if lockErr != nil {
			next.tokens.clear()
			s.oauthReleaseRefresh(connection)
			cancel()
			return apiCredential{}, oauthCredentialProblem()
		}
		resultErr := refreshErr
		var current domain.AccountOAuthCredential
		valid := false
		readErr := s.Store.Read(settle, func(tx *store.Tx) error {
			var e error
			current, valid, e = tx.AccountOAuthCredential(account, connection)
			if e != nil {
				return e
			}
			if !valid || current.RefreshState != domain.OAuthRefreshClaimed || current.RefreshID != metadata.RefreshID || current.Revision != metadata.Revision {
				return oauthCredentialProblem()
			}
			row, e := tx.Get(domain.AccountKind, account)
			if e != nil {
				return e
			}
			a, e := store.Decode[domain.Account](row)
			if e != nil {
				return e
			}
			a = a.ForConnection(requestedConnection)
			if a.Connection == nil || a.Connection.CredentialReferenceID() != connection || a.Removal != nil || a.ProviderID != providerID {
				return oauthCredentialProblem()
			}
			row, e = tx.Get(domain.ProviderKind, providerID)
			if e != nil {
				return e
			}
			p, e := store.Decode[domain.Provider](row)
			if e != nil {
				return e
			}
			accepted, e := s.oauthProfile(p)
			if e != nil || accepted.digest() != profile.digest() {
				return oauthCredentialProblem()
			}
			return nil
		})
		if readErr != nil {
			resultErr = readErr
		}
		if resultErr == nil {
			payload, e := encodeOAuthTokens(next.tokens)
			resultErr = e
			if e == nil {
				_, resultErr = vault.Put(settle, credentials.Ref{Owner: account, ID: metadata.RefreshID, Purpose: credentials.AccountAPI}, payload)
			}
			clear(payload)
		}
		if valid && current.RefreshState == domain.OAuthRefreshClaimed && current.RefreshID == metadata.RefreshID {
			before = current.Revision
			current.Revision++
			if resultErr == nil {
				current.Cleanup = append(current.Cleanup, current.TokenID)
				current.TokenID = metadata.RefreshID
				current.RefreshID = ""
				current.RefreshState = domain.OAuthRefreshIdle
				current.ExpiresAt = next.expires
			} else {
				current.RefreshState = domain.OAuthRefreshRecovery
				if domain.SafeError(refreshErr).Code == domain.PermissionDenied {
					current.RefreshState = domain.OAuthRefreshDenied
				}
				// The claimed reference remains reserved even if protected publication
				// failed or its result was lost. Ordinary account cleanup enumerates it.
			}
			_, e := s.Store.Mutate(settle, domain.NewID(), "oauth.settle-refresh", metadata.RefreshID, func(tx *store.Tx) (any, error) { return nil, tx.PutAccountOAuthCredential(current, before) })
			if e != nil {
				resultErr = e
			}
		}
		s.oauthReleaseRefresh(connection)
		release()
		cancel()
		next.tokens.clear()
		if resultErr != nil {
			s.logger.WarnContext(ctx, "account_oauth_refresh_uncertain", "account_id", account, "connection_id", connection, "code", domain.SafeError(resultErr).Code)
			return apiCredential{}, oauthCredentialProblem()
		}
		s.logger.InfoContext(ctx, "account_oauth_refresh_committed", "account_id", account, "connection_id", connection)
		// Re-read current authority and protected generation; never return an
		// uncommitted token, even to the request that performed the refresh.
	}
}

func (s *Service) oauthReleaseRefresh(connection domain.ID) {
	s.oauthRefreshMu.Lock()
	defer s.oauthRefreshMu.Unlock()
	if done := s.oauthRefreshes[connection]; done != nil {
		delete(s.oauthRefreshes, connection)
		close(done)
	}
}
