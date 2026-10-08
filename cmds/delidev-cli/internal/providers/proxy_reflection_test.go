// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInspectionRejectsUnicodeEscapedProxyCredentialBeforeCatalog(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	raw, _ := json.Marshal(credential)
	for _, model := range []string{`fixture-password`, `\u0066ixture-password`, `\u0066\u0069\u0078\u0074\u0075\u0072\u0065\u002d\u0070\u0061\u0073\u0073\u0077\u006f\u0072\u0064`} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"data":[{"id":"%s","name":"Fixture model"}]}`, model)
			}))
			defer server.Close()
			profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 1, Bypass: []domain.ProxyBypass{{Host: "127.0.0.1"}}}, CredentialGeneration: domain.NewID()}
			observation, err := Inspect(context.Background(), localProvider(server.URL), nil, func(context.Context) (domain.NetworkProfile, []byte, error) {
				return profile, append([]byte(nil), raw...), nil
			})
			if err != nil || observation.Problem() == nil || len(observation.Models) != 0 {
				t.Fatal("reflected model reached catalog observation", err)
			}
			encoded, _ := json.Marshal(observation)
			if strings.Contains(string(encoded), credential.Username) || strings.Contains(string(encoded), credential.Password) {
				t.Fatal("protected value entered public observation")
			}
		})
	}
}
