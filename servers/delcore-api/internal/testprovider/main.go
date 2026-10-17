// SPDX-License-Identifier: Apache-2.0
// Test-only OIDC discovery fixture for isolated OCI acceptance tests.
package main

import (
	"io"
	"net/http"
)

func main() {
	http.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"issuer":"http://127.0.0.1:8099","jwks_uri":"http://127.0.0.1:8099/keys","authorization_endpoint":"http://127.0.0.1:8099/auth","token_endpoint":"http://127.0.0.1:8099/token","id_token_signing_alg_values_supported":["RS256"]}`)
	})
	http.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"keys":[]}`)
	})
	if err := http.ListenAndServe("127.0.0.1:8099", nil); err != nil {
		panic(err)
	}
}
