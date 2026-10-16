// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestClaudeUsageFetchNormalizesWindowsWithExactRequestAndGenuineTime(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	before := time.Now().UTC().Add(-time.Second)
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatalf("outcome %s", outcome)
	}
	if f.transport.count() != 1 {
		t.Fatalf("expected exactly one authenticated GET, saw %d", f.transport.count())
	}
	request := f.transport.requests[0]
	if request.Method != http.MethodGet || request.URL.String() != "https://api.anthropic.com/api/oauth/usage" || request.Body != nil && request.ContentLength != 0 {
		t.Fatalf("request %s %s", request.Method, request.URL)
	}
	if f.transport.tokens[0] != usageFixtureAccess || request.Header.Get("anthropic-beta") != "oauth-2025-04-20" || request.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(request.Header.Get("User-Agent"), "DeliDev/") {
		t.Fatalf("headers %v", request.Header)
	}
	if deadline, ok := request.Context().Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		t.Fatal("the request has no five-second deadline")
	}
	usage := f.usage()
	if usage.LastSuccess == nil || usage.LastSuccess.Before(before) || len(usage.Windows) != 2 || usage.Windows[0].ID != domain.ClaudeLimitFiveHour || usage.Windows[0].Remaining != 0.75 || usage.Windows[1].ID != domain.ClaudeLimitSevenDay || usage.Windows[1].Remaining != 0.5 {
		t.Fatalf("usage %+v", usage)
	}
	if usage.Windows[0].ResetAt == nil || !usage.Windows[0].ResetAt.Equal(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) || usage.Failure != domain.ClaudeLimitFailureNone || usage.Read.Active() {
		t.Fatalf("usage bookkeeping %+v", usage)
	}
	_, a := f.record()
	if len(a.Quota) != 2 {
		t.Fatalf("projection %+v", a.Quota)
	}
	for _, w := range a.Quota {
		if w.Blocking || w.ComparisonGroup != "claude" || w.State != domain.Observed || !w.ObservedAt.Equal(*usage.LastSuccess) {
			t.Fatalf("window %+v", w)
		}
	}
	if a.Health != domain.AccountReady || a.ConfirmedExhausted {
		t.Fatal("a usage read changed execution readiness")
	}
}

func TestClaudeUsageFailuresPreserveAcceptedEvidenceAndTime(t *testing.T) {
	deadline := context.DeadlineExceeded
	scenarios := []struct {
		name     string
		response usageResponse
		err      error
		failure  domain.ClaudeLimitFailure
	}{
		{"timeout", usageResponse{}, deadline, domain.ClaudeLimitFailureTimeout},
		{"network", usageResponse{}, errors.New("dial"), domain.ClaudeLimitFailureNetwork},
		{"forbidden", usageResponse{status: 403, header: http.Header{"Retry-After": {"120"}}}, nil, domain.ClaudeLimitFailureForbidden},
		{"rate limited by date", usageResponse{status: 429, header: http.Header{"Retry-After": {time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)}}}, nil, domain.ClaudeLimitFailureRateLimited},
		{"empty", usageResponse{status: 200, body: `{}`}, nil, domain.ClaudeLimitFailureEmpty},
		{"null windows", usageResponse{status: 200, body: `{"five_hour":null,"seven_day":null}`}, nil, domain.ClaudeLimitFailureEmpty},
		{"malformed recognized window", usageResponse{status: 200, body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":"x"}}`}, nil, domain.ClaudeLimitFailureMalformed},
		{"out of range", usageResponse{status: 200, body: `{"five_hour":{"utilization":101}}`}, nil, domain.ClaudeLimitFailureMalformed},
		{"truncated", usageResponse{status: 200, body: `{"five_hour":{"utilization":1`}, nil, domain.ClaudeLimitFailureMalformed},
		{"oversized", usageResponse{status: 200, body: `{"five_hour":{"utilization":1},"x":"` + strings.Repeat("a", 1<<20) + `"}`}, nil, domain.ClaudeLimitFailureOversized},
		{"redirect", usageResponse{status: 302, header: http.Header{"Location": {"https://example.invalid/usage"}}}, nil, domain.ClaudeLimitFailureRedirect},
		{"server error", usageResponse{status: 503}, nil, domain.ClaudeLimitFailureServer},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			f := newUsageFixture(t)
			f.link()
			f.refreshOnce(true)
			accepted := *f.usage()
			if accepted.LastSuccess == nil || len(accepted.Windows) != 2 {
				t.Fatal("seed read failed")
			}
			f.transport.mu.Lock()
			f.transport.responses, f.transport.err = []usageResponse{scenario.response}, scenario.err
			f.transport.mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
				t.Fatalf("outcome %s", outcome)
			}
			usage := f.usage()
			if usage.Failure != scenario.failure || usage.FailureAt == nil {
				t.Fatalf("failure %q at %v", usage.Failure, usage.FailureAt)
			}
			if !usage.LastSuccess.Equal(*accepted.LastSuccess) || len(usage.Windows) != 2 || usage.Windows[0].Remaining != 0.75 || usage.Windows[1].Remaining != 0.5 {
				t.Fatalf("accepted evidence changed: %+v", usage)
			}
			if _, a := f.record(); len(a.Quota) != 2 || !a.Quota[0].ObservedAt.Equal(*accepted.LastSuccess) || a.Health != domain.AccountReady {
				t.Fatalf("projection after failure %+v", a.Quota)
			}
			backoff := scenario.failure == domain.ClaudeLimitFailureForbidden || scenario.failure == domain.ClaudeLimitFailureRateLimited
			if backoff != (usage.BackoffUntil != nil) {
				t.Fatalf("backoff = %v for %s", usage.BackoffUntil, scenario.failure)
			}
			if usage.ReconnectRequired {
				t.Fatal("a transient failure demanded a reconnect")
			}
		})
	}
}

func TestClaudeUsageRetryAfterSecondsHonorsDefaultAndCap(t *testing.T) {
	for _, tc := range []struct {
		header string
		min    time.Duration
		max    time.Duration
	}{
		{"90", 85 * time.Second, 95 * time.Second},
		{"", 4*time.Minute + 55*time.Second, 5*time.Minute + 5*time.Second},
		{"999999", 59*time.Minute + 55*time.Second, time.Hour + 5*time.Second},
	} {
		f := newUsageFixture(t)
		f.link()
		header := http.Header{}
		if tc.header != "" {
			header.Set("Retry-After", tc.header)
		}
		f.transport.responses = []usageResponse{{status: 429, header: header}}
		f.refreshOnce(true)
		usage := f.usage()
		if usage.BackoffUntil == nil {
			t.Fatal("no backoff recorded")
		}
		if remaining := time.Until(*usage.BackoffUntil); remaining < tc.min || remaining > tc.max {
			t.Fatalf("Retry-After %q -> %v", tc.header, remaining)
		}
	}
}

func TestClaudeUsageBackoffSuppressesEveryTriggerUntilPermitted(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.transport.responses = []usageResponse{{status: 429, header: http.Header{"Retry-After": {"300"}}}}
	f.refreshOnce(true)
	sent := f.transport.count()
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadSuppressed {
		t.Fatalf("manual refresh bypassed the suppression: %s", outcome)
	}
	// The row refresh reports the pause instead of sending.
	usage := f.usage()
	_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, f.observationRequest(usage)))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("row refresh during backoff: %v", err)
	}
	// Refresh all (explicit) and the maintenance pass skip the account.
	for _, trigger := range []pb.SubscriptionQuotaRefreshTrigger{pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_EXPLICIT, pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_FOREGROUND, pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_UNSPECIFIED} {
		response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID()), Trigger: trigger}))
		if err != nil || len(response.Msg.Accounts) != 0 {
			t.Fatalf("%v: %v %v", trigger, response, err)
		}
	}
	if err := f.service.queueDueClaudeUsage(f.owner(), time.Now().UTC().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if f.usage().Read.Active() || f.transport.count() != sent {
		t.Fatal("a trigger sent during the provider backoff")
	}
	// Once the deadline passes, explicit refresh may read again.
	f.mutateUsage(func(s *domain.ClaudeLimitState) {
		past := time.Now().Add(-time.Second)
		s.BackoffUntil = &past
	})
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatalf("outcome after backoff: %s", outcome)
	}
}

func (f *usageFixture) observationRequest(usage *domain.ClaudeLimitState) *pb.RequestSubscriptionObservationRequest {
	r, a := f.record()
	return &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA, ConnectionId: string(a.Connection.ID), GenerationId: string(usage.Generation)}
}

func TestClaudeUsageDueBoundaryAndForegroundTriggerCannotForceBeforeFourMinutes(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	base := time.Now().UTC()
	f.mutateUsage(func(s *domain.ClaudeLimitState) { s.Read.LastAttemptAt = &base })
	ctx := f.owner()
	if err := f.service.queueDueClaudeUsage(ctx, base.Add(4*time.Minute-time.Millisecond)); err != nil || f.usage().Read.Active() {
		t.Fatalf("not due at 3m59.999s: %v", err)
	}
	if err := f.service.queueDueClaudeUsage(ctx, base.Add(4*time.Minute)); err != nil || !f.usage().Read.Active() {
		t.Fatalf("due at 4m: %v", err)
	}
	f.service.runClaudeUsageRead(ctx, f.input.AccountID)
	// The 30-second foreground trigger (and an older client) is due-gated.
	first := f.usage().Read.LastAttemptAt
	for _, trigger := range []pb.SubscriptionQuotaRefreshTrigger{pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_FOREGROUND, pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_UNSPECIFIED} {
		response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID()), Trigger: trigger}))
		if err != nil || len(response.Msg.Accounts) != 0 || f.usage().Read.Active() {
			t.Fatalf("%v forced a Claude fetch before four minutes: %v %v", trigger, response, err)
		}
	}
	// An explicit Refresh all may fetch sooner.
	response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID()), Trigger: pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_EXPLICIT}))
	if err != nil || len(response.Msg.Accounts) != 1 || response.Msg.Accounts[0] != string(f.input.AccountID) || !f.usage().Read.Active() {
		t.Fatalf("explicit refresh all: %v %v", response, err)
	}
	if f.usage().Read.LastAttemptAt.Equal(*first) {
		t.Fatal("the explicit attempt did not restart the cadence")
	}
}

func TestClaudeUsageReadsAreSingleFlightAndCoalesce(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.transport.delay = make(chan struct{})
	if outcome := f.refreshOnceAsyncAccept(); outcome != claudeUsageReadAccepted {
		t.Fatalf("first accept %s", outcome)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); f.service.runClaudeUsageRead(f.owner(), f.input.AccountID) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.transport.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Overlapping triggers join the in-flight read and send nothing.
	for _, manual := range []bool{true, false} {
		if outcome := f.refreshOnceAsyncAccept(manual); outcome != claudeUsageReadJoined {
			t.Fatalf("overlap (manual=%v) = %s", manual, outcome)
		}
	}
	response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID()), Trigger: pb.SubscriptionQuotaRefreshTrigger_SUBSCRIPTION_QUOTA_REFRESH_TRIGGER_EXPLICIT}))
	if err != nil || len(response.Msg.Accounts) != 1 {
		t.Fatalf("joined account must still be acknowledged: %v %v", response, err)
	}
	close(f.transport.delay)
	wg.Wait()
	if f.transport.count() != 1 {
		t.Fatalf("expected one coalesced GET, saw %d", f.transport.count())
	}
}

// refreshOnceAsyncAccept accepts a read without running it.
func (f *usageFixture) refreshOnceAsyncAccept(manual ...bool) claudeUsageReadOutcome {
	f.t.Helper()
	isManual := len(manual) == 0 || manual[0]
	var outcome claudeUsageReadOutcome
	unlock, err := f.service.lockAccounts(f.owner())
	if err != nil {
		f.t.Fatal(err)
	}
	defer unlock()
	_, err = f.service.Store.Mutate(f.owner(), domain.NewID(), "fixture.claude-usage.accept", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		outcome, err = f.service.acceptClaudeUsageRead(tx, r, a, domain.NewID(), domain.Principal{Type: domain.OwnerDevice}, time.Now().UTC(), isManual)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return outcome
}

func TestClaudeUsageRowRefreshJoinsTheOwnerAndChecksGeneration(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	usage := f.usage()
	request := f.observationRequest(usage)
	response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || response.Msg.OperationId != request.Mutation.RequestId {
		t.Fatalf("row refresh: %v %v", response, err)
	}
	if !f.usage().Read.Active() {
		t.Fatal("the usage-read owner did not accept the row refresh")
	}
	replay, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("replay: %v %v", replay, err)
	}
	f.service.runClaudeUsageRead(f.owner(), f.input.AccountID)
	if f.transport.count() != 1 {
		t.Fatalf("GET count %d", f.transport.count())
	}
	stale := f.observationRequest(usage)
	stale.GenerationId = string(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, stale)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("a stale usage generation was accepted: %v", err)
	}
	_ = generation
}

func TestClaudeUsagePublicationRechecksAuthority(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.transport.delay = make(chan struct{})
	if f.refreshOnceAsyncAccept() != claudeUsageReadAccepted {
		t.Fatal("not accepted")
	}
	done := make(chan struct{})
	go func() { defer close(done); f.service.runClaudeUsageRead(f.owner(), f.input.AccountID) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.transport.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// The connection is replaced while the GET is in flight.
	f.mutateUsage(func(s *domain.ClaudeLimitState) { s.Generation = domain.NewID() })
	close(f.transport.delay)
	awaitServerFixture(t, done)
	usage := f.usage()
	if usage.LastSuccess != nil || len(usage.Windows) != 0 {
		t.Fatalf("a late result was published into a replaced generation: %+v", usage)
	}
}

func TestClaudeUsageReadNeverUsesNativeUsageOrCachedEvidence(t *testing.T) {
	// The only evidence source is the direct GET: a cached/native read cannot
	// refresh evidence because no such path exists. A read that cannot reach the
	// endpoint leaves the observation time untouched.
	f := newUsageFixture(t)
	f.link()
	f.transport.err = errors.New("offline")
	f.refreshOnce(true)
	if usage := f.usage(); usage.LastSuccess != nil || len(usage.Windows) != 0 || usage.Failure != domain.ClaudeLimitFailureNetwork {
		t.Fatalf("usage %+v", usage)
	}
}
