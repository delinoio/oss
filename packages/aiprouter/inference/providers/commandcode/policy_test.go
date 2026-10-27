// SPDX-License-Identifier: Apache-2.0
package commandcode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func TestSelectedPolicyCallback(t *testing.T) {
	calls := 0
	a := NewWithOptions(Options{Policy: func(_ context.Context, in inference.AdapterInput) (Policy, error) {
		calls++
		if in.Credential.AccountID == "denied" {
			return Policy{}, inference.ErrUnavailable
		}
		if in.Credential.AccountID == "authoritative" {
			return Policy{Authoritative: true, ReasoningEfforts: map[string][]string{"zai-org/GLM-5.2": {"low"}}}, nil
		}
		return Policy{RejectedEfforts: map[string][]string{"zai-org/GLM-5.2": {"low"}}}, nil
	}})
	// A callback is authoritative over stale context defaults, including an empty
	// callback result. The complete selected policy belongs to the service owner.
	ctx := WithPolicy(context.Background(), Policy{Authoritative: true, ReasoningEfforts: map[string][]string{"zai-org/GLM-5.2": {"low"}}})
	in := fixtureInput(`{"messages":[],"reasoning_effort":"low"}`)
	in.Credential.AccountID = "authoritative"
	r, err := a.Build(ctx, in)
	if err != nil || !strings.Contains(string(r.Body), `"reasoning_effort":"low"`) {
		t.Fatalf("build %s %v", r.Body, err)
	}
	f := inference.HTTPFailure{Status: 422, Body: []byte("unsupported effort")}
	if _, changed, err := a.RepairRequest(ctx, in, r, f); err != nil || changed {
		t.Fatalf("authoritative repair %v %v", changed, err)
	}
	if calls != 2 {
		t.Fatalf("policy resolved %d times", calls)
	}
	in.Credential.AccountID = "other"
	next, err := a.Build(ctx, in)
	if err != nil || strings.Contains(string(next.Body), "reasoning_effort") {
		t.Fatalf("stale policy escaped rotation %s %v", next.Body, err)
	}
	in.Credential.AccountID = "denied"
	if _, err := a.Build(ctx, in); !errors.Is(err, inference.ErrUnavailable) {
		t.Fatalf("build swallowed policy failure: %v", err)
	}
	if _, changed, err := a.RepairRequest(ctx, in, r, f); changed || !errors.Is(err, inference.ErrUnavailable) {
		t.Fatalf("repair swallowed policy failure: %v %v", changed, err)
	}
}

func TestPolicyCallbackSnapshotAndCancellation(t *testing.T) {
	source := Policy{ReasoningEfforts: map[string][]string{"m": {"low"}}, RefreshedEfforts: map[string][]string{"m": {"high"}}, RejectedEfforts: map[string][]string{"m": {"max"}}}
	calls := 0
	a := NewWithOptions(Options{Policy: func(context.Context, inference.AdapterInput) (Policy, error) { calls++; return source, nil }})
	snapshot, err := a.resolvePolicy(context.Background(), fixtureInput(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	source.ReasoningEfforts["m"][0] = "changed"
	source.RefreshedEfforts["m"][0] = "changed"
	source.RejectedEfforts["m"][0] = "changed"
	if snapshot.ReasoningEfforts["m"][0] != "low" || snapshot.RefreshedEfforts["m"][0] != "high" || snapshot.RejectedEfforts["m"][0] != "max" {
		t.Fatal("callback maps retained")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.resolvePolicy(ctx, fixtureInput(`{}`)); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled policy callback invoked", err, calls)
	}
}
