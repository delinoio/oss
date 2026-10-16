// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The one server usage-read owner. Row and Manage refresh, Refresh all and the
// inactive maintenance loop all end here, so overlapping triggers coalesce into
// one durable single-flight read per account. Usage evidence is informational:
// it is never blocking routing evidence and a failed read never changes the
// execution account's readiness.

// claudeUsageReadOutcome is the closed result of asking the owner to read.
type claudeUsageReadOutcome string

const (
	claudeUsageReadAccepted   claudeUsageReadOutcome = "accepted"
	claudeUsageReadJoined     claudeUsageReadOutcome = "joined"
	claudeUsageReadNotDue     claudeUsageReadOutcome = "not_due"
	claudeUsageReadSuppressed claudeUsageReadOutcome = "suppressed"
	claudeUsageReadIneligible claudeUsageReadOutcome = "ineligible"
)

// claudeUsageTracked reports whether the account owns a usage connection that
// the refresh paths must route to the usage-read owner instead of the ChatGPT
// quota lane.
func claudeUsageTracked(a domain.Account) bool {
	return a.ManagedClaudeAccount() && a.Subscription.ClaudeUsage != nil && a.Subscription.ClaudeUsage.Linked()
}

// acceptClaudeUsageRead marks the account's single in-flight read. A manual
// refresh ignores the four-minute cadence but never an active backoff, and every
// trigger joins a read that is already in flight.
func (s *Service) acceptClaudeUsageRead(tx *store.Tx, r store.Record, a domain.Account, request domain.ID, actor domain.Principal, now time.Time, manual bool) (claudeUsageReadOutcome, error) {
	if !claudeUsageTracked(a) || a.Connection == nil || a.Removal != nil || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired {
		return claudeUsageReadIneligible, nil
	}
	usage := a.Subscription.ClaudeUsage
	if usage.Read.Active() {
		return claudeUsageReadJoined, nil
	}
	if !usage.Readable() {
		return claudeUsageReadIneligible, nil
	}
	if usage.SuppressedAt(now) {
		return claudeUsageReadSuppressed, nil
	}
	if !manual && !usage.DueAt(now) {
		return claudeUsageReadNotDue, nil
	}
	if subscriptionActorValid(tx, actor) != nil {
		return claudeUsageReadIneligible, nil
	}
	started := now
	usage.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadFetching, ID: request, Epoch: s.subscriptionServerEpoch(), StartedAt: &started, ConnectionID: a.Connection.ID, Generation: usage.Generation, Actor: actor, LastAttemptAt: &started}
	if err := putClaudeUsage(tx, r, a); err != nil {
		return claudeUsageReadIneligible, err
	}
	return claudeUsageReadAccepted, nil
}

// queueDueClaudeUsage accepts the automatic reads whose cadence has elapsed.
// The 30-second foreground trigger and this maintenance pass share the rule:
// nothing is read before four minutes after the previous attempt.
func (s *Service) queueDueClaudeUsage(ctx context.Context, now time.Time) error {
	var due []domain.ID
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		records, err := all(tx, domain.AccountKind)
		if err != nil {
			return err
		}
		for _, r := range records {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return err
			}
			if claudeUsageTracked(a) && a.Connection != nil && a.Subscription.ClaudeUsage.DueAt(now) {
				due = append(due, r.ID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.acceptDueClaudeUsage(ctx, due, now)
}

// acceptDueClaudeUsage accepts the reads of accounts a scan found due. Each is
// rechecked inside its write transaction, so a stale candidate list is harmless.
func (s *Service) acceptDueClaudeUsage(ctx context.Context, due []domain.ID, now time.Time) error {
	if len(due) == 0 {
		return nil
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	for _, id := range due {
		_, err := s.Store.Mutate(ctx, domain.NewID(), "claude.usage.maintenance", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
			r, a, err := accountFromTx(tx, id, 0)
			if err != nil {
				return nil, err
			}
			// Recheck inside the write transaction: a concurrent owner may have
			// accepted the read already.
			outcome, err := s.acceptClaudeUsageRead(tx, r, a, domain.NewID(), domain.Principal{Type: domain.OwnerDevice}, now, false)
			if err != nil {
				return nil, err
			}
			if outcome != claudeUsageReadAccepted {
				return nil, errNoDueSubscriptionQuota
			}
			return struct{}{}, nil
		})
		if err != nil && !errors.Is(err, errNoDueSubscriptionQuota) {
			return err
		}
	}
	return nil
}

// claudeUsageReadResult is what one read reports to its finish step.
type claudeUsageReadResult struct {
	observation *domain.ClaudeLimitObservation
	failure     domain.ClaudeLimitFailure
	retryAfter  time.Duration
	// credential is the claude-usage reference the read ended with. It differs
	// from the one it started with only after a committed rotation.
	credential domain.ID
	reconnect  bool
	// cleanup is false when the profile's native storage may still hold
	// credential bytes the read could not remove.
	cleanup bool
}

// claudeUsageReadAuthority is the authority a read started under.
type claudeUsageReadAuthority struct {
	read       domain.ClaudeLimitRead
	generation domain.ID
	credential domain.ID
	identity   string
}

// runClaudeUsageRead owns one accepted read from claim to publication.
func (s *Service) runClaudeUsageRead(parent context.Context, id domain.ID) {
	var authority claudeUsageReadAuthority
	var bundle subscription.ClaudeUsageBundle
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	var run *claudeUsageRun
	registered := false
	err = s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
			return subscriptionDenied()
		}
		usage := a.Subscription.ClaudeUsage
		// The marker is captured before the authority is judged, so a read whose
		// actor or connection was revoked can still be settled by its identifier.
		if usage.Read.Active() {
			authority.read = usage.Read
		}
		if !claudeUsageTracked(a) || a.Connection == nil || a.Removal != nil {
			return subscriptionDenied()
		}
		if !usage.Read.Active() || usage.Read.Epoch != s.subscriptionServerEpoch() || usage.Read.ConnectionID != a.Connection.ID || usage.Read.Generation != usage.Generation || subscriptionActorValid(tx, usage.Read.Actor) != nil {
			return subscriptionDenied()
		}
		authority = claudeUsageReadAuthority{read: usage.Read, generation: usage.Generation, credential: usage.CredentialID, identity: usage.Identity.Commitment}
		return nil
	})
	var openErr error
	if err == nil {
		// The run is registered before the gate is released and before the token
		// is opened, so a disconnect, logout or deletion that revokes the
		// connection afterwards always finds this read and cancels it.
		if run, registered = s.registerClaudeUsageRun(id, cancel); registered {
			bundle, openErr = s.openClaudeUsageCredential(parent, id, authority.generation, authority.credential)
		}
	}
	unlock()
	if err != nil || !registered {
		// The authority changed or another run owns the account. Nothing was sent.
		s.discardClaudeUsageRead(id, authority.read)
		return
	}
	defer s.finishClaudeUsageRun(id, run)
	defer bundle.Clear()
	s.logger.InfoContext(ctx, "claude_usage_read_started", "account_id", id, "read_id", authority.read.ID)
	result := claudeUsageReadResult{credential: authority.credential, cleanup: true}
	if openErr != nil {
		result.failure, result.reconnect = domain.ClaudeLimitFailureCredentials, true
	} else {
		result = s.fetchClaudeUsage(ctx, id, authority, &bundle)
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishClaudeUsageRead(bounded, id, authority, result); err != nil {
		s.logger.WarnContext(bounded, "claude_usage_read_publication_refused", "account_id", id, "read_id", authority.read.ID, "code", domain.SafeError(err).Code)
	}
}

// discardClaudeUsageRead idles a marker whose read never sent anything.
func (s *Service) discardClaudeUsageRead(id domain.ID, read domain.ClaudeLimitRead) {
	if read.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return
	}
	defer unlock()
	_, _ = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.read.discard", struct{ Account, Read domain.ID }{id, read.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ClaudeUsage == nil || a.Subscription.ClaudeUsage.Read.ID != read.ID {
			return struct{}{}, nil
		}
		a.Subscription.ClaudeUsage.Read.Phase, a.Subscription.ClaudeUsage.Read.ID = "", ""
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
}

// fetchClaudeUsage performs the read: an optional supervised refresh-login
// before the access token expires, the one authenticated GET and, after a
// definite 401, at most one refresh and one more GET.
func (s *Service) fetchClaudeUsage(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority, bundle *subscription.ClaudeUsageBundle) claudeUsageReadResult {
	result := claudeUsageReadResult{credential: authority.credential, cleanup: true}
	refreshed := false
	refresh := func() bool {
		next, credential, refreshResult := s.refreshClaudeUsage(ctx, id, authority, result.credential, bundle)
		result.cleanup = result.cleanup && refreshResult.cleanup
		if refreshResult.failure != domain.ClaudeLimitFailureNone {
			result.failure, result.reconnect = refreshResult.failure, refreshResult.reconnect
			return false
		}
		bundle.Clear()
		*bundle = next
		result.credential = credential
		refreshed = true
		return true
	}
	if bundle.Expiring(time.Now().UTC(), domain.ClaudeLimitRefreshMargin) && !refresh() {
		return result
	}
	observation, status, retryAfter, failure := s.getClaudeUsage(ctx, bundle.AccessToken)
	if status == http.StatusUnauthorized && !refreshed {
		// Exactly one serialized refresh and one more GET per read.
		if !refresh() {
			return result
		}
		observation, status, retryAfter, failure = s.getClaudeUsage(ctx, bundle.AccessToken)
	}
	switch {
	case failure != domain.ClaudeLimitFailureNone:
		result.failure, result.retryAfter = failure, retryAfter
		if status == http.StatusUnauthorized {
			result.reconnect = true
		}
	default:
		result.observation = &observation
	}
	return result
}

// getClaudeUsage issues the one fixed request. It follows no redirect, uses the
// configured server network route with no direct fallback and bounds time and
// size. It returns a validated observation or one closed failure.
func (s *Service) getClaudeUsage(ctx context.Context, access []byte) (domain.ClaudeLimitObservation, int, time.Duration, domain.ClaudeLimitFailure) {
	client, release := s.claudeUsageClient()
	defer release()
	requestContext, cancel := context.WithTimeout(ctx, domain.ClaudeLimitRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, domain.ClaudeLimitEndpoint, nil)
	if err != nil {
		return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureNetwork
	}
	request.Header.Set("Authorization", "Bearer "+string(access))
	request.Header.Set("anthropic-beta", domain.ClaudeLimitBeta)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "DeliDev/"+rpc.Version)
	response, err := client.Do(request)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded):
			return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureTimeout
		case ctx.Err() != nil:
			return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureInterrupted
		case domain.SafeError(err).Code == domain.Unsupported || domain.SafeError(err).Code == domain.PermissionDenied:
			return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureRoute
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureTimeout
		}
		return domain.ClaudeLimitObservation{}, 0, 0, domain.ClaudeLimitFailureNetwork
	}
	defer response.Body.Close()
	status := response.StatusCode
	switch {
	case status >= 300 && status < 400:
		return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureRedirect
	case status == http.StatusUnauthorized:
		return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureUnauthorized
	case status == http.StatusForbidden:
		return domain.ClaudeLimitObservation{}, status, domain.ParseClaudeRetryAfter(response.Header.Get("Retry-After"), time.Now()), domain.ClaudeLimitFailureForbidden
	case status == http.StatusTooManyRequests:
		return domain.ClaudeLimitObservation{}, status, domain.ParseClaudeRetryAfter(response.Header.Get("Retry-After"), time.Now()), domain.ClaudeLimitFailureRateLimited
	case status < 200 || status > 299:
		return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureServer
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, domain.ClaudeLimitMaxResponse+1))
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureTimeout
		}
		return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureMalformed
	}
	observation, err := domain.ParseClaudeLimit(body, time.Now().UTC())
	clear(body)
	if err != nil {
		return domain.ClaudeLimitObservation{}, status, 0, domain.ClaudeLimitFailureOf(err)
	}
	return observation, status, 0, domain.ClaudeLimitFailureNone
}

// claudeUsageClient returns the outbound client of the read and a function that
// releases its idle connections. Redirects are never followed.
func (s *Service) claudeUsageClient() (*http.Client, func()) {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if s.claudeUsageTransport != nil {
		return &http.Client{Transport: s.claudeUsageTransport, Timeout: domain.ClaudeLimitRequestTimeout, CheckRedirect: noRedirect}, func() {}
	}
	base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: domain.ClaudeLimitRequestTimeout}).DialContext, TLSHandshakeTimeout: domain.ClaudeLimitRequestTimeout, ResponseHeaderTimeout: domain.ClaudeLimitRequestTimeout, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &http.Client{Transport: &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, Timeout: domain.ClaudeLimitRequestTimeout, CheckRedirect: noRedirect}, base.CloseIdleConnections
}

// finishClaudeUsageRead publishes one read. It rechecks the connection, usage
// generation, actor, credential and server epoch the read started under; a
// changed authority discards the result. Only a validated nonempty observation
// advances the observation time.
func (s *Service) finishClaudeUsageRead(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority, result claudeUsageReadResult) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.read.finish", struct{ Account, Read domain.ID }{id, authority.read.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageTracked(a) || a.Connection == nil {
			return nil, subscriptionDenied()
		}
		usage := a.Subscription.ClaudeUsage
		read := usage.Read
		if !read.Active() || read.ID != authority.read.ID || read.Epoch != s.subscriptionServerEpoch() {
			s.logger.WarnContext(ctx, "claude_usage_read_discarded", "account_id", id, "read_id", authority.read.ID, "failure", domain.ClaudeLimitFailureAuthorityChanged)
			return nil, subscriptionDenied()
		}
		now := time.Now().UTC()
		// A changed connection, generation, credential or revoked actor discards the
		// result, but the matching durable marker is still settled: an unsettled
		// marker would keep the account reading and refuse every later refresh.
		authorityChanged := read.ConnectionID != a.Connection.ID || usage.Generation != authority.generation || usage.CredentialID != result.credential || subscriptionActorValid(tx, read.Actor) != nil
		switch {
		case authorityChanged:
			s.logger.WarnContext(ctx, "claude_usage_read_discarded", "account_id", id, "read_id", authority.read.ID, "failure", domain.ClaudeLimitFailureAuthorityChanged)
		case result.observation != nil:
			observation := *result.observation
			usage.RecordSuccess(observation)
		default:
			usage.RecordFailure(result.failure, now, result.retryAfter)
			if result.reconnect {
				usage.ReconnectRequired = true
			}
		}
		usage.Read.Phase, usage.Read.ID, usage.Read.Epoch, usage.Read.StartedAt = "", "", "", nil
		usage.Read.ConnectionID, usage.Read.Generation, usage.Read.Actor = "", "", domain.Principal{}
		if !result.reconnect && result.failure != domain.ClaudeLimitFailureRefreshUncertain {
			usage.Read.RefreshClaimed = false
		}
		if !result.cleanup {
			usage.CleanupPending = true
		}
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err == nil {
		s.logger.InfoContext(ctx, "claude_usage_read_finished", "account_id", id, "read_id", authority.read.ID, "result", claudeUsageResultName(result), "failure", result.failure, "backoff_ms", result.retryAfter.Milliseconds(), "cleanup_confirmed", result.cleanup)
	}
	return err
}

func claudeUsageResultName(result claudeUsageReadResult) string {
	if result.observation != nil {
		return "observed"
	}
	return "failed"
}

// refreshClaudeUsage runs at most one serialized, supervised native
// refresh-login in the account's exclusive usage profile. The refresh token,
// scopes and client identifier reach only the child's environment. The child is
// joined, the identity must be unchanged, the rotated bundle is captured and
// committed under a new immutable reference, and the profile's native storage
// is emptied again. A refresh whose rotation is ambiguous is never repeated.
func (s *Service) refreshClaudeUsage(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority, credential domain.ID, old *subscription.ClaudeUsageBundle) (subscription.ClaudeUsageBundle, domain.ID, claudeUsageReadResult) {
	fail := func(failure domain.ClaudeLimitFailure, cleanup bool) (subscription.ClaudeUsageBundle, domain.ID, claudeUsageReadResult) {
		return subscription.ClaudeUsageBundle{}, credential, claudeUsageReadResult{failure: failure, reconnect: failure.ReconnectRequired() || failure == domain.ClaudeLimitFailureRefreshRejected, cleanup: cleanup}
	}
	// The durable claim precedes the child, so a restart never repeats it.
	if err := s.claimClaudeUsageRefresh(ctx, id, authority); err != nil {
		return fail(domain.ClaudeLimitFailureAuthorityChanged, true)
	}
	opener := s.claudeUsageOpen
	if opener == nil {
		opener = openClaudeUsageNative
	}
	native, err := opener(ctx, s.scopeFor(id), authority.read.ID, s.logger)
	if err != nil {
		s.releaseClaudeUsageRefreshClaim(id, authority)
		if claudeUsageLeaseLeaked(err) {
			// The failed open could not release the profile lease, so the read ends
			// with a durable cleanup obligation instead of a leaked lock.
			return fail(domain.ClaudeLimitFailureCleanupUncertain, false)
		}
		return fail(domain.ClaudeLimitFailureRefreshUnsupported, true)
	}
	next, rotated, result := s.refreshClaudeUsageNative(ctx, id, authority, credential, old, native)
	if err := native.Close(); err != nil {
		// The exclusive profile lease may stay locked, so later refreshes,
		// reconnects and disconnects would fail against it. The read settles with a
		// durable cleanup obligation instead of reporting the profile released.
		s.logger.WarnContext(ctx, "claude_usage_profile_release_failed", "account_id", id, "read_id", authority.read.ID, "code", domain.SafeError(err).Code)
		result.cleanup = false
	}
	return next, rotated, result
}

// refreshClaudeUsageNative runs the refresh-login in the held native profile.
func (s *Service) refreshClaudeUsageNative(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority, credential domain.ID, old *subscription.ClaudeUsageBundle, native claudeUsageNative) (subscription.ClaudeUsageBundle, domain.ID, claudeUsageReadResult) {
	fail := func(failure domain.ClaudeLimitFailure, cleanup bool) (subscription.ClaudeUsageBundle, domain.ID, claudeUsageReadResult) {
		return subscription.ClaudeUsageBundle{}, credential, claudeUsageReadResult{failure: failure, reconnect: failure.ReconnectRequired() || failure == domain.ClaudeLimitFailureRefreshRejected, cleanup: cleanup}
	}
	storage := native.Storage()
	if present, err := storage.Present(ctx); err != nil || present {
		s.releaseClaudeUsageRefreshClaim(id, authority)
		// Unexpected native storage in the exclusive usage profile is never adopted.
		// The connection is fenced with a durable cleanup obligation, so every later
		// trigger stops repeating this check until the storage is removed.
		return fail(domain.ClaudeLimitFailureProfileUnsafe, false)
	}
	process, err := native.StartRefresh(ctx, claude.UsageRefreshInput{RefreshToken: old.RefreshToken, Scopes: old.Scopes, ClientID: old.ClientID})
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			// The child started and its descendants are not proven stopped, so the
			// exchange may have happened: keep the claim and require a reconnect.
			return fail(domain.ClaudeLimitFailureRefreshUncertain, false)
		}
		s.releaseClaudeUsageRefreshClaim(id, authority)
		return fail(domain.ClaudeLimitFailureRefreshUnsupported, true)
	}
	waitErr := func() error {
		select {
		case <-process.Done():
			return process.Wait()
		case <-ctx.Done():
			return ctx.Err()
		}
	}()
	// An interactive authorization address in the output means this native build
	// cannot renew non-interactively; read that before the child is closed.
	_, progressErr := process.Progress()
	if err := process.Close(); err != nil {
		// The child may still hold or have rotated the credential.
		_ = removeClaudeUsageStorage(ctx, storage)
		return fail(domain.ClaudeLimitFailureRefreshUncertain, false)
	}
	cleanup := true
	discard := func() {
		if removeClaudeUsageStorage(ctx, storage) != nil {
			cleanup = false
		}
	}
	if waitErr != nil {
		discard()
		if ctx.Err() != nil {
			return fail(domain.ClaudeLimitFailureRefreshUncertain, cleanup)
		}
		if domain.ClaudeLimitFailureOf(progressErr) == domain.ClaudeLimitFailureRefreshUnsupported {
			return fail(domain.ClaudeLimitFailureRefreshUnsupported, cleanup)
		}
		return fail(domain.ClaudeLimitFailureRefreshRejected, cleanup)
	}
	identity, err := native.Identity(ctx)
	if err != nil || s.claudeLimitIdentityCommitment(id, identity) != authority.identity {
		discard()
		return fail(domain.ClaudeLimitFailureIdentityChanged, cleanup)
	}
	// Email and organization identifier are the stable identity; the plan and the
	// organization display name may legitimately change and are published with
	// the rotation.
	display := &domain.ClaudeLimitIdentity{Email: identity.Email, Organization: identity.Organization, Plan: identity.Plan, Commitment: authority.identity}
	if display.Validate() != nil {
		display = nil
	}
	raw, err := storage.Read(ctx)
	if err != nil {
		discard()
		return fail(domain.ClaudeLimitFailureRefreshUncertain, cleanup)
	}
	rotated, err := subscription.ParseClaudeUsageNative(raw, time.Now().UTC())
	clear(raw)
	if err != nil {
		discard()
		return fail(domain.ClaudeLimitFailureRefreshUncertain, cleanup)
	}
	if subscription.SameClaudeUsageTokens(*old, rotated) {
		// The native child did not rotate anything: renewal is unsupported.
		rotated.Clear()
		discard()
		return fail(domain.ClaudeLimitFailureRefreshUnsupported, cleanup)
	}
	next, err := s.commitClaudeUsageRotation(ctx, id, authority, credential, rotated, display)
	if err != nil {
		rotated.Clear()
		discard()
		return fail(domain.ClaudeLimitFailureRefreshUncertain, cleanup)
	}
	discard()
	return rotated, next, claudeUsageReadResult{credential: next, cleanup: cleanup}
}

// removeClaudeUsageStorage empties the native storage on a bounded context that
// survives the read's own cancellation.
func removeClaudeUsageStorage(ctx context.Context, storage claudeUsageStorage) error {
	bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	return storage.Remove(bounded)
}

func (s *Service) claimClaudeUsageRefresh(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.refresh.claim", struct{ Account, Read domain.ID }{id, authority.read.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageTracked(a) {
			return nil, subscriptionDenied()
		}
		usage := a.Subscription.ClaudeUsage
		if usage.Read.ID != authority.read.ID || usage.Read.Phase != domain.ClaudeLimitReadFetching && usage.Read.Phase != domain.ClaudeLimitReadRefreshing || usage.Read.RefreshClaimed || usage.Generation != authority.generation {
			return nil, subscriptionDenied()
		}
		usage.Read.Phase, usage.Read.RefreshClaimed = domain.ClaudeLimitReadRefreshing, true
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	return err
}

// releaseClaudeUsageRefreshClaim withdraws a claim whose child provably never
// started, so a later read may try again.
func (s *Service) releaseClaudeUsageRefreshClaim(id domain.ID, authority claudeUsageReadAuthority) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return
	}
	defer unlock()
	_, _ = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.refresh.release", struct{ Account, Read domain.ID }{id, authority.read.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageTracked(a) || a.Subscription.ClaudeUsage.Read.ID != authority.read.ID {
			return struct{}{}, nil
		}
		usage := a.Subscription.ClaudeUsage
		usage.Read.Phase, usage.Read.RefreshClaimed = domain.ClaudeLimitReadFetching, false
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
}

// commitClaudeUsageRotation stages the rotated bundle under a new immutable
// reference and switches the connection to it. The previous reference becomes a
// durable removal obligation only after the switch commits.
func (s *Service) commitClaudeUsageRotation(ctx context.Context, id domain.ID, authority claudeUsageReadAuthority, old domain.ID, rotated subscription.ClaudeUsageBundle, display *domain.ClaudeLimitIdentity) (domain.ID, error) {
	bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return "", err
	}
	defer unlock()
	next := domain.NewID()
	envelope, err := subscription.SealClaudeUsage(s.Identity.ServerID, id, authority.generation, next, rotated, time.Now().UTC())
	if err != nil {
		return "", err
	}
	defer clear(envelope)
	if err := s.stageClaudeUsageCredential(bounded, id, next, envelope); err != nil {
		// A write that failed without an acknowledgment may still have stored the
		// reference, so its identifier becomes a durable removal obligation.
		_ = s.recordClaudeUsageRetirement(bounded, id, next)
		return "", err
	}
	_, err = s.Store.Mutate(bounded, domain.NewID(), "claude.usage.refresh.commit", struct{ Account, Read domain.ID }{id, authority.read.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageTracked(a) {
			return nil, subscriptionDenied()
		}
		usage := a.Subscription.ClaudeUsage
		if usage.Read.ID != authority.read.ID || usage.CredentialID != old || usage.Generation != authority.generation {
			return nil, subscriptionDenied()
		}
		usage.RetiredCredentials = append(usage.RetiredCredentials, old)
		usage.RetiredCredentials = slices.Compact(slices.Sorted(slices.Values(usage.RetiredCredentials)))
		usage.CredentialID = next
		if display != nil && usage.Identity != nil && usage.Identity.Commitment == display.Commitment {
			usage.Identity = display
		}
		usage.Read.Phase, usage.Read.RefreshClaimed = domain.ClaudeLimitReadFetching, false
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err != nil {
		if published, e := s.claudeUsageStagedPublished(id, "", next); e == nil && published {
			// The commit happened although its acknowledgment was lost.
			return next, nil
		} else if e != nil {
			// Unprovable either way: nothing is deleted.
			return "", err
		}
		// The switch did not commit, so the staged reference is unused.
		if deleteErr := s.deleteClaudeUsageReference(bounded, id, next); deleteErr != nil {
			// Losing the identifier would retain the OAuth credential until the whole
			// account is removed, so the deletion becomes a durable obligation.
			s.logger.WarnContext(bounded, "claude_usage_staging_cleanup_pending", "account_id", id, "code", domain.SafeError(deleteErr).Code)
			_ = s.recordClaudeUsageRetirement(bounded, id, next)
		}
		return "", err
	}
	s.logger.InfoContext(bounded, "claude_usage_refresh_rotated", "account_id", id, "read_id", authority.read.ID)
	return next, nil
}

// requestClaudeUsageObservation serves an explicit row or Manage refresh of a
// Claude account. It joins the usage-read owner: the request is checked against
// the confirmed connection and usage generation, coalesces with an in-flight
// read and cannot bypass an active provider backoff.
func (s *Service) requestClaudeUsageObservation(tx *store.Tx, r store.Record, a domain.Account, op domain.SubscriptionObservationOperation, confirmed bool) (any, error) {
	if confirmed || !claudeUsageTracked(a) || a.Connection == nil || op.ConnectionID != a.Connection.ID || op.Generation != a.Subscription.ClaudeUsage.Generation {
		return nil, domain.Fail(domain.Conflict, "The confirmed account generation changed.", "Read the current account and confirm the original operation again before sending.")
	}
	outcome, err := s.acceptClaudeUsageRead(tx, r, a, op.ID, op.Actor, time.Now().UTC(), true)
	if err != nil {
		return nil, err
	}
	switch outcome {
	case claudeUsageReadSuppressed:
		return nil, domain.Fail(domain.ResourceExhausted, "Claude usage reads are paused by the provider.", "Wait for the provider's retry time; a manual refresh cannot bypass it.")
	case claudeUsageReadIneligible:
		return nil, subscriptionDenied()
	}
	return accountReceipt{ID: r.ID}, nil
}
