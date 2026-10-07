// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type oauthDeviceGrant struct {
	device, user []byte
	expires      time.Time
	interval     time.Duration
}

func (g *oauthDeviceGrant) clear() { clear(g.device); clear(g.user) }

type oauthDevicePoll uint8

const (
	oauthDeviceIssued oauthDevicePoll = iota
	oauthDevicePending
	oauthDeviceSlowDown
)

type oauthDeviceClient interface {
	Authorize(context.Context, oauthProfile) (oauthDeviceGrant, error)
	Poll(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error)
}

func (c ownedOAuthTokenClient) Authorize(ctx context.Context, p oauthProfile) (oauthDeviceGrant, error) {
	var g oauthDeviceGrant
	status, v, err := c.request(ctx, p.authorization, map[string][]byte{"client_id": []byte(p.registration.ClientID)})
	if err != nil {
		return g, err
	}
	defer clearOAuthObject(v)
	var uri string
	seconds, e := strconv.ParseInt(string(v["expires_in"]), 10, 32)
	interval := int64(5)
	if raw, present := v["interval"]; present {
		interval, err = strconv.ParseInt(string(raw), 10, 32)
	}
	if status != http.StatusOK || e != nil || err != nil || seconds < 1 || seconds > 86400 || interval < 1 || interval > 60 || json.Unmarshal(v["verification_uri"], &uri) != nil || uri != p.registration.VerificationURI {
		return g, oauthCredentialProblem()
	}
	g.device, err = decodeOAuthASCIIKey(v["device_code"])
	if err == nil {
		g.user, err = decodeOAuthASCIIKey(v["user_code"])
	}
	if err != nil || len(g.device) > 8192 || len(g.user) > 64 {
		g.clear()
		return oauthDeviceGrant{}, oauthCredentialProblem()
	}
	// Approval codes have a bounded printable surface. Device codes stay server-only.
	for _, b := range g.user {
		if b < 33 || b > 126 {
			g.clear()
			return oauthDeviceGrant{}, oauthCredentialProblem()
		}
	}
	g.interval = time.Duration(interval) * time.Second
	g.expires = time.Now().Add(time.Duration(seconds) * time.Second)
	return g, nil
}
func (c ownedOAuthTokenClient) Poll(ctx context.Context, p oauthProfile, device []byte) (oauthTokenResult, oauthDevicePoll, error) {
	status, v, err := c.request(ctx, p.token, map[string][]byte{"grant_type": []byte("urn:ietf:params:oauth:grant-type:device_code"), "client_id": []byte(p.registration.ClientID), "device_code": device})
	if err != nil {
		return oauthTokenResult{}, oauthDeviceIssued, err
	}
	defer clearOAuthObject(v)
	if status == http.StatusBadRequest {
		var code string
		if json.Unmarshal(v["error"], &code) == nil {
			switch code {
			case "authorization_pending":
				return oauthTokenResult{}, oauthDevicePending, nil
			case "slow_down":
				return oauthTokenResult{}, oauthDeviceSlowDown, nil
			case "access_denied", "expired_token":
				return oauthTokenResult{}, oauthDeviceIssued, domain.Fail(domain.PermissionDenied, "Device authorization was denied or expired.", "Cancel before starting another connection.")
			}
		}
	}
	result, err := parseOAuthToken(status, v, p)
	return result, oauthDeviceIssued, err
}
func (s *Service) deviceOAuthClient() oauthDeviceClient {
	if s.oauthDeviceClient != nil {
		return s.oauthDeviceClient
	}
	return ownedOAuthTokenClient{route: s.outboundResolver()}
}

// Only the original Go polling owner can supply a received device grant to the
// existing protected completion path. Public Complete never polls or exchanges.
type deviceOAuthResultKey struct{}
type deviceOAuthResult struct {
	attempt domain.ID
	result  oauthTokenResult
}

func (s *Service) authorizeOAuthDevice(ctx context.Context, a domain.AccountOAuthAttempt, profile oauthProfile, actor domain.Principal, finish func()) error {
	grant, err := s.deviceOAuthClient().Authorize(ctx, profile)
	defer grant.clear()
	work, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), actor), 5*time.Second)
	defer stop()
	unlock, lockErr := s.lockAccounts(work)
	if lockErr != nil {
		finish()
		return lockErr
	}
	current, readErr := s.oauthRead(work, a.ID)
	if readErr == nil && current.State == domain.OAuthAwaiting && current.Generation == s.oauthGeneration && ctx.Err() == nil {
		if err == nil {
			err = s.Store.Read(work, func(tx *store.Tx) error { return s.oauthAttemptProvider(tx, a) })
		}
		if err == nil {
			live := s.oauthLive[a.ID]
			if live == nil {
				err = oauthCredentialProblem()
			} else {
				live.authorization = profile.registration.VerificationURI
				live.userCode = bytes.Clone(grant.user)
				device := bytes.Clone(grant.device)
				state := bytes.Clone(live.state)
				deadline := grant.expires
				if a.ExpiresAt.Before(deadline) {
					deadline = a.ExpiresAt
				}
				go s.pollOAuthDevice(ctx, current, profile, device, state, grant.interval, deadline, finish)
				unlock()
				return nil
			}
		}
		s.oauthRecoveryLocked(current, oauthProblemFor(current))
	}
	s.clearOAuthLive(a.ID)
	unlock()
	finish()
	if readErr != nil {
		return readErr
	}
	// Start was admitted. Its original receipt remains recoverable even if the
	// one authorize response was lost; no replay can issue another request.
	return nil
}
func (s *Service) pollOAuthDevice(ctx context.Context, a domain.AccountOAuthAttempt, profile oauthProfile, device, state []byte, interval time.Duration, deadline time.Time, finish func()) {
	defer finish()
	defer clear(device)
	defer clear(state)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			s.settleOAuthDevice(a, domain.OAuthExpired, domain.Fail(domain.PermissionDenied, "Device authorization expired.", "Cancel before starting another connection."))
			return
		}
		delay := interval
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.settleOAuthDevice(a, domain.OAuthRecovery, oauthProblemFor(a))
			return
		case <-timer.C:
		}
		if !time.Now().Before(deadline) {
			continue
		}
		// This private durable dispatch owner was established in Start. Recheck the
		// original provider before each explicitly allowed pending/slow_down poll.
		unlock, e := s.lockAccounts(ctx)
		if e != nil {
			s.settleOAuthDevice(a, domain.OAuthRecovery, oauthProblemFor(a))
			return
		}
		current, e := s.oauthRead(ctx, a.ID)
		if e == nil && (current.State != domain.OAuthAwaiting || current.Generation != s.oauthGeneration || current.DeviceRequestID != a.DeviceRequestID) {
			e = oauthCredentialProblem()
		}
		if e == nil {
			e = s.Store.Read(ctx, func(tx *store.Tx) error { return s.oauthAttemptProvider(tx, a) })
		}
		unlock()
		if e != nil {
			s.settleOAuthDevice(a, domain.OAuthRecovery, oauthProblemFor(a))
			return
		}
		// Device approval can outlive Start by minutes. A valid admission is not
		// a code-identity lease for a later request that can issue credentials.
		if e := s.checkCredentialRuntime(ctx, credentialRuntimeOAuthExchange); e != nil {
			s.settleOAuthDevice(a, domain.OAuthRecovery, domain.SafeError(e))
			return
		}
		result, phase, e := s.deviceOAuthClient().Poll(ctx, profile, device)
		if e != nil {
			result.tokens.clear()
			state := domain.OAuthRecovery
			if domain.SafeError(e).Code == domain.PermissionDenied {
				state = domain.OAuthFailed
			}
			s.settleOAuthDevice(a, state, domain.SafeError(e))
			return
		}
		if phase == oauthDevicePending {
			result.tokens.clear()
			continue
		}
		if phase == oauthDeviceSlowDown {
			result.tokens.clear()
			interval += 5 * time.Second
			continue
		}
		if phase != oauthDeviceIssued {
			result.tokens.clear()
			s.settleOAuthDevice(a, domain.OAuthRecovery, oauthProblemFor(a))
			return
		}
		completeCtx := context.WithValue(ctx, deviceOAuthResultKey{}, deviceOAuthResult{attempt: a.ID, result: result})
		_, e = s.CompleteAccountOAuth(completeCtx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: &pb.Mutation{Id: string(a.ID), ExpectedRevision: current.Revision, RequestId: string(domain.NewID())}, AuthorizationCode: []byte("server-owned-device-result"), AuthorizationState: bytes.Clone(state)}))
		result.tokens.clear()
		if e != nil {
			s.settleOAuthDevice(a, domain.OAuthRecovery, oauthProblemFor(a))
		}
		return
	}
}
func (s *Service) settleOAuthDevice(a domain.AccountOAuthAttempt, state domain.AccountOAuthState, problem *domain.Error) {
	ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), a.Actor), 5*time.Second)
	defer stop()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return
	}
	defer unlock()
	current, err := s.oauthRead(ctx, a.ID)
	if err == nil && current.State == domain.OAuthAwaiting && current.Generation == a.Generation && current.DeviceRequestID == a.DeviceRequestID {
		_, err = s.oauthUpdate(ctx, current, state, func(v *domain.AccountOAuthAttempt) { v.Problem = problem })
	}
	s.clearOAuthLive(a.ID)
	s.logger.InfoContext(ctx, "account_oauth_device_settled", "attempt_id", a.ID, "state", state, "error_code", func() domain.Code {
		if err != nil {
			return domain.SafeError(err).Code
		}
		return ""
	}())
}
