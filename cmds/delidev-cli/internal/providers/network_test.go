package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkProviderInspectionUsesExplicitProxy(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer provider-fixture-key" {
					t.Error("credential separation")
				}
				io.WriteString(w, `{"data":[{"id":"model-one"}]}`)
			}))
			defer origin.Close()
			fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			}
			p := localProvider(origin.URL)
			p.Authentication = domain.BearerAuth
			result, err := Inspect(context.Background(), p, []byte("provider-fixture-key"), fixture.Resolver())
			if err != nil || result.Problem() != nil || len(result.Models) != 1 || fixture.Calls.Load() != 1 {
				t.Fatal("provider routing", result, err)
			}
		})
	}
}
