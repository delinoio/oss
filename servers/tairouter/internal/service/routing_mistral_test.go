// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

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
