// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"google.golang.org/protobuf/proto"
)

func TestUsageOperatorDestinationCIDRRoundTrip(t *testing.T) {
	f := usageOperatorFixture(t)
	_, client := f.user("operator")
	op := rpc.NewOperatorServiceClient(client, f.url)
	initial, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, err)
	input := proto.Clone(initial.Msg.Settings).(*v1.InstanceSettings)
	id := core.ID()
	input.AllowedDestinations = []*v1.AllowedDestination{
		{Id: id, Origin: "https://provider.example", AllowedCidrs: []string{"203.0.113.0/24", "2001:db8::/32"}},
		{Origin: "http://localhost:11434"},
	}
	updated, err := op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: input, ExpectedRevision: initial.Msg.Settings.Revision}))
	acceptanceOK(t, err)
	read, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, err)
	if !proto.Equal(updated.Msg.Settings, read.Msg.Settings) {
		t.Fatal("generated settings roundtrip changed persisted rules")
	}
	if len(read.Msg.Settings.AllowedDestinations) != 2 {
		t.Fatal("destination rules lost")
	}
	rule := read.Msg.Settings.AllowedDestinations[0]
	if rule.Id != id {
		t.Fatal("destination ID lost")
	}
	if len(rule.AllowedCidrs) != 2 || rule.AllowedCidrs[0] != "203.0.113.0/24" || rule.AllowedCidrs[1] != "2001:db8::/32" {
		t.Fatal("CIDR restrictions lost")
	}
	if len(read.Msg.Settings.AllowedDestinations[1].AllowedCidrs) != 0 {
		t.Fatal("empty origin approval changed semantics")
	}
	for _, bad := range []string{"not-a-prefix", "10.0.0.0/33", "2001:db8::/129", "10.0.0.1", "::ffff:127.0.0.0/80"} {
		t.Run(bad, func(t *testing.T) {
			invalidSettings := proto.Clone(read.Msg.Settings).(*v1.InstanceSettings)
			invalidSettings.AllowedDestinations[0].AllowedCidrs = []string{bad}
			_, err := op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: invalidSettings, ExpectedRevision: read.Msg.Settings.Revision}))
			acceptanceCode(t, err, connect.CodeInvalidArgument)
			after, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
			acceptanceOK(t, err)
			if !proto.Equal(read.Msg.Settings, after.Msg.Settings) {
				t.Fatal("invalid prefix changed stored settings")
			}
		})
	}
	for _, rules := range [][]*v1.AllowedDestination{
		{{Origin: "https://provider.example/path", AllowedCidrs: []string{"10.0.0.0/8"}}},
		{{Origin: "https://user:password@provider.example", AllowedCidrs: []string{"10.0.0.0/8"}}},
		{{Origin: "https://provider.example", AllowedCidrs: []string{"10.0.0.0/8"}}, {Origin: "https://provider.example:443"}},
	} {
		invalidSettings := proto.Clone(read.Msg.Settings).(*v1.InstanceSettings)
		invalidSettings.AllowedDestinations = rules
		_, err := op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: invalidSettings, ExpectedRevision: read.Msg.Settings.Revision}))
		acceptanceCode(t, err, connect.CodeInvalidArgument)
	}

	cleared := proto.Clone(read.Msg.Settings).(*v1.InstanceSettings)
	cleared.AllowedDestinations = nil
	removed, err := op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: cleared, ExpectedRevision: read.Msg.Settings.Revision}))
	acceptanceOK(t, err)
	after, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, err)
	if len(removed.Msg.Settings.AllowedDestinations) != 0 || len(after.Msg.Settings.AllowedDestinations) != 0 {
		t.Fatal("cleared typed rules resurrected legacy approvals")
	}

}

// The resolver is a deterministic DNS fixture; every successful transport still
// dials a pinned address through the production destination-policy code.
type operatorDestinationResolver struct {
	addresses []netip.Addr
	calls     int
}

func (r *operatorDestinationResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return append([]netip.Addr(nil), r.addresses...), nil
}

func operatorDestinationSend(t *testing.T, p inference.DestinationPolicy, url string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.Do(context.Background(), request, func(ctx context.Context, start func(context.Context) error) error { return start(ctx) }, nil)
}

func TestUsageOperatorDestinationCIDRTransport(t *testing.T) {
	f := usageOperatorFixture(t)
	_, client := f.user("operator")
	op := rpc.NewOperatorServiceClient(client, f.url)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://unapproved.example/private", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	get, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, err)
	get.Msg.Settings.AllowedDestinations = []*v1.AllowedDestination{{Origin: remote.URL, AllowedCidrs: []string{"127.0.0.0/8"}}}
	_, err = op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: get.Msg.Settings, ExpectedRevision: get.Msg.Settings.Revision}))
	acceptanceOK(t, err)
	stored, err := f.s.Settings(f.ctx)
	acceptanceOK(t, err)
	// Include the legacy origin projection deliberately: it must not bypass the typed restriction.
	policy := inference.DestinationPolicy{ApprovedOrigins: stored.Destinations}
	for _, rule := range stored.DestinationRules {
		policy.Rules = append(policy.Rules, inference.DestinationRule{Origin: rule.Origin, AllowedCIDRs: rule.AllowedCIDRs})
	}
	for _, test := range []struct {
		name      string
		addresses []netip.Addr
		allowed   bool
	}{
		{"ipv4", []netip.Addr{netip.MustParseAddr("127.0.0.1")}, true},
		{"mapped-ipv6", []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}, true},
		{"outside", []netip.Addr{netip.MustParseAddr("8.8.8.8")}, false},
		{"mixed", []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("8.8.8.8")}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := &operatorDestinationResolver{addresses: test.addresses}
			p := policy
			p.Resolver = resolver
			dials := 0
			p.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials++
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ip, err := netip.ParseAddr(host)
				if err != nil || ip.Unmap() != netip.MustParseAddr("127.0.0.1") {
					return nil, errors.New("transport did not pin allowed address")
				}
				return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
			}
			response, err := operatorDestinationSend(t, p, remote.URL)
			if test.allowed {
				acceptanceOK(t, err)
				response.Body.Close()
				if dials != 1 || resolver.calls != 1 {
					t.Fatal("transport did not use one validated DNS result")
				}
			} else if err == nil || dials != 0 {
				if response != nil {
					response.Body.Close()
				}
				t.Fatal("disallowed DNS answer reached transport")
			}
		})
	}
	resolver := &operatorDestinationResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	policy.Resolver = resolver
	_, err = operatorDestinationSend(t, policy, remote.URL+"/redirect")
	if err == nil || resolver.calls != 1 {
		t.Fatal("redirect was followed or accepted")
	}
}

func TestUsageOperatorDestinationPublicHTTPSNarrowing(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer remote.Close()
	roots := x509.NewCertPool()
	roots.AddCert(remote.Certificate())
	for _, restricted := range []bool{false, true} {
		resolver := &operatorDestinationResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
		dials := 0
		policy := inference.DestinationPolicy{Resolver: resolver, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials++
			return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
		}}
		if restricted {
			policy.Rules = []inference.DestinationRule{{Origin: remote.URL, AllowedCIDRs: []string{"1.1.1.0/24"}}}
		}
		response, err := operatorDestinationSend(t, policy, remote.URL)
		if restricted {
			if err == nil || dials != 0 {
				if response != nil {
					response.Body.Close()
				}
				t.Fatal("public HTTPS bypassed explicit CIDR rule")
			}
		} else {
			acceptanceOK(t, err)
			response.Body.Close()
			if dials != 1 {
				t.Fatal("default public HTTPS stopped working")
			}
		}
	}
}
