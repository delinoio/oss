package github

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkGitHubUsesProxyWithIndependentDestinationTLS(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			origin, roots := outboundtest.TLSOrigin(t, "api.github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer github-fixture-token" || r.Host != "api.github.com" {
					t.Error("authority/credential separation")
				}
				io.WriteString(w, `{"id":17,"node_id":"U_17","login":"fixture-user","type":"User"}`)
			}))
			fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			}
			c := New(fixture.Resolver())
			base := c.http.Transport.(*outbound.Transport).Base
			base.TLSClientConfig.RootCAs = roots
			base.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("direct denied") }
			result, err := c.Identity(context.Background(), []byte("github-fixture-token"))
			if err != nil || result.State != domain.IntegrationIdentityVerified || fixture.Calls.Load() != 1 {
				t.Fatal("GitHub routing", result, err)
			}
			if target := <-fixture.Targets; target != "api.github.com:443" {
				t.Fatal(target)
			}
		})
	}
}
