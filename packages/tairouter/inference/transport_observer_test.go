// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestTransportFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		sent bool
		want TransportFailureKind
	}{
		{&net.DNSError{IsNotFound: true}, false, TransportFailureDNS},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, false, TransportFailureDial},
		{&net.OpError{Op: "read", Err: syscall.ECONNRESET}, true, TransportFailureConnection},
		{context.DeadlineExceeded, false, TransportFailureTimeout},
		{context.Canceled, true, TransportFailureCanceled},
		{ErrUnavailable, false, TransportFailureUnknown},
	} {
		f := classifyTransportFailure(context.Background(), tc.err, tc.sent)
		if f.Kind != tc.want || f.Sent != tc.sent {
			t.Fatalf("%v: %+v", tc.err, f)
		}
	}
}

type transportObserverFixture struct {
	failures []TransportFailure
	version  string
}

func (*transportObserverFixture) ObserveResponse(context.Context, Principal, Target, Credential, int, http.Header) error {
	return nil
}
func (o *transportObserverFixture) ObserveTransportFailure(_ context.Context, _ Principal, _ Target, c Credential, f TransportFailure) error {
	o.failures = append(o.failures, f)
	o.version = c.CredentialVersion
	return nil
}
func TestPhysicalTransportObserverActualFailedDial(t *testing.T) {
	h, _, _, _, _ := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") }, Chat)
	o := &transportObserverFixture{}
	h.c.ResponseObserver = o
	h.c.Destinations.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}
	out := call(h, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if out.Code == 200 || len(o.failures) != 1 || o.failures[0].Kind != TransportFailureDial || o.failures[0].Sent {
		t.Fatalf("%d %+v", out.Code, o.failures)
	}
}
func TestPreselectionBudgetDoesNotReplenish(t *testing.T) {
	rec := &UsageRecord{}
	ctx := context.WithValue(context.Background(), routingRequestKey{}, routingRequest{record: rec})
	ctx = RoutingDecisionContext(ctx, 1)
	if !HasPhysicalSendBudget(ctx) || HasPhysicalSendBudget(context.Background()) {
		t.Fatal("incorrect budget presence")
	}
	if err := PhysicalSend(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx = RoutingDecisionContext(ctx, 100)
	err := PhysicalSend(ctx, func(context.Context) error { t.Fatal("replenished budget"); return nil })
	var public *Error
	if !errors.As(err, &public) || public.Code != "request_send_budget_exhausted" {
		t.Fatalf("%v", err)
	}
}

func TestExecutorTransportObserverExcludesSupportAndRetainsVersion(t *testing.T) {
	h, s, _, _, _ := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") }, Chat)
	o := &transportObserverFixture{}
	h.c.ResponseObserver = o
	h.c.Destinations.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}
	x := &requestExecutor{h: h, target: Target{BaseURL: s.URL}, credential: Credential{AccountID: "a", CredentialVersion: "captured"}, record: &UsageRecord{}, maxSends: 2}
	for _, wire := range []Wire{"", Chat} {
		if _, err := x.Do(context.Background(), AdapterRequest{URL: s.URL, ResponseWire: wire}); err == nil {
			t.Fatal("missing failure")
		}
	}
	if len(o.failures) != 1 || o.version != "captured" || x.record.Sends != 0 {
		t.Fatalf("%+v sends=%d", o, x.record.Sends)
	}
}
