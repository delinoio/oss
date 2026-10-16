// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	usageRenewedAccess  = "sk-ant-oat01-UsageRenewedAccessTokenABCDEFGHIJKLMNOP0123456789_-"
	usageRenewedRefresh = "sk-ant-ort01-UsageRenewedRefreshTokenABCDEFGHIJKLMNOP0123456789_-"
	usageBody           = `{"five_hour":{"utilization":25,"resets_at":"2026-10-16T12:00:00Z"},"seven_day":{"utilization":50,"resets_at":"2026-10-20T12:00:00Z"},"unrelated":{"utilization":99}}`
)

// usageClock is the controllable wall clock of the usage-read owner.
type usageClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *usageClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *usageClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recordedUsageRequest is the secret-free-comparable copy of one provider GET.
type recordedUsageRequest struct {
	Method string
	URL    string
	Header http.Header
}

// usageTransport is the injected provider. It records every request and
// answers through handler, which receives the zero-based request number.
type usageTransport struct {
	mu       sync.Mutex
	requests []recordedUsageRequest
	handler  func(n int, req *http.Request) (*http.Response, error)
}

func (t *usageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	n := len(t.requests)
	t.requests = append(t.requests, recordedUsageRequest{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone()})
	handler := t.handler
	t.mu.Unlock()
	if handler == nil {
		return usageResponse(http.StatusOK, usageBody), nil
	}
	return handler(n, req)
}

func (t *usageTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.requests)
}

func (t *usageTransport) request(n int) recordedUsageRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requests[n]
}

func usageResponse(status int, body string, headers ...string) *http.Response {
	response := &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
	for i := 0; i+1 < len(headers); i += 2 {
		response.Header.Set(headers[i], headers[i+1])
	}
	return response
}

// fakeUsageRenewal is the injected native refresh-login. It reads no profile.
type fakeUsageRenewal struct {
	status     claude.UsageRefreshStatus
	runErr     error
	captureErr error
	email      string
	block      chan struct{}
	runs       atomic.Int32
	captures   atomic.Int32
	releases   atomic.Int32
	mu         sync.Mutex
	captured   []subscription.ClaudeUsageBundle
}

func (r *fakeUsageRenewal) Run(ctx context.Context) (claude.UsageRefreshStatus, error) {
	r.runs.Add(1)
	if r.block != nil {
		select {
		case <-ctx.Done():
			return claude.UsageRefreshUncertain, ctx.Err()
		case <-r.block:
		}
	}
	return r.status, r.runErr
}

func (r *fakeUsageRenewal) Capture(context.Context) (claudeUsageCapture, error) {
	r.captures.Add(1)
	if r.captureErr != nil {
		return claudeUsageCapture{}, r.captureErr
	}
	bundle := subscription.ClaudeUsageBundle{AccessToken: []byte(usageRenewedAccess), RefreshToken: []byte(usageRenewedRefresh), ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Scopes: []string{"user:inference", "user:profile"}, ClientID: claude.UsageClientID}
	r.mu.Lock()
	r.captured = append(r.captured, bundle)
	r.mu.Unlock()
	return claudeUsageCapture{Bundle: bundle, Identity: claude.UsageIdentity{Email: r.email, OrganizationID: usageFixtureOrgID, OrganizationName: usageFixtureOrgName, Plan: "max"}}, nil
}

func (r *fakeUsageRenewal) Release() error { r.releases.Add(1); return nil }

// zeroed reports whether every captured token buffer was overwritten.
func (r *fakeUsageRenewal) zeroed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, bundle := range r.captured {
		for _, b := range append(bytes.Clone(bundle.AccessToken), bundle.RefreshToken...) {
			if b != 0 {
				return false
			}
		}
	}
	return len(r.captured) != 0
}

type readFixture struct {
	*usageFixture
	clock      *usageClock
	provider   *usageTransport
	renewal    *fakeUsageRenewal
	renewOpens atomic.Int32
	renewSeen  atomic.Value
	cipher     []byte
}

// newReadFixture links a usage connection and installs the provider, the clock
// and the native renewal seams.
func newReadFixture(t *testing.T, native func(*fakeUsageNative)) *readFixture {
	t.Helper()
	f := &readFixture{usageFixture: newUsageFixture(t), provider: &usageTransport{}, clock: &usageClock{t: time.Now().UTC()}}
	if native != nil {
		native(f.usage)
	}
	f.renewal = &fakeUsageRenewal{status: claude.UsageRefreshCompleted, email: usageFixtureEmail}
	f.service.claudeUsageTransport = f.provider
	f.service.claudeUsageClock = f.clock.Now
	f.service.claudeUsageRenew = func(_ context.Context, _ nativeScope, _ domain.ID, bundle subscription.ClaudeUsageBundle, _ *slog.Logger) (claudeUsageRenewal, string, error) {
		f.renewOpens.Add(1)
		f.renewSeen.Store(string(bundle.RefreshToken))
		return f.renewal, domain.ClaudeProtocolVersion, nil
	}
	op, review := f.stage()
	if _, err := f.confirm(op, review.Identity.IdentityCommitment); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *readFixture) account() domain.ID { return f.input.AccountID }

// await joins the running read of the account.
func (f *readFixture) await() {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for f.service.claudeUsage.live(f.account()) {
		if time.Now().After(deadline) {
			f.t.Fatal("the usage read never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *readFixture) refreshRequest(requestID domain.ID) *pb.RequestSubscriptionObservationRequest {
	r, a := f.record()
	return &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(requestID), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}
}

func (f *readFixture) refresh() (*pb.RequestSubscriptionObservationResponse, error) {
	response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, f.refreshRequest(domain.NewID())))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// refreshAndWait is one explicit refresh followed by the join of its read.
func (f *readFixture) refreshAndWait() error {
	f.t.Helper()
	if _, err := f.refresh(); err != nil {
		return err
	}
	f.await()
	return nil
}

func (f *readFixture) refreshAll() *pb.RefreshAllSubscriptionQuotasResponse {
	f.t.Helper()
	response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())}))
	if err != nil {
		f.t.Fatal(err)
	}
	f.await()
	return response.Msg
}

func (f *readFixture) schedule() []domain.ID {
	f.t.Helper()
	admitted := f.service.scheduleClaudeUsageReads(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), domain.Principal{Type: domain.OwnerDevice}, claudeUsageTriggerMaintenance)
	f.await()
	return admitted
}

func (f *readFixture) windows() map[domain.ClaudeUsageWindowID]domain.ClaudeUsageWindowEvidence {
	result := map[domain.ClaudeUsageWindowID]domain.ClaudeUsageWindowEvidence{}
	for _, w := range f.section().Windows {
		result[w.ID] = w
	}
	return result
}

func connectCode(err error) connect.Code {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Code()
	}
	return 0
}

func TestClaudeUsageFetchSendsTheExactRequestAndPublishesTheObservation(t *testing.T) {
	f := newReadFixture(t, nil)
	_, before := f.record()
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	if f.provider.count() != 1 {
		t.Fatalf("provider requests = %d", f.provider.count())
	}
	got := f.provider.request(0)
	if got.Method != http.MethodGet || got.URL != "https://api.anthropic.com/api/oauth/usage" {
		t.Fatalf("request = %s %s", got.Method, got.URL)
	}
	for name, want := range map[string]string{"Authorization": "Bearer " + usageFixtureAccess, "Anthropic-Beta": "oauth-2025-04-20", "Content-Type": "application/json", "User-Agent": "DeliDev/0.1.0"} {
		if got.Header.Get(name) != want {
			t.Fatalf("header %s = %q, want %q", name, got.Header.Get(name), want)
		}
	}
	_, after := f.record()
	section := after.Subscription.ClaudeUsage
	if section.Refresh != domain.ClaudeUsageIdle || section.Failure != domain.ClaudeUsageFailureNone || section.LastSuccessAt == nil || !section.LastSuccessAt.Equal(f.clock.Now()) {
		t.Fatalf("section = %+v", section)
	}
	byID := f.windows()
	five, seven := byID[domain.ClaudeUsageFiveHour], byID[domain.ClaudeUsageSevenDay]
	if len(byID) != 2 || five.Remaining != 0.75 || seven.Remaining != 0.5 || five.ResetAt == nil || five.ResetAt.Format(time.RFC3339) != "2026-10-16T12:00:00Z" || seven.ResetAt.Format(time.RFC3339) != "2026-10-20T12:00:00Z" || !five.ObservedAt.Equal(f.clock.Now()) {
		t.Fatalf("windows = %+v", byID)
	}
	projected := map[string]domain.QuotaWindow{}
	for _, w := range after.Quota {
		projected[w.ID] = w
	}
	if len(projected) != 2 || projected["five_hour"].ComparisonGroup != "claude" || projected["five_hour"].Blocking || *projected["five_hour"].Remaining != 0.75 || *projected["seven_day"].Remaining != 0.5 {
		t.Fatalf("projected quota = %+v", after.Quota)
	}
	// The execution authority and its quota lane are untouched.
	if after.Subscription.Generation != before.Subscription.Generation || after.Connection.ID != before.Connection.ID || after.Health != before.Health {
		t.Fatal("a usage read changed the execution connection")
	}
}

func TestClaudeUsageFetchNormalizesBoundsAndNullWindows(t *testing.T) {
	f := newReadFixture(t, nil)
	// 0 and 100 percent; unrelated fields are ignored.
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		return usageResponse(http.StatusOK, `{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":100,"resets_at":"2026-10-20T12:00:00Z"},"extra_usage":{"utilization":3}}`), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	byID := f.windows()
	if byID[domain.ClaudeUsageFiveHour].Remaining != 1 || byID[domain.ClaudeUsageFiveHour].ResetAt != nil || byID[domain.ClaudeUsageSevenDay].Remaining != 0 {
		t.Fatalf("0/100 windows = %+v", byID)
	}
	first := *f.section().LastSuccessAt
	// A null window is unavailable, never zero, and keeps its earlier evidence
	// and observation time.
	f.clock.Advance(time.Minute)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		return usageResponse(http.StatusOK, `{"five_hour":{"utilization":10,"resets_at":null},"seven_day":null}`), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	byID = f.windows()
	if byID[domain.ClaudeUsageFiveHour].Remaining != 0.9 || !byID[domain.ClaudeUsageFiveHour].ObservedAt.Equal(f.clock.Now()) {
		t.Fatalf("five_hour = %+v", byID[domain.ClaudeUsageFiveHour])
	}
	if seven := byID[domain.ClaudeUsageSevenDay]; seven.Remaining != 0 || !seven.ObservedAt.Equal(first) {
		t.Fatalf("a null window was refreshed or zeroed: %+v", seven)
	}
}

func TestClaudeUsageUnusableAnswersKeepEvidenceAndTimestamp(t *testing.T) {
	f := newReadFixture(t, nil)
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	seeded := f.section()
	last := *seeded.LastSuccessAt
	seededWindows := f.windows()
	oversized := `{"five_hour":{"utilization":1},"pad":"` + strings.Repeat("x", domain.MaxClaudeUsageResponseBytes) + `"}`
	cases := []struct {
		name    string
		handler func(int, *http.Request) (*http.Response, error)
		want    domain.ClaudeUsageFailure
	}{
		{"timeout", func(_ int, r *http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }, domain.ClaudeUsageFailureUnavailable},
		{"server error", func(int, *http.Request) (*http.Response, error) { return usageResponse(500, "oops"), nil }, domain.ClaudeUsageFailureUnavailable},
		{"redirect", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(http.StatusFound, "", "Location", "https://evil.invalid/api/oauth/usage"), nil
		}, domain.ClaudeUsageFailureUnavailable},
		{"empty windows", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":null,"seven_day":{"resets_at":null}}`), nil
		}, domain.ClaudeUsageFailureEmpty},
		{"cached-only body without windows", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"cached":true}`), nil
		}, domain.ClaudeUsageFailureEmpty},
		{"malformed utilization", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":{"utilization":"NaN"}}`), nil
		}, domain.ClaudeUsageFailureMalformed},
		{"out of range", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":{"utilization":101}}`), nil
		}, domain.ClaudeUsageFailureMalformed},
		{"bad reset time", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":{"utilization":5,"resets_at":"tomorrow"}}`), nil
		}, domain.ClaudeUsageFailureMalformed},
		{"truncated", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":{"utilization":5`), nil
		}, domain.ClaudeUsageFailureMalformed},
		{"oversized", func(int, *http.Request) (*http.Response, error) { return usageResponse(200, oversized), nil }, domain.ClaudeUsageFailureMalformed},
		{"one valid and one malformed window fail atomically", func(int, *http.Request) (*http.Response, error) {
			return usageResponse(200, `{"five_hour":{"utilization":5},"seven_day":{"utilization":-1}}`), nil
		}, domain.ClaudeUsageFailureMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.clock.Advance(5 * time.Minute)
			f.provider.handler = c.handler
			before := f.provider.count()
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			if f.provider.count() != before+1 {
				t.Fatalf("provider requests = %d, want one more than %d", f.provider.count(), before)
			}
			section := f.section()
			if section.Failure != c.want || section.Refresh != domain.ClaudeUsageIdle || section.Connection != domain.ClaudeUsageLinked {
				t.Fatalf("section after %s = %+v", c.name, section)
			}
			if !section.LastSuccessAt.Equal(last) || len(f.windows()) != len(seededWindows) || f.windows()[domain.ClaudeUsageFiveHour].Remaining != 0.75 || !f.windows()[domain.ClaudeUsageFiveHour].ObservedAt.Equal(last) {
				t.Fatalf("a failed read changed the accepted evidence: %+v", section)
			}
			if section.BackoffUntil != nil {
				t.Fatalf("a non-throttling failure paused reads: %v", section.BackoffUntil)
			}
		})
	}
	// Only the redirect target was never requested.
	for i := 0; i < f.provider.count(); i++ {
		if strings.Contains(f.provider.request(i).URL, "evil.invalid") {
			t.Fatal("a redirect was followed")
		}
	}
}

func TestClaudeUsageThrottlingHonorsRetryAfterAndCannotBeBypassed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header string
		want   func(now time.Time) time.Duration
		fail   domain.ClaudeUsageFailure
	}{
		{"429 seconds", 429, "120", func(time.Time) time.Duration { return 2 * time.Minute }, domain.ClaudeUsageFailureRateLimited},
		{"403 http date", 403, "", func(time.Time) time.Duration { return 10 * time.Minute }, domain.ClaudeUsageFailureForbidden},
		{"429 default", 429, "", func(time.Time) time.Duration { return domain.ClaudeUsageDefaultBackoff }, domain.ClaudeUsageFailureRateLimited},
		{"429 cap", 429, "86400", func(time.Time) time.Duration { return domain.ClaudeUsageMaxBackoff }, domain.ClaudeUsageFailureRateLimited},
		{"403 garbage", 403, "soon", func(time.Time) time.Duration { return domain.ClaudeUsageDefaultBackoff }, domain.ClaudeUsageFailureForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = 1000 * time.Hour })
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			last := *f.section().LastSuccessAt
			f.clock.Advance(5 * time.Minute)
			header := c.header
			if c.name == "403 http date" {
				header = f.clock.Now().Add(10 * time.Minute).Format(http.TimeFormat)
			}
			f.provider.handler = func(int, *http.Request) (*http.Response, error) {
				if header == "" {
					return usageResponse(c.status, "slow down"), nil
				}
				return usageResponse(c.status, "slow down", "Retry-After", header), nil
			}
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			section := f.section()
			until := f.clock.Now().Add(c.want(f.clock.Now()))
			if section.Failure != c.fail || section.BackoffUntil == nil || section.BackoffUntil.Sub(until).Abs() > time.Second || !section.LastSuccessAt.Equal(last) || len(f.windows()) != 2 {
				t.Fatalf("section = %+v want backoff until %v", section, until)
			}
			sent := f.provider.count()
			// No trigger can send before the deadline: not Refresh all, not the
			// maintenance scan and not a manual refresh.
			f.clock.Advance(c.want(f.clock.Now()) - time.Second)
			f.refreshAll()
			f.schedule()
			if _, err := f.refresh(); err == nil || connectCode(err) == 0 {
				t.Fatalf("a manual refresh bypassed the suppression: %v", err)
			}
			if f.provider.count() != sent {
				t.Fatalf("a suppressed connection sent %d requests", f.provider.count()-sent)
			}
			if f.section().Refresh != domain.ClaudeUsageIdle {
				t.Fatal("a refused refresh claimed a read")
			}
			// At the deadline an explicit refresh may read again.
			f.clock.Advance(time.Second)
			f.provider.handler = nil
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			if f.provider.count() != sent+1 || f.section().Failure != domain.ClaudeUsageFailureNone || f.section().BackoffUntil != nil {
				t.Fatalf("reads did not resume: %+v", f.section())
			}
		})
	}
}

// An unavailable answer pauses reads only when it carries a usable provider
// deadline: a malformed Retry-After must not become the default throttling pause.
func TestClaudeUsageUnavailablePausesOnlyOnAUsableRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		pause  time.Duration
	}{
		{"seconds", "120", 2 * time.Minute},
		{"malformed", "soon", 0},
		{"absent", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = 1000 * time.Hour })
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			f.clock.Advance(5 * time.Minute)
			f.provider.handler = func(int, *http.Request) (*http.Response, error) {
				if c.header == "" {
					return usageResponse(503, "busy"), nil
				}
				return usageResponse(503, "busy", "Retry-After", c.header), nil
			}
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			section := f.section()
			if section.Failure != domain.ClaudeUsageFailureUnavailable {
				t.Fatalf("failure = %q", section.Failure)
			}
			if c.pause == 0 && section.BackoffUntil != nil {
				t.Fatalf("a malformed or missing Retry-After suppressed reads until %v", section.BackoffUntil)
			}
			if c.pause != 0 && (section.BackoffUntil == nil || section.BackoffUntil.Sub(f.clock.Now().Add(c.pause)).Abs() > time.Second) {
				t.Fatalf("a usable Retry-After was not honored: %v", section.BackoffUntil)
			}
		})
	}
}

func TestClaudeUsageScheduledReadsAreDueExactlyAtFourMinutes(t *testing.T) {
	f := newReadFixture(t, nil)
	// Maintenance reads a never-read connection at once, then waits four minutes.
	if admitted := f.schedule(); len(admitted) != 1 || f.provider.count() != 1 {
		t.Fatalf("first scheduled read: admitted %v requests %d", admitted, f.provider.count())
	}
	base := f.clock.Now()
	f.clock.Advance(4*time.Minute - time.Millisecond)
	if admitted := f.schedule(); len(admitted) != 0 || f.provider.count() != 1 {
		t.Fatalf("a read was due at 3m59.999s: %v %d", admitted, f.provider.count())
	}
	f.clock.Advance(time.Millisecond)
	if !f.clock.Now().Equal(base.Add(4 * time.Minute)) {
		t.Fatal("clock arithmetic")
	}
	if admitted := f.schedule(); len(admitted) != 1 || f.provider.count() != 2 {
		t.Fatalf("a read was not due at 4m: %v %d", admitted, f.provider.count())
	}
}

func TestClaudeUsageForegroundAndRefreshAllTriggersCannotForceAFetch(t *testing.T) {
	f := newReadFixture(t, nil)
	response := f.refreshAll()
	if f.provider.count() != 1 || len(response.Accounts) != 1 || response.Accounts[0] != string(f.account()) {
		t.Fatalf("the first Refresh all: requests %d accounts %v", f.provider.count(), response.Accounts)
	}
	// The automatic foreground cadence calls Refresh all every 30 seconds.
	for i := 1; i <= 7; i++ {
		f.clock.Advance(30 * time.Second)
		response := f.refreshAll()
		if f.provider.count() != 1 || len(response.Accounts) != 0 {
			t.Fatalf("tick %d forced a fetch: requests %d accounts %v", i, f.provider.count(), response.Accounts)
		}
	}
	f.clock.Advance(30 * time.Second)
	if response := f.refreshAll(); f.provider.count() != 2 || len(response.Accounts) != 1 {
		t.Fatalf("the due tick did not read: requests %d accounts %v", f.provider.count(), response.Accounts)
	}
	// An explicit refresh may read sooner than the due interval.
	if err := f.refreshAndWait(); err != nil || f.provider.count() != 3 {
		t.Fatalf("explicit refresh: %v requests %d", err, f.provider.count())
	}
}

func TestClaudeUsageOverlappingTriggersShareOneRead(t *testing.T) {
	f := newReadFixture(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-release
		return usageResponse(200, usageBody), nil
	}
	first, err := f.refresh()
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if f.section().Refresh != domain.ClaudeUsageReading {
		t.Fatalf("the claim is not durable: %+v", f.section())
	}
	// Row/Manage refresh, Refresh all and maintenance all coalesce.
	second, err := f.refresh()
	if err != nil || second.OperationId == first.OperationId {
		t.Fatalf("a coalesced refresh was refused: %v", err)
	}
	if _, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	if admitted := f.service.scheduleClaudeUsageReads(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), domain.Principal{Type: domain.OwnerDevice}, claudeUsageTriggerMaintenance); len(admitted) != 0 {
		t.Fatalf("maintenance started a second read: %v", admitted)
	}
	close(release)
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("overlapping triggers sent %d requests", f.provider.count())
	}
}

// A request that coalesced into a running read is validated and durably claimed
// like an admitted one, so retrying it exactly after the read finished replays
// instead of fetching again, and an unfenced stale request is refused while the
// read runs.
func TestClaudeUsageCoalescedRefreshIsClaimedAndReplaysAfterTheReadFinishes(t *testing.T) {
	f := newReadFixture(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-release
		return usageResponse(200, usageBody), nil
	}
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	stale := f.refreshRequest(domain.NewID())
	stale.GenerationId = string(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, stale)); connectCode(err) == 0 {
		t.Fatal("a stale generation was acknowledged by the busy shortcut")
	}
	coalesced := f.refreshRequest(domain.NewID())
	first, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, coalesced))
	if err != nil || first.Msg.Replayed {
		t.Fatalf("the coalesced refresh was not accepted: %v %+v", err, first)
	}
	close(release)
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("coalescing sent %d provider requests", f.provider.count())
	}
	retried, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, coalesced))
	if err != nil || !retried.Msg.Replayed || retried.Msg.OperationId != coalesced.Mutation.RequestId {
		t.Fatalf("the coalesced request did not replay: %v %+v", err, retried)
	}
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("the retried coalesced request started another provider read: %d", f.provider.count())
	}
}

// A durable non-idle phase without a registered owner is an orphaned claim, not
// a running read: an explicit refresh must not record a coalesced receipt for it
// (the request would replay forever without a read) and is admitted normally
// once the cleanup scanner fenced the claim.
func TestClaudeUsageRefreshIsNotCoalescedIntoAnOrphanedClaim(t *testing.T) {
	f := newReadFixture(t, nil)
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.usage.orphan", nil, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, f.account(), 0)
		if err != nil {
			return nil, err
		}
		if err := a.Subscription.ClaudeUsage.BeginRead(f.clock.Now()); err != nil {
			return nil, err
		}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	}); err != nil {
		t.Fatal(err)
	}
	if f.service.claudeUsage.live(f.account()) || f.section().Refresh != domain.ClaudeUsageReading {
		t.Fatalf("the orphaned claim was not prepared: %+v", f.section())
	}
	request := f.refreshRequest(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err == nil {
		t.Fatal("a refresh was acknowledged although no read owns the orphaned claim")
	}
	if f.provider.count() != 0 {
		t.Fatalf("the refused refresh sent %d provider requests", f.provider.count())
	}
	// The scanner fences the orphan; the same retained request then reads.
	if err := f.service.cleanupClaudeUsage(context.Background(), f.account()); err != nil {
		t.Fatal(err)
	}
	if f.section().Refresh != domain.ClaudeUsageIdle {
		t.Fatalf("the orphaned claim was not fenced: %+v", f.section())
	}
	retried := f.refreshRequest(domain.ID(request.Mutation.RequestId))
	response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, retried))
	if err != nil || response.Msg.Replayed {
		t.Fatalf("the retried request was not admitted as a new read: %v %+v", err, response)
	}
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("the retried request did not perform its read: %d", f.provider.count())
	}
}

// The Refresh all receipt carries the Claude usage reads of the request: a retry
// after a lost response lists the original accounts and never reads again, and a
// request whose admissions did not happen before the response was lost resumes
// them on retry.
func TestClaudeUsageRefreshAllReceiptIncludesTheClaudeReads(t *testing.T) {
	f := newReadFixture(t, nil)
	f.clock.Advance(5 * time.Minute)
	request := &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())}
	send := func() *pb.RefreshAllSubscriptionQuotasResponse {
		t.Helper()
		response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
		if err != nil {
			t.Fatal(err)
		}
		f.await()
		return response.Msg
	}
	first := send()
	if first.Replayed || len(first.Accounts) != 1 || first.Accounts[0] != string(f.account()) || f.provider.count() != 1 {
		t.Fatalf("first response = %+v requests %d", first, f.provider.count())
	}
	retried := send()
	if !retried.Replayed || len(retried.Accounts) != 1 || retried.Accounts[0] != string(f.account()) {
		t.Fatalf("the replay did not reproduce the original account list: %+v", retried)
	}
	if f.provider.count() != 1 {
		t.Fatalf("the replayed request read again: %d requests", f.provider.count())
	}

	// A request whose admissions were interrupted after its receipt committed
	// resumes them on retry: the receipt, not the response, owns the reads.
	f.clock.Advance(5 * time.Minute)
	resumed := &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())}
	f.service.stopping.Store(true)
	interrupted, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, resumed))
	f.service.stopping.Store(false)
	if err != nil || len(interrupted.Msg.Accounts) != 1 || f.provider.count() != 1 {
		t.Fatalf("the interrupted request = %v %+v requests %d", err, interrupted, f.provider.count())
	}
	again, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, resumed))
	if err != nil || !again.Msg.Replayed || len(again.Msg.Accounts) != 1 {
		t.Fatalf("the retry = %v %+v", err, again)
	}
	f.await()
	if f.provider.count() != 2 {
		t.Fatalf("the retry did not resume the receipt's read: %d requests", f.provider.count())
	}
	f.await()
	if _, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, resumed)); err != nil {
		t.Fatal(err)
	}
	f.await()
	if f.provider.count() != 2 {
		t.Fatalf("a third delivery read again: %d requests", f.provider.count())
	}
}

func TestClaudeUsageRefreshReceiptReplayNeverReadsAgain(t *testing.T) {
	f := newReadFixture(t, nil)
	request := f.refreshRequest(domain.NewID())
	for i := 0; i < 3; i++ {
		response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
		if err != nil {
			t.Fatal(err)
		}
		if response.Msg.OperationId != request.Mutation.RequestId || response.Msg.Replayed != (i > 0) {
			t.Fatalf("response %d = %+v", i, response.Msg)
		}
		f.await()
	}
	if f.provider.count() != 1 {
		t.Fatalf("a replayed refresh sent %d requests", f.provider.count())
	}
}

func TestClaudeUsageExplicitRefreshIsFencedByTheConfirmedAccountGeneration(t *testing.T) {
	f := newReadFixture(t, nil)
	stale := f.refreshRequest(domain.NewID())
	stale.GenerationId = string(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, stale)); connectCode(err) == 0 {
		t.Fatal("a stale generation was served")
	}
	foreign := f.refreshRequest(domain.NewID())
	foreign.ConnectionId = string(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, foreign)); connectCode(err) == 0 {
		t.Fatal("a foreign connection was served")
	}
	machine := f.refreshRequest(domain.NewID())
	machine.MachineId = string(f.input.MachineID)
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, machine)); connectCode(err) == 0 {
		t.Fatal("a Runner lane request was served for a usage connection")
	}
	reset := f.refreshRequest(domain.NewID())
	reset.Action = pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, reset)); connectCode(err) == 0 {
		t.Fatal("a reset-credit action was served for a usage connection")
	}
	if f.provider.count() != 0 {
		t.Fatalf("a refused request sent %d provider requests", f.provider.count())
	}
}

func TestClaudeUsageInactiveFeatureSchedulesAndServesNothing(t *testing.T) {
	f := newReadFixture(t, nil)
	f.service.claudeUsageDisabledTest = true
	if admitted := f.schedule(); len(admitted) != 0 {
		t.Fatalf("an inactive feature scheduled %v", admitted)
	}
	if _, err := f.refresh(); err == nil {
		t.Fatal("an inactive feature served a refresh")
	}
	if f.provider.count() != 0 {
		t.Fatal("an inactive feature reached the provider")
	}
}

func TestClaudeUsageMaintenanceLoopReadsOnceAndStopsWithItsContext(t *testing.T) {
	f := newReadFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.service.runClaudeUsageReads(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for f.provider.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the maintenance loop did not stop")
	}
	f.await()
	if f.provider.count() != 1 || f.section().LastSuccessAt == nil {
		t.Fatalf("maintenance read: requests %d section %+v", f.provider.count(), f.section())
	}
}

// blockedProvider answers only when released and reports when a request began.
func blockedProvider(respectContext bool) (handler func(int, *http.Request) (*http.Response, error), started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	handler = func(_ int, req *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		if respectContext {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-release:
			}
		} else {
			<-release
		}
		return usageResponse(200, usageBody), nil
	}
	return handler, started, release
}

func TestClaudeUsageDisconnectCancelsAndJoinsTheReadWithoutLatePublication(t *testing.T) {
	for _, respectContext := range []bool{true, false} {
		f := newReadFixture(t, nil)
		handler, started, release := blockedProvider(respectContext)
		f.provider.handler = handler
		generation := f.section().Generation
		if _, err := f.refresh(); err != nil {
			t.Fatal(err)
		}
		<-started
		disconnected := make(chan error, 1)
		go func() { disconnected <- f.disconnect(string(generation)) }()
		if !respectContext {
			time.Sleep(100 * time.Millisecond)
			close(release)
		}
		select {
		case err := <-disconnected:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the disconnect never joined the read")
		}
		section := f.section()
		_, account := f.record()
		if section.Connection != domain.ClaudeUsageReconnectRequired || section.Reading() || len(section.Windows) != 0 || section.LastSuccessAt != nil || len(account.Quota) != 0 {
			t.Fatalf("a late observation was published after the disconnect (context respected %v): %+v", respectContext, section)
		}
		if f.service.claudeUsage.live(f.account()) {
			t.Fatal("the read outlived the disconnect")
		}
	}
}

// A read or login admitted between the disconnect's join and its account gate
// must be joined too: the retirement and cleanup may never run under a live
// owner that would keep the credentials and report success.
func TestClaudeUsageDisconnectRejoinsARunAdmittedAfterTheJoin(t *testing.T) {
	f := newReadFixture(t, nil)
	generation := f.section().Generation
	var admitted atomic.Int32
	f.service.claudeUsageRetirementJoined = func() {
		if admitted.Add(1) != 1 {
			return
		}
		run, runCtx, err := f.service.claudeUsage.reserve(f.account(), domain.NewID(), time.Now().Add(time.Minute))
		if err != nil {
			t.Error(err)
			return
		}
		// The new owner stops when it is canceled, like a real read.
		go func() {
			<-runCtx.Done()
			f.service.claudeUsage.finish(run)
		}()
	}
	if err := f.disconnect(string(generation)); err != nil {
		t.Fatal(err)
	}
	if admitted.Load() < 2 {
		t.Fatalf("the late run was not joined by another attempt: %d joins", admitted.Load())
	}
	section := f.section()
	if f.service.claudeUsage.live(f.account()) || section.Connection == domain.ClaudeUsageLinked || section.Reading() || section.OwesCleanup() || len(f.usageRefs()) != 0 {
		t.Fatalf("the disconnect left authority, a live run or credentials: %+v refs %d", section, len(f.usageRefs()))
	}
}

func TestClaudeUsageDisconnectRefusesWhenRunsKeepStarting(t *testing.T) {
	f := newReadFixture(t, nil)
	generation := f.section().Generation
	f.service.claudeUsageRetirementJoined = func() {
		run, runCtx, err := f.service.claudeUsage.reserve(f.account(), domain.NewID(), time.Now().Add(time.Minute))
		if err != nil {
			t.Error(err)
			return
		}
		go func() {
			<-runCtx.Done()
			f.service.claudeUsage.finish(run)
		}()
	}
	before := f.section()
	if err := f.disconnect(string(generation)); err == nil {
		t.Fatal("a disconnect was acknowledged while runs kept starting")
	}
	f.service.claudeUsageRetirementJoined = nil
	after := f.section()
	if after.Connection != before.Connection || !after.Published || len(f.usageRefs()) == 0 {
		t.Fatalf("a refused disconnect changed the connection: %+v", after)
	}
	if err := f.disconnect(string(generation)); err != nil {
		t.Fatalf("the retried disconnect failed: %v", err)
	}
}

func TestClaudeUsageLogoutCancelsAndJoinsTheRead(t *testing.T) {
	f := newReadFixture(t, nil)
	handler, started, _ := blockedProvider(true)
	f.provider.handler = handler
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	// Joining the read changes the account, so the first request may carry a
	// stale revision; the joined read is gone and the retry is accepted.
	accepted := false
	for i := 0; i < 3 && !accepted; i++ {
		_, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
		accepted = err == nil
	}
	if !accepted {
		t.Fatal("the logout was never accepted")
	}
	if f.service.claudeUsage.live(f.account()) {
		t.Fatal("the logout was accepted before the read was joined")
	}
	awaitServerFixture(t, f.run())
	_, account := f.record()
	section := account.Subscription.ClaudeUsage
	if account.Connection != nil || section == nil || section.Reading() || section.Connection != domain.ClaudeUsageReconnectRequired || section.Refresh != domain.ClaudeUsageIdle || len(account.Quota) != 0 {
		t.Fatalf("a read survived the logout: %+v", section)
	}
	if f.provider.count() != 1 {
		t.Fatalf("provider requests = %d", f.provider.count())
	}
}

func TestClaudeUsageReplacedBindingPublishesNothing(t *testing.T) {
	f := newReadFixture(t, nil)
	handler, started, release := blockedProvider(true)
	f.provider.handler = handler
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	// The usage generation is replaced while the provider request is in flight.
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.usage.generation", nil, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, f.account(), 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ClaudeUsage.Generation = domain.NewID()
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.await()
	if section := f.section(); section.LastSuccessAt != nil || len(section.Windows) != 0 {
		t.Fatalf("an observation of the replaced generation was published: %+v", section)
	}
}

func TestClaudeUsagePublicationRecheckCoversEveryBindingField(t *testing.T) {
	f := newReadFixture(t, nil)
	claimed := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		once.Do(func() { close(claimed) })
		<-release
		return usageResponse(200, usageBody), nil
	}
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-claimed
	_, account := f.record()
	usage := account.Subscription.ClaudeUsage
	read := &claudeUsageRead{account: f.account(), actor: domain.Principal{Type: domain.OwnerDevice}, epoch: f.service.subscriptionServerEpoch(), generation: usage.Generation, connection: account.Connection.ID, credential: usage.CredentialID}
	check := func(name string, mutate func(*claudeUsageRead), want bool) {
		t.Helper()
		candidate := *read
		mutate(&candidate)
		err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			return f.service.claudeUsageBoundTx(tx, account, &candidate, domain.ClaudeUsageReading)
		})
		if (err == nil) != want {
			t.Fatalf("%s: bound = %v, want %v", name, err == nil, want)
		}
	}
	check("unchanged", func(*claudeUsageRead) {}, true)
	check("generation", func(r *claudeUsageRead) { r.generation = domain.NewID() }, false)
	check("connection", func(r *claudeUsageRead) { r.connection = domain.NewID() }, false)
	check("credential", func(r *claudeUsageRead) { r.credential = domain.NewID() }, false)
	check("server epoch", func(r *claudeUsageRead) { r.epoch = domain.NewID() }, false)
	check("revoked actor", func(r *claudeUsageRead) {
		r.actor = domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
	}, false)
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		return f.service.claudeUsageBoundTx(tx, account, read, domain.ClaudeUsageRenewing)
	})
	if err == nil {
		t.Fatal("a read bound in the wrong phase")
	}
	close(release)
	f.await()
}

func TestClaudeUsageRenewalBeforeExpiryRotatesThroughDurableIntentAndReadsOnce(t *testing.T) {
	f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
	section := f.section()
	old := section.CredentialID
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	if f.renewOpens.Load() != 1 || f.renewal.runs.Load() != 1 || f.renewal.captures.Load() != 1 || f.renewal.releases.Load() == 0 {
		t.Fatalf("renewal native work: opens %d runs %d captures %d releases %d", f.renewOpens.Load(), f.renewal.runs.Load(), f.renewal.captures.Load(), f.renewal.releases.Load())
	}
	if f.renewSeen.Load() != usageFixtureRefresh {
		t.Fatal("the renewal did not receive the stored refresh token")
	}
	if f.provider.count() != 1 || f.provider.request(0).Header.Get("Authorization") != "Bearer "+usageRenewedAccess {
		t.Fatalf("the read did not use the rotated access token: %d requests", f.provider.count())
	}
	section = f.section()
	if section.CredentialID == old || section.Refresh != domain.ClaudeUsageIdle || section.Connection != domain.ClaudeUsageLinked || !section.Published || section.ProfileCleanup || section.LastSuccessAt == nil {
		t.Fatalf("section after renewal = %+v", section)
	}
	if len(section.RetiredCredentials) != 1 || section.RetiredCredentials[0] != old {
		t.Fatalf("the superseded credential is not owed for deletion: %v", section.RetiredCredentials)
	}
	// The rotated bundle opens only under its own binding; the old reference is
	// removed by the owned cleanup.
	raw, err := f.secrets.Get(context.Background(), claudeUsageRef(f.account(), section.CredentialID))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenClaudeUsageBundle(raw, f.service.Identity.ServerID, f.account(), section.CredentialID, section.Identity.Commitment)
	if err != nil || string(bundle.AccessToken) != usageRenewedAccess || string(bundle.RefreshToken) != usageRenewedRefresh {
		t.Fatalf("rotated bundle: %v", err)
	}
	bundle.Clear()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
		t.Fatal(err)
	}
	if after := f.section(); after.OwesCleanup() || len(f.usageRefs()) != 1 || f.usageRefs()[0].ID != section.CredentialID {
		t.Fatalf("cleanup left %+v refs %v", after, f.usageRefs())
	}
	if !f.renewal.zeroed() {
		t.Fatal("the rotated token buffers were not cleared")
	}
	// The next read uses the rotated credential and needs no second renewal.
	f.clock.Advance(5 * time.Minute)
	if err := f.refreshAndWait(); err != nil || f.renewal.runs.Load() != 1 {
		t.Fatalf("second read: %v runs %d", err, f.renewal.runs.Load())
	}
}

func TestClaudeUsageDefiniteUnauthorizedRenewsOnceAndRetriesTheGetOnce(t *testing.T) {
	f := newReadFixture(t, nil)
	f.provider.handler = func(n int, _ *http.Request) (*http.Response, error) {
		if n == 0 {
			return usageResponse(401, "expired"), nil
		}
		return usageResponse(200, usageBody), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	if f.provider.count() != 2 || f.renewal.runs.Load() != 1 {
		t.Fatalf("requests %d renewals %d", f.provider.count(), f.renewal.runs.Load())
	}
	if f.provider.request(0).Header.Get("Authorization") != "Bearer "+usageFixtureAccess || f.provider.request(1).Header.Get("Authorization") != "Bearer "+usageRenewedAccess {
		t.Fatal("the retry did not use the rotated token")
	}
	if section := f.section(); section.Failure != domain.ClaudeUsageFailureNone || section.LastSuccessAt == nil || section.Connection != domain.ClaudeUsageLinked {
		t.Fatalf("section = %+v", section)
	}
}

func TestClaudeUsageSecondUnauthorizedRequiresReconnectWithoutAnotherRenewal(t *testing.T) {
	f := newReadFixture(t, nil)
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	seeded := *f.section().LastSuccessAt
	f.clock.Advance(5 * time.Minute)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) { return usageResponse(401, "no"), nil }
	sent := f.provider.count()
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	if f.provider.count() != sent+2 || f.renewal.runs.Load() != 1 {
		t.Fatalf("requests %d renewals %d", f.provider.count()-sent, f.renewal.runs.Load())
	}
	section := f.section()
	if section.Connection != domain.ClaudeUsageReconnectRequired || section.Failure != domain.ClaudeUsageFailureUnauthorized || section.Published || section.Refresh != domain.ClaudeUsageIdle || !section.LastSuccessAt.Equal(seeded) || len(section.Windows) != 2 {
		t.Fatalf("section = %+v", section)
	}
	// Reads are revoked: no trigger sends or renews again.
	f.clock.Advance(time.Hour)
	f.refreshAll()
	f.schedule()
	if _, err := f.refresh(); err == nil {
		t.Fatal("a reconnect-required connection accepted a refresh")
	}
	if f.provider.count() != sent+2 || f.renewal.runs.Load() != 1 {
		t.Fatal("a revoked connection read or renewed again")
	}
}

func TestClaudeUsageRejectedUnsupportedAndForeignRenewalsRequireReconnect(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*readFixture)
		want  domain.ClaudeUsageFailure
		reads int
	}{
		{"rejected refresh", func(f *readFixture) {
			f.renewal.status, f.renewal.runErr = claude.UsageRefreshRejected, errors.New("exit 1")
		}, domain.ClaudeUsageFailureUnauthorized, 0},
		{"foreign identity", func(f *readFixture) { f.renewal.email = "someone.else@example.com" }, domain.ClaudeUsageFailureIdentityChanged, 0},
		{"unsupported native", func(f *readFixture) {
			f.service.claudeUsageRenew = func(context.Context, nativeScope, domain.ID, subscription.ClaudeUsageBundle, *slog.Logger) (claudeUsageRenewal, string, error) {
				f.renewOpens.Add(1)
				return nil, "", domain.Fail(domain.Unsupported, "unsupported", "")
			}
		}, domain.ClaudeUsageFailureUnsupported, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
			c.setup(f)
			credential := f.section().CredentialID
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			section := f.section()
			if section.Connection != domain.ClaudeUsageReconnectRequired || section.Failure != c.want || section.Published || section.Refresh != domain.ClaudeUsageIdle || section.Reading() {
				t.Fatalf("section = %+v", section)
			}
			if f.provider.count() != c.reads || f.renewOpens.Load() != 1 || f.renewal.runs.Load() > 1 {
				t.Fatalf("requests %d opens %d runs %d", f.provider.count(), f.renewOpens.Load(), f.renewal.runs.Load())
			}
			if len(section.RetiredCredentials) == 0 || section.RetiredCredentials[len(section.RetiredCredentials)-1] != credential && !containsID(section.RetiredCredentials, credential) {
				t.Fatalf("the credential is not owed for deletion: %v", section.RetiredCredentials)
			}
			// A foreign identity's bundle is never stored.
			if c.name == "foreign identity" {
				for _, ref := range f.usageRefs() {
					raw, err := f.secrets.Get(context.Background(), ref)
					if err == nil && strings.Contains(string(raw), "access_token") && ref.ID != credential {
						t.Fatalf("a foreign bundle was stored under %s", ref.ID)
					}
				}
				if !f.renewal.zeroed() {
					t.Fatal("the foreign bundle buffers were not cleared")
				}
			}
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
				t.Fatal(err)
			}
			if after := f.section(); after.OwesCleanup() || len(f.usageRefs()) != 0 {
				t.Fatalf("cleanup left %+v refs %v", after, f.usageRefs())
			}
		})
	}
}

func containsID(ids []domain.ID, id domain.ID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestClaudeUsageRenewalThatNeverStartedBacksOffWithoutLosingTheLink(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"discovery unavailable", domain.Fail(domain.Unavailable, "no executable", "")},
		{"leftover credential storage", nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "present", "")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
			f.service.claudeUsageRenew = func(context.Context, nativeScope, domain.ID, subscription.ClaudeUsageBundle, *slog.Logger) (claudeUsageRenewal, string, error) {
				f.renewOpens.Add(1)
				return nil, "", c.err
			}
			credential := f.section().CredentialID
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			section := f.section()
			if section.Connection != domain.ClaudeUsageLinked || !section.Published || section.CredentialID != credential || section.Refresh != domain.ClaudeUsageIdle || section.Failure != domain.ClaudeUsageFailureUnavailable || section.BackoffUntil == nil {
				t.Fatalf("section = %+v", section)
			}
			if f.provider.count() != 0 || f.renewal.runs.Load() != 0 {
				t.Fatalf("requests %d runs %d", f.provider.count(), f.renewal.runs.Load())
			}
			// The stored refresh token was never offered, so the renewal may be
			// retried after the pause; the owed native cleanup settles first.
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
				t.Fatal(err)
			}
			if f.section().OwesCleanup() {
				t.Fatalf("cleanup left %+v", f.section())
			}
			f.clock.Advance(domain.ClaudeUsageDefaultBackoff)
			f.service.claudeUsageRenew = func(context.Context, nativeScope, domain.ID, subscription.ClaudeUsageBundle, *slog.Logger) (claudeUsageRenewal, string, error) {
				f.renewOpens.Add(1)
				return f.renewal, domain.ClaudeProtocolVersion, nil
			}
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			if f.renewal.runs.Load() != 1 || f.provider.count() != 1 || f.section().Failure != domain.ClaudeUsageFailureNone {
				t.Fatalf("the renewal did not resume: runs %d requests %d %+v", f.renewal.runs.Load(), f.provider.count(), f.section())
			}
		})
	}
}

func TestClaudeUsageAmbiguousRotationRetainsRecoveryAndNeverRepeatsAnExchange(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*readFixture)
	}{
		{"lost refresh response", func(f *readFixture) {
			f.renewal.status, f.renewal.runErr = claude.UsageRefreshUncertain, errors.New("lost")
		}},
		{"capture failure after a joined child", func(f *readFixture) { f.renewal.captureErr = domain.Fail(domain.RecoveryRequired, "ambiguous", "") }},
		{"native open requires recovery", func(f *readFixture) {
			f.service.claudeUsageRenew = func(context.Context, nativeScope, domain.ID, subscription.ClaudeUsageBundle, *slog.Logger) (claudeUsageRenewal, string, error) {
				f.renewOpens.Add(1)
				return nil, "", domain.Fail(domain.RecoveryRequired, "child unproven", "")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
			c.setup(f)
			if err := f.refreshAndWait(); err != nil {
				t.Fatal(err)
			}
			section := f.section()
			if section.Connection != domain.ClaudeUsageReconnectRequired || section.Refresh != domain.ClaudeUsageRotationUncertain || section.Published || !section.ProfileCleanup || section.Reading() {
				t.Fatalf("section = %+v", section)
			}
			if f.provider.count() != 0 {
				t.Fatalf("an ambiguous rotation still read: %d", f.provider.count())
			}
			// No trigger repeats the exchange, before or after a restart.
			f.clock.Advance(time.Hour)
			f.refreshAll()
			f.schedule()
			if _, err := f.refresh(); err == nil {
				t.Fatal("an ambiguous rotation accepted a refresh")
			}
			f.service.claudeUsage = claudeUsageRuns{}
			f.service.subscriptionEpoch = domain.NewID()
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			if err := f.service.initializeServerSubscriptions(ctx); err != nil {
				t.Fatal(err)
			}
			f.schedule()
			if f.renewOpens.Load() != 1 || f.renewal.runs.Load() > 1 || f.provider.count() != 0 {
				t.Fatalf("exchange repeated: opens %d runs %d requests %d", f.renewOpens.Load(), f.renewal.runs.Load(), f.provider.count())
			}
			if after := f.section(); after.Refresh != domain.ClaudeUsageRotationUncertain || after.Reading() {
				t.Fatalf("restart dropped the recovery state: %+v", after)
			}
			// The owned cleanup removes the storage and references but the phase
			// stays visible until the user reconnects.
			if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
				t.Fatal(err)
			}
			if after := f.section(); after.OwesCleanup() || after.Refresh != domain.ClaudeUsageRotationUncertain {
				t.Fatalf("after cleanup %+v", after)
			}
		})
	}
}

func TestClaudeUsageShutdownDuringRenewalIsFencedAtRestartAndNeverRepeated(t *testing.T) {
	f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
	f.renewal.block = make(chan struct{})
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.renewal.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if section := f.section(); section.Refresh != domain.ClaudeUsageRenewing || !section.ProfileCleanup {
		t.Fatalf("the renewal claim is not durable: %+v", section)
	}
	f.service.closeClaudeUsage()
	if section := f.section(); section.Refresh != domain.ClaudeUsageRenewing {
		t.Fatalf("shutdown rewrote the durable renewal claim: %+v", section)
	}
	f.service.claudeUsage = claudeUsageRuns{}
	f.service.subscriptionEpoch = domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section.Connection != domain.ClaudeUsageReconnectRequired || section.Refresh != domain.ClaudeUsageRotationUncertain || section.Published || !section.ProfileCleanup {
		t.Fatalf("restart section = %+v", section)
	}
	f.schedule()
	if f.renewal.runs.Load() != 1 || f.provider.count() != 0 {
		t.Fatalf("restart repeated the renewal: runs %d requests %d", f.renewal.runs.Load(), f.provider.count())
	}
}

func TestClaudeUsageRestartReleasesAnInterruptedReadWithoutRepeatingIt(t *testing.T) {
	f := newReadFixture(t, nil)
	handler, started, _ := blockedProvider(true)
	f.provider.handler = handler
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	f.service.closeClaudeUsage()
	if section := f.section(); section.Refresh != domain.ClaudeUsageReading {
		t.Fatalf("shutdown rewrote the claim: %+v", section)
	}
	f.service.claudeUsage = claudeUsageRuns{}
	f.service.subscriptionEpoch = domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.initializeServerSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section.Refresh != domain.ClaudeUsageIdle || section.Connection != domain.ClaudeUsageLinked || section.LastAttemptAt == nil || section.LastSuccessAt != nil {
		t.Fatalf("restart section = %+v", section)
	}
	// The claimed attempt still spaces the next scheduled read.
	if admitted := f.schedule(); len(admitted) != 0 {
		t.Fatalf("a restart forced an immediate scheduled read: %v", admitted)
	}
}

// A read whose registered owner is gone without settling (its settlement
// failed) is fenced by the cleanup scanner in the same server epoch instead of
// rejecting every later admission until a restart.
func TestClaudeUsageScannerReleasesAnOrphanedReadClaimWithoutRepeatingIt(t *testing.T) {
	f := newReadFixture(t, nil)
	handler, started, _ := blockedProvider(true)
	f.provider.handler = handler
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	_, account := f.record()
	if f.service.claudeUsageCleanupCandidate(f.account(), account, f.clock.Now(), false) {
		t.Fatal("a read owned by its live run was selected for cleanup")
	}
	f.service.closeClaudeUsage()
	f.service.claudeUsage = claudeUsageRuns{}
	_, account = f.record()
	if !f.service.claudeUsageCleanupCandidate(f.account(), account, f.clock.Now(), false) {
		t.Fatal("an orphaned read claim was not selected for cleanup")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section.Refresh != domain.ClaudeUsageIdle || section.Connection != domain.ClaudeUsageLinked || section.LastAttemptAt == nil {
		t.Fatalf("the orphaned read claim was not released: %+v", section)
	}
	_, account = f.record()
	if f.service.claudeUsageCleanupCandidate(f.account(), account, f.clock.Now(), false) {
		t.Fatal("a released claim is still selected for cleanup")
	}
}

// An orphaned renewal may have rotated the credential: the scanner requires an
// explicit reconnect and never repeats the exchange.
func TestClaudeUsageScannerFencesAnOrphanedRenewalAsRotationUncertain(t *testing.T) {
	f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
	f.renewal.block = make(chan struct{})
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.renewal.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f.service.closeClaudeUsage()
	f.service.claudeUsage = claudeUsageRuns{}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, account := f.record()
	if !f.service.claudeUsageCleanupCandidate(f.account(), account, f.clock.Now(), false) {
		t.Fatal("an orphaned renewal claim was not selected for cleanup")
	}
	if err := f.service.cleanupClaudeUsage(ctx, f.account()); err != nil {
		t.Fatal(err)
	}
	section := f.section()
	if section.Connection != domain.ClaudeUsageReconnectRequired || section.Refresh != domain.ClaudeUsageRotationUncertain || section.Published {
		t.Fatalf("the orphaned renewal was not fenced: %+v", section)
	}
	f.schedule()
	if f.renewal.runs.Load() != 1 || f.provider.count() != 0 {
		t.Fatalf("the scanner repeated the renewal: runs %d requests %d", f.renewal.runs.Load(), f.provider.count())
	}
}

func TestRetryClaudeUsageSettlementRepeatsTransientFailuresOnly(t *testing.T) {
	calls := 0
	transient := errors.New("database is busy")
	if err := retryClaudeUsageSettlement(3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return transient
		}
		return nil
	}); err != nil || calls != 3 {
		t.Fatalf("a transient failure was not retried to success: calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := retryClaudeUsageSettlement(3, time.Millisecond, func() error { calls++; return transient }); !errors.Is(err, transient) || calls != 3 {
		t.Fatalf("a persistent failure was not reported after the bounded attempts: calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := retryClaudeUsageSettlement(3, time.Millisecond, func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("a settled read was repeated: calls=%d err=%v", calls, err)
	}
}

func TestClaudeUsageReadsAreRoutedAndOpenedOnlyThroughTheOwner(t *testing.T) {
	f := newReadFixture(t, nil)
	f.provider.handler = func(_ int, req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api.anthropic.com" || req.URL.Path != "/api/oauth/usage" || req.URL.RawQuery != "" || req.Body != nil && req.ContentLength > 0 {
			t.Errorf("unexpected request target %s", req.URL)
		}
		return usageResponse(200, usageBody), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	// Without an injected transport the request goes through the server network
	// route resolver, never a direct connection: an unusable route fails the read.
	f.service.claudeUsageTransport = nil
	client, release := f.service.claudeUsageClient()
	defer release()
	if client.CheckRedirect == nil || client.Timeout != 5*time.Second {
		t.Fatalf("client: redirect guard %v timeout %v", client.CheckRedirect != nil, client.Timeout)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirects are not refused: %v", err)
	}
}

// logTimestamp matches the JSON time field of one structured log line.
var logTimestamp = regexp.MustCompile(`"time":"[^"]*",?`)

func TestLogTimestampRemovalLeavesTheFieldsOnly(t *testing.T) {
	line := `{"time":"2026-10-17T15:32:30.7512+09:00","level":"INFO","msg":"claude_usage_read_finished","result":"observed"}`
	got := logTimestamp.ReplaceAllString(line, "")
	if strings.Contains(got, "0.75") || !strings.Contains(got, `"msg":"claude_usage_read_finished"`) {
		t.Fatalf("unexpected stripped line %s", got)
	}
}

func TestClaudeUsageLogsStateAndResourcesNeverExposeSecrets(t *testing.T) {
	f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = time.Minute })
	// Renewal, a successful read, throttling and a reconnect-required end.
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(5 * time.Minute)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		return usageResponse(429, "secret "+usageRenewedAccess, "Retry-After", "30"), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) { return usageResponse(401, "bad"), nil }
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	// The handler stamps every line with a fractional-second time; a stamp such
	// as "10.7512" contains the quota text "0.75" by chance, so only the fields
	// are searched.
	logs := logTimestamp.ReplaceAllString(f.logText(), "")
	for _, secret := range []string{usageFixtureAccess, usageFixtureRefresh, usageRenewedAccess, usageRenewedRefresh, usageFixtureEmail, usageFixtureOrgName, usageFixtureOrgID, "Bearer", "api.anthropic.com", "/api/oauth/usage", "utilization", "remaining", "0.75", "/subscription-runtime", "Usage Sentinel", f.section().Identity.Commitment} {
		if strings.Contains(logs, secret) {
			t.Fatalf("a log line exposed %q", secret)
		}
	}
	for _, event := range []string{"claude_usage_read_claimed", "claude_usage_read_finished", "claude_usage_renewal_started", "claude_usage_renewal_published"} {
		if !strings.Contains(logs, event) {
			t.Fatalf("log event %s is missing", event)
		}
	}
	_, account := f.record()
	raw, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{usageFixtureAccess, usageFixtureRefresh, usageRenewedAccess, usageRenewedRefresh} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the account document exposed a token")
		}
	}
}

func TestClaudeUsageOtherAccountKindsKeepTheirObservationLane(t *testing.T) {
	// A ChatGPT quota request keeps its existing validation: the usage owner never
	// claims an account that holds no usage section.
	f := newSubscriptionFixture(t)
	request := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA}
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err == nil {
		t.Fatal("a disconnected ChatGPT account accepted an observation")
	}
	if f.service.claudeUsage.live(f.input.AccountID) {
		t.Fatal("the usage owner claimed a ChatGPT account")
	}
}

// bumpAccountRevision commits a benign account write that changes neither the
// connection, the execution generation nor the usage section.
func (f *readFixture) bumpAccountRevision() {
	f.t.Helper()
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.usage.revision", nil, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, f.account(), 0)
		if err != nil {
			return nil, err
		}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	}); err != nil {
		f.t.Fatal(err)
	}
}

// An explicit refresh is fenced by the account revision it observed: a stale row
// or Manage request must not start a provider read after the account changed,
// even though its connection and execution generation are unchanged.
func TestClaudeUsageExplicitRefreshIsFencedByTheExpectedRevision(t *testing.T) {
	f := newReadFixture(t, nil)
	stale := f.refreshRequest(domain.NewID())
	f.bumpAccountRevision()
	_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, stale))
	if connectCode(err) != connect.CodeAborted && connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a stale-revision refresh = %v, want a revision conflict", err)
	}
	f.await()
	if f.provider.count() != 0 {
		t.Fatalf("the stale refresh sent %d provider requests", f.provider.count())
	}
	if f.section().Refresh != domain.ClaudeUsageIdle {
		t.Fatalf("the stale refresh left a claim: %+v", f.section())
	}
	// The current revision reads, and the same request ID with another revision
	// is not an exact replay of the claimed request.
	current := f.refreshRequest(domain.NewID())
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, current)); err != nil {
		t.Fatal(err)
	}
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("the current refresh sent %d provider requests", f.provider.count())
	}
	reused := f.refreshRequest(domain.ID(current.Mutation.RequestId))
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, reused)); err == nil {
		t.Fatal("a request ID reused with a different revision was treated as an exact replay")
	}
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("the reused request ID started another read: %d", f.provider.count())
	}
}

// A stale request that would coalesce into a running read is fenced too: it is
// refused before any receipt is written, and an exact retry with the current
// revision is accepted.
func TestClaudeUsageCoalescedRefreshIsFencedByTheExpectedRevision(t *testing.T) {
	f := newReadFixture(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
	// A regression must fail the test instead of leaving the read blocked.
	t.Cleanup(releaseRead)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-release
		return usageResponse(200, usageBody), nil
	}
	stale := f.refreshRequest(domain.NewID())
	if _, err := f.refresh(); err != nil {
		t.Fatal(err)
	}
	<-started
	// The running read already moved the account revision past the stale request.
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, stale)); err == nil {
		t.Fatal("a stale-revision request coalesced into the running read")
	}
	if _, replayed, err := f.service.Store.Replay(context.Background(), domain.ID(stale.Mutation.RequestId), "claude.usage.read.claim", nil); err == nil && replayed {
		t.Fatal("a refused stale request left a durable claim")
	}
	current := f.refreshRequest(domain.ID(stale.Mutation.RequestId))
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, current)); err != nil {
		t.Fatalf("the current-revision request was not accepted: %v", err)
	}
	releaseRead()
	f.await()
	if f.provider.count() != 1 {
		t.Fatalf("coalescing sent %d provider requests", f.provider.count())
	}
}
