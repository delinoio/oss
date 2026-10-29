// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

func TestMistralWaitersBelongToTheirAdmission(t *testing.T) {
	for _, differentAccount := range []bool{false, true} {
		for _, cleanupBeforeJoin := range []bool{false, true} {
			name := "cleanup-after-join"
			if cleanupBeforeJoin {
				name = "cleanup-before-join"
			}
			if differentAccount {
				name += "-different-account"
			}
			t.Run(name, func(t *testing.T) {
				a, sc, key := dispatchFixture(t)
				d := saveRoutingFixture(t, a, sc, "account", "model")
				d.Metadata.BaseUrl = "https://api.mistral.ai/v1"
				putRoutingProvider(t, a, sc, d)
				if differentAccount {
					other := saveRoutingFixture(t, a, sc, "other-account", "other-model")
					other.Metadata.BaseUrl = "https://api.mistral.ai/v1"
					other.Secret = "other-fixture-secret"
					putRoutingProvider(t, a, sc, other)
				}
				type pendingRelease struct {
					lease   store.Lease
					cleanup func(context.Context)
				}
				var pending []pendingRelease
				a.releaseCompleted = func(l store.Lease, cleanup func(context.Context)) {
					pending = append(pending, pendingRelease{l, cleanup})
				}
				ctx := context.Background()
				turn := func(model string) (inference.Principal, inference.Target, inference.Credential, func()) {
					p, release, err := a.Admit(ctx, key)
					if err != nil {
						t.Fatal(err)
					}
					route, err := a.Resolve(ctx, p, model, inference.Generate)
					if err != nil {
						t.Fatal(err)
					}
					credential, err := a.Select(ctx, p, route.Targets[0], "")
					if err != nil {
						t.Fatal(err)
					}
					return p, route.Targets[0], credential, release
				}
				pa, _, _, releaseA := turn("model")
				if err := a.Dispatch(ctx, pa, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				a.mu.Lock()
				var first mistralWait
				for _, wait := range a.mistral {
					first = wait
				}
				a.mu.Unlock()
				ticketA := first.ticket
				if ticketA == "" {
					t.Fatal("first admission has no ticket")
				}
				releaseA()
				if len(pending) != 1 {
					t.Fatal("first release was not captured")
				}
				modelB := "model"
				if differentAccount {
					modelB = "other-model"
				}
				pb, targetB, credentialB, releaseB := turn(modelB)
				if !cleanupBeforeJoin && !differentAccount {
					a.mu.Lock()
					leaseB := a.lease
					a.mu.Unlock()
					if err := a.s.SetMistralHold(ctx, leaseB, first.account, first.version, first.digest, first.model, time.Now().Add(-time.Second)); err != nil {
						t.Fatal(err)
					}
				}
				if cleanupBeforeJoin {
					if err := a.s.Release(ctx, pending[0].lease); err != nil {
						t.Fatal(err)
					}
					pending[0].cleanup(ctx)
				}
				if err := a.Dispatch(ctx, pb, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				a.mu.Lock()
				var ticketB string
				for _, wait := range a.mistral {
					ticketB = wait.ticket
				}
				leaseB := a.lease
				a.mu.Unlock()
				if ticketB == "" || ticketB == ticketA {
					t.Fatal("second admission reused first ticket")
				}
				if !cleanupBeforeJoin && !differentAccount {
					a.mu.Lock()
					var claim string
					for _, wait := range a.mistral {
						claim = wait.claim
					}
					a.mu.Unlock()
					if claim == "" {
						t.Fatal("second admission did not hold the due probe claim")
					}
				}
				if _, err := a.s.TryMistralProbe(ctx, leaseB, ticketA); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("older ticket accepted with newer lease: %v", err)
				}
				if !cleanupBeforeJoin {
					if err := a.s.Release(ctx, pending[0].lease); err != nil {
						t.Fatal(err)
					}
					pending[0].cleanup(ctx)
				}
				if err := a.observeMistral(ctx, inference.AdapterInput{Principal: pb, Target: targetB, Credential: credentialB}, inference.HTTPFailure{Status: 200, Header: http.Header{}}); err != nil {
					t.Fatal(err)
				}
				a.mu.Lock()
				_, exists := a.mistral[selectionVersion([]string{credentialB.AccountID, a.versions[credentialB.AccountID], targetB.Model})]
				a.mu.Unlock()
				if !exists {
					t.Fatal("older release removed current admission state")
				}
				releaseB()
				if len(pending) != 2 {
					t.Fatal("second release was not captured")
				}
				if err := a.s.Release(ctx, pending[1].lease); err != nil {
					t.Fatal(err)
				}
				pending[1].cleanup(ctx)
			})
		}
	}
}

func TestMistralMeasuredMinutePressure(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
		hold    bool
	}{
		{"unknown", http.Header{}, false},
		{"zero allowance", http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"0"}, "X-Ratelimit-Remaining-Tokens-Minute": {"0"}}, false},
		{"minute exhausted", http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"0"}}, true},
		{"query above remaining", http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"10"}, "X-Ratelimit-Tokens-Query-Cost": {"20"}}, true},
		{"query above allowance", http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"0"}, "X-Ratelimit-Tokens-Query-Cost": {"101"}}, false},
		{"monthly exhausted", http.Header{"X-Ratelimit-Limit-Tokens-Month": {"1000"}, "X-Ratelimit-Remaining-Tokens-Month": {"0"}, "X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"0"}}, false},
		{"requests exhausted", http.Header{"X-Ratelimit-Limit-Req-Minute": {"20"}, "X-Ratelimit-Remaining-Req-Minute": {"0"}}, true},
		{"invalid remaining", http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"-1"}}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			d, hold := mistralHoldDelay(inference.HTTPFailure{Status: 429, Header: tt.headers}, time.Now())
			if hold != tt.hold || hold && d != time.Minute {
				t.Fatalf("hold=%v delay=%v", hold, d)
			}
		})
	}
	d, hold := mistralHoldDelay(inference.HTTPFailure{Status: 429, Header: http.Header{"Retry-After": {"120"}}}, time.Now())
	if !hold || d != 2*time.Minute {
		t.Fatal("oversized retry hint shortened")
	}
	_, hold = mistralHoldDelay(inference.HTTPFailure{Status: 200, Header: http.Header{"X-Ratelimit-Limit-Tokens-Minute": {"100"}, "X-Ratelimit-Remaining-Tokens-Minute": {"0"}}}, time.Now())
	if hold {
		t.Fatal("healthy zero remaining predicted request charge")
	}
}
func TestMistralSharedProbeAcrossServerAdapters(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	d := saveRoutingFixture(t, a, sc, "account", "model")
	d.Metadata.BaseUrl = "https://api.mistral.ai/v1"
	putRoutingProvider(t, a, sc, d)
	p := admitRouting(t, a, key)
	r, e := a.Resolve(context.Background(), p, "model", inference.Generate)
	if e != nil {
		t.Fatal(e)
	}
	target := r.Targets[0]
	c, e := a.Select(context.Background(), p, target, "")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = a.awaitMistral(ctx, p); e != nil {
		t.Fatal(e)
	}
	in := inference.AdapterInput{Principal: p, Target: target, Credential: c}
	if e = a.observeMistral(ctx, in, inference.HTTPFailure{Status: 429, Header: http.Header{"Retry-After": {"0.05"}}}); e != nil {
		t.Fatal(e)
	}
	b := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
	q := admitRouting(t, b, key)
	br, e := b.Resolve(ctx, q, "model", inference.Generate)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Select(ctx, q, br.Targets[0], ""); e != nil {
		t.Fatal(e)
	}
	if e = a.awaitMistral(ctx, p); e != nil {
		t.Fatal(e)
	}
	waiting := make(chan error, 1)
	go func() { waiting <- b.awaitMistral(ctx, q) }()
	select {
	case e := <-waiting:
		t.Fatalf("second adapter acquired active probe: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = a.observeMistral(ctx, in, inference.HTTPFailure{Status: 200, Header: http.Header{}}); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-waiting:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("successful probe did not release waiting adapter")
	}
}
