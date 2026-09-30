// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkInferenceRelayUsesExplicitProxy(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Error("credential separation")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[]}`)
			})
			fixture := outboundtest.Connect(t, outboundtest.Address(f.upstream.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(f.upstream.URL))
			}
			f.server.Close()
			f.server = httptest.NewServer(New(f.authority, slog.New(slog.NewJSONHandler(f.logs, nil)), fixture.Resolver()))
			response, _, err := f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
			if err != nil || response.StatusCode != 200 || fixture.Calls.Load() != 1 {
				t.Fatal("inference routing", err)
			}
		})
	}
}
