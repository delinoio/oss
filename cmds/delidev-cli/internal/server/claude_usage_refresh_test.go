// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	usageFixtureRotatedAccess = "sk-ant-oat01-UsageServerRotatedAccessABCDEFGHIJKLMNOP0123456789"
)

// rotating makes the fake native refresh-login rotate the stored bundle.
func (f *usageFixture) rotating() {
	f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
		process := newFakeUsageProcess()
		close(process.finish)
		f.native.store.set(usageNativeRecord(usageFixtureRotatedAccess, usageFixtureRotated, time.Now().Add(time.Hour)))
		return process, nil
	}
}

func (f *usageFixture) openCurrent() subscription.ClaudeUsageBundle {
	f.t.Helper()
	usage := f.usage()
	f.secrets.mu.Lock()
	raw := f.secrets.values[credentials.Ref{Owner: f.input.AccountID, ID: usage.CredentialID, Purpose: credentials.ClaudeUsage}]
	f.secrets.mu.Unlock()
	bundle, err := subscription.OpenClaudeUsage(raw, f.service.Identity.ServerID, f.input.AccountID, usage.Generation, usage.CredentialID, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return bundle
}

func TestClaudeUsageRefreshesOnceBeforeExpiryAndCapturesTheRotatedBundle(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	old := f.seedBundle(time.Now().Add(30 * time.Second))
	f.rotating()
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadAccepted {
		t.Fatal(outcome)
	}
	if len(f.native.refreshInputs) != 1 {
		t.Fatalf("expected one serialized refresh-login, saw %d", len(f.native.refreshInputs))
	}
	input := f.native.refreshInputs[0]
	if string(input.RefreshToken) != usageFixtureRefresh || !slices.Contains(input.Scopes, "user:profile") || !slices.Contains(input.Scopes, "user:inference") || input.ClientID != subscription.ClaudeUsageClientID {
		t.Fatalf("refresh input %+v", input)
	}
	if f.transport.count() != 1 || f.transport.tokens[0] != usageFixtureRotatedAccess {
		t.Fatalf("the GET must use the rotated access token exactly once: %v", f.transport.tokens)
	}
	usage := f.usage()
	if usage.CredentialID == old || usage.LastSuccess == nil || usage.Read.RefreshClaimed || usage.Read.Active() || !slices.Contains(usage.RetiredCredentials, old) {
		t.Fatalf("usage after rotation %+v", usage)
	}
	bundle := f.openCurrent()
	if string(bundle.RefreshToken) != usageFixtureRotated {
		t.Fatal("the rotated bundle was not captured")
	}
	bundle.Clear()
	// The superseded reference is removed by the durable cleanup.
	if err := f.service.completeClaudeUsageCleanup(f.owner(), f.input.AccountID, false); err != nil {
		t.Fatal(err)
	}
	if len(f.usageRefs()) != 1 || len(f.usage().RetiredCredentials) != 0 {
		t.Fatalf("superseded reference remained: %d", len(f.usageRefs()))
	}
	if present, _ := f.native.store.Present(context.Background()); present {
		t.Fatal("native credentials stayed in the profile after capture")
	}
	if f.native.opens.Load() == 0 || f.native.closes.Load() == 0 {
		t.Fatal("the exclusive profile was not released")
	}
}

func TestClaudeUsageDefiniteUnauthorizedRefreshesOnceThenRetriesTheGetOnce(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.rotating()
	f.transport.responses = []usageResponse{{status: 401}, {status: 200, body: `{"five_hour":{"utilization":10}}`}}
	f.refreshOnce(true)
	if len(f.native.refreshInputs) != 1 || f.transport.count() != 2 {
		t.Fatalf("refreshes %d GETs %d", len(f.native.refreshInputs), f.transport.count())
	}
	if f.transport.tokens[0] != usageFixtureAccess || f.transport.tokens[1] != usageFixtureRotatedAccess {
		t.Fatalf("tokens %v", f.transport.tokens)
	}
	if usage := f.usage(); usage.LastSuccess == nil || len(usage.Windows) != 1 || usage.Windows[0].Remaining != 0.9 || usage.ReconnectRequired {
		t.Fatalf("usage %+v", usage)
	}
}

func TestClaudeUsageSecondUnauthorizedRequiresReconnectWithoutAnotherExchange(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.rotating()
	f.refreshOnce(true)
	accepted := *f.usage()
	f.transport.mu.Lock()
	f.transport.responses = []usageResponse{{status: 401}, {status: 401}}
	f.transport.mu.Unlock()
	f.native.refreshInputs = nil
	time.Sleep(5 * time.Millisecond)
	f.refreshOnce(true)
	if len(f.native.refreshInputs) != 1 || f.transport.count() != 3 {
		t.Fatalf("exactly one refresh and one retried GET are allowed: refreshes %d GETs %d", len(f.native.refreshInputs), f.transport.count())
	}
	usage := f.usage()
	if !usage.ReconnectRequired || usage.Failure != domain.ClaudeLimitFailureUnauthorized || !usage.LastSuccess.Equal(*accepted.LastSuccess) || len(usage.Windows) != 2 {
		t.Fatalf("usage %+v", usage)
	}
	// An explicit reconnect is required: no trigger reads or exchanges again.
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible {
		t.Fatalf("outcome %s", outcome)
	}
	if len(f.native.refreshInputs) != 1 {
		t.Fatal("a repeated refresh exchange was attempted")
	}
}

func TestClaudeUsageRefreshFailuresRequireReconnectAndNeverRepeatTheExchange(t *testing.T) {
	scenarios := []struct {
		name    string
		prepare func(*usageFixture)
		failure domain.ClaudeLimitFailure
	}{
		{"rejected refresh", func(f *usageFixture) {
			f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
				process := newFakeUsageProcess()
				process.waitErr = domain.Fail(domain.Unavailable, "exit", "")
				close(process.finish)
				return process, nil
			}
		}, domain.ClaudeLimitFailureRefreshRejected},
		{"foreign identity", func(f *usageFixture) {
			f.rotating()
			f.native.identity.Email = "foreign@example.com"
		}, domain.ClaudeLimitFailureIdentityChanged},
		{"unsupported native renewal", func(f *usageFixture) {
			f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
				return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureRefreshUnsupported, 0)
			}
		}, domain.ClaudeLimitFailureRefreshUnsupported},
		{"nothing rotated", func(f *usageFixture) {
			f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
				process := newFakeUsageProcess()
				close(process.finish)
				f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
				return process, nil
			}
		}, domain.ClaudeLimitFailureRefreshUnsupported},
		{"lost rotation", func(f *usageFixture) {
			f.rotating()
			f.secrets.putError = errors.New("vault unavailable")
		}, domain.ClaudeLimitFailureRefreshUncertain},
		{"unreadable rotated bundle", func(f *usageFixture) {
			f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
				process := newFakeUsageProcess()
				close(process.finish)
				f.native.store.set([]byte(`{"claudeAiOauth":{"accessToken":"broken"}}`))
				return process, nil
			}
		}, domain.ClaudeLimitFailureRefreshUncertain},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			f := newUsageFixture(t)
			f.link()
			old := f.seedBundle(time.Now().Add(30 * time.Second))
			f.refreshOnce(true)
			accepted := f.usage()
			if accepted.LastSuccess != nil {
				t.Fatal("seed read published")
			}
			scenario.prepare(f)
			// Expire the seeded bundle again so the pre-expiry refresh runs.
			f.mutateUsage(func(s *domain.ClaudeLimitState) { s.ReconnectRequired, s.Failure, s.FailureAt = false, "", nil })
			sent := f.transport.count()
			f.refreshOnce(true)
			usage := f.usage()
			if usage.Failure != scenario.failure || !usage.ReconnectRequired {
				t.Fatalf("failure %q reconnect %v", usage.Failure, usage.ReconnectRequired)
			}
			if f.transport.count() != sent {
				t.Fatal("a GET was sent after the refresh failed")
			}
			if usage.CredentialID != old {
				t.Fatal("the original credential was replaced by an unconfirmed rotation")
			}
			if present, _ := f.native.store.Present(context.Background()); present {
				t.Fatal("rotated bytes stayed in the profile")
			}
			if strings.Contains(string(mustJSON(t, usage)), usageFixtureRotated) {
				t.Fatal("a rotated token entered stored state")
			}
			exchanges := len(f.native.refreshInputs)
			if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible || len(f.native.refreshInputs) != exchanges {
				t.Fatalf("a repeat exchange was attempted: %s", outcome)
			}
			if _, a := f.record(); a.Health != domain.AccountReady {
				t.Fatal("a renewal failure changed execution readiness")
			}
		})
	}
}

func TestClaudeUsageUnjoinableRefreshChildLeavesAnUncertainFencedRecovery(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.seedBundle(time.Now().Add(30 * time.Second))
	f.native.refresh = func(claude.UsageRefreshInput) (*fakeUsageProcess, error) {
		process := newFakeUsageProcess()
		process.closeErr = errors.New("still running")
		close(process.finish)
		return process, nil
	}
	f.refreshOnce(true)
	usage := f.usage()
	if usage.Failure != domain.ClaudeLimitFailureRefreshUncertain || !usage.ReconnectRequired || !usage.Read.RefreshClaimed {
		t.Fatalf("usage %+v", usage)
	}
}

func TestClaudeUsageRestartNeverRepeatsLoginRefreshOrFetchAmbiguity(t *testing.T) {
	t.Run("interrupted login", func(t *testing.T) {
		f := newUsageFixture(t)
		started, err := f.start("")
		if err != nil {
			t.Fatal(err)
		}
		// A server of another epoch owned this login; this one cannot claim it.
		f.service.subscriptionEpoch = domain.NewID()
		f.service.runClaudeUsageLogin(f.owner(), f.input.AccountID)
		if f.native.opens.Load() != 0 {
			t.Fatal("a stale-epoch login was relaunched")
		}
		f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
		if err := f.service.reconcileClaudeUsage(f.owner(), f.input.AccountID); err != nil {
			t.Fatal(err)
		}
		usage := f.usage()
		if usage.Operation.State != domain.ClaudeLimitLoginFailed || usage.Operation.Failure != domain.ClaudeLimitFailureInterrupted || usage.CleanupPending {
			t.Fatalf("operation %+v", usage.Operation)
		}
		if present, _ := f.native.store.Present(context.Background()); present {
			t.Fatal("interrupted login credentials were not removed")
		}
		if p, err := f.progress(started.OperationId, started.OperationId); err != nil || p.State != pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_FAILED {
			t.Fatalf("progress %v %v", p, err)
		}
		if f.native.loginStarts.Load() != 0 {
			t.Fatal("restart repeated the browser sign-in")
		}
	})
	t.Run("interrupted refresh", func(t *testing.T) {
		f := newUsageFixture(t)
		f.link()
		f.mutateUsage(func(s *domain.ClaudeLimitState) {
			started := time.Now()
			s.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadRefreshing, ID: domain.NewID(), Epoch: domain.NewID(), StartedAt: &started, ConnectionID: domain.NewID(), Generation: s.Generation, Actor: domain.Principal{Type: domain.OwnerDevice}, RefreshClaimed: true}
		})
		if err := f.service.reconcileClaudeUsage(f.owner(), f.input.AccountID); err != nil {
			t.Fatal(err)
		}
		usage := f.usage()
		if usage.Failure != domain.ClaudeLimitFailureRefreshUncertain || !usage.ReconnectRequired || usage.Read.Active() {
			t.Fatalf("usage %+v", usage)
		}
		if len(f.native.refreshInputs) != 0 || f.transport.count() != 0 {
			t.Fatal("restart repeated an uncertain refresh")
		}
	})
	t.Run("interrupted fetch", func(t *testing.T) {
		f := newUsageFixture(t)
		f.link()
		f.mutateUsage(func(s *domain.ClaudeLimitState) {
			started := time.Now()
			s.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadFetching, ID: domain.NewID(), Epoch: domain.NewID(), StartedAt: &started, ConnectionID: domain.NewID(), Generation: s.Generation, Actor: domain.Principal{Type: domain.OwnerDevice}}
		})
		if err := f.service.reconcileClaudeUsage(f.owner(), f.input.AccountID); err != nil {
			t.Fatal(err)
		}
		usage := f.usage()
		if usage.Read.Active() || usage.ReconnectRequired || usage.Failure != domain.ClaudeLimitFailureInterrupted || f.transport.count() != 0 {
			t.Fatalf("usage %+v", usage)
		}
	})
}
