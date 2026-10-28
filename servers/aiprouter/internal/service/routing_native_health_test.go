// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNativeHealthPinnedStreak(t *testing.T) {
	now := time.Unix(1700000000, 0)
	p := defaultNativeHealthPolicy()
	h := nativeHealthState{}
	for i := 1; i <= 6; i++ {
		h.note(nativeHealthTransient, p, now)
		if i < 3 {
			if h.avoided(p, now) {
				t.Fatal("early failover")
			}
		} else if got := time.UnixMilli(h.AvoidUntil).Sub(now); got != nativeHealthAvoidSteps[i-3] {
			t.Fatalf("failure %d avoid %v", i, got)
		}
	}
	h.note(nativeHealthSuccess, p, now)
	if h.Failures != 6 {
		t.Fatal("escalated streak cleared after one success")
	}
	h.note(nativeHealthCaller, p, now)
	if h.Successes != 1 {
		t.Fatal("caller error changed streak")
	}
	h.note(nativeHealthSuccess, p, now)
	if h.avoided(p, now) || h.Failures != 0 {
		t.Fatal("two successes did not recover")
	}
	h.note(nativeHealthTransient, p, now)
	h.note(nativeHealthTransient, p, now.Add(5*time.Minute))
	if h.Failures != 2 {
		t.Fatal("inclusive five-minute gap")
	}
	h.note(nativeHealthTransient, p, now.Add(10*time.Minute+time.Millisecond))
	if h.Failures != 1 {
		t.Fatal("stale streak retained")
	}
	p.Threshold = 0
	for i := 0; i < 10; i++ {
		h.note(nativeHealthTransient, p, now)
	}
	if h.avoided(p, now) {
		t.Fatal("disabled failover avoided")
	}
	h.note(nativeHealthSuccess, p, now)
	if h.Failures != 0 {
		t.Fatal("disabled failover needs two successes")
	}
}
func TestNativeHealthFailureWindowBoundaries(t *testing.T) {
	now := time.Unix(1700000000, 0)
	p := defaultNativeHealthPolicy()
	h := nativeHealthState{}
	for i := 0; i < 19; i++ {
		o := nativeHealthSuccess
		if i < 5 {
			o = nativeHealthTransient
		}
		h.note(o, p, now)
	}
	if h.Degraded {
		t.Fatal("less than minimum samples")
	}
	h.note(nativeHealthSuccess, p, now)
	if !h.Degraded {
		t.Fatal("25 percent must degrade")
	}
	if h.steersNew(p, true, now) {
		t.Fatal("manual pin steered by default")
	}
	p.DetourPinned = true
	if !h.steersNew(p, true, now) {
		t.Fatal("explicit detour policy ignored")
	}
	// The old failed samples expire exactly at the lower window boundary.
	for i := 0; i < 20; i++ {
		h.note(nativeHealthSuccess, p, now.Add(60*time.Second))
	}
	if !h.Degraded || h.HealthySince != now.Add(time.Minute).UnixMilli() {
		t.Fatal("recovery must sustain")
	}
	for _, d := range []time.Duration{29 * time.Second, 30 * time.Second} {
		h.recompute(now.Add(time.Minute + d))
		if h.Degraded != (d < 30*time.Second) {
			t.Fatalf("recovery boundary %v", d)
		}
	}
	for i := 0; i < 600; i++ {
		h.note(nativeHealthSuccess, p, now.Add(90*time.Second))
	}
	if len(h.Samples) != 512 {
		t.Fatal("unbounded window")
	}
	b, e := json.Marshal(h)
	if e != nil || len(b) > 32<<10 {
		t.Fatalf("state exceeds durable bound: %d %v", len(b), e)
	}
}
func TestNativeHealthRecoveryNeedsEnoughSamples(t *testing.T) {
	now := time.Unix(1700000000, 0)
	h := nativeHealthState{Degraded: true, HealthySince: now.Add(-time.Minute).UnixMilli()}
	h.recompute(now)
	if !h.Degraded || h.HealthySince != 0 {
		t.Fatal("empty window falsely recovered")
	}
}
func TestNativeHealthClassificationAndQuota(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   nativeHealthOutcome
	}{{200, nativeHealthSuccess}, {302, nativeHealthNeutral}, {400, nativeHealthCaller}, {401, nativeHealthCredential}, {402, nativeHealthQuota}, {403, nativeHealthCredential}, {429, nativeHealthQuota}, {503, nativeHealthTransient}, {0, nativeHealthUnknown}, {600, nativeHealthUnknown}} {
		if got := classifyNativeHealth(tc.status, nativeHealthUnknown, false); got != tc.want {
			t.Fatalf("status %d: %v", tc.status, got)
		}
	}
	if classifyNativeHealth(403, nativeHealthUnknown, true) != nativeHealthWorkspace {
		t.Fatal("workspace refusal quarantined")
	}
	now := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		retry        string
		resets       []time.Time
		delay, avoid time.Duration
		source       nativeHealthCooldownSource
	}{{"", nil, time.Minute, time.Minute, nativeHealthCooldownDefault}, {"0", nil, time.Minute, time.Minute, nativeHealthCooldownDefault}, {"172800", nil, 24 * time.Hour, 24 * time.Hour, nativeHealthCooldownRetryAfter}, {"", []time.Time{now.Add(7 * 24 * time.Hour)}, 15 * time.Minute, 6 * time.Hour, nativeHealthCooldownReset}, {"120", []time.Time{now.Add(time.Hour)}, 2 * time.Minute, time.Hour, nativeHealthCooldownRetryAfter}, {"", []time.Time{now.Add(time.Hour), now.Add(2 * time.Minute)}, 2 * time.Minute, 2 * time.Minute, nativeHealthCooldownReset}} {
		c := nativeHealthQuotaCooldown(tc.retry, tc.resets, now)
		if c.Until.Sub(now) != tc.delay || c.AvoidUntil.Sub(now) != tc.avoid || c.Source != tc.source {
			t.Fatalf("cooldown %+v for %+v", c, tc)
		}
	}
	if nativeHealthHoldExpired(now, now.Add(10*time.Minute)) || !nativeHealthHoldExpired(now, now.Add(10*time.Minute+time.Millisecond)) {
		t.Fatal("hold boundary")
	}
}
