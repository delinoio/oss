// SPDX-License-Identifier: Apache-2.0
// Test-only OIDC and payment fixtures for isolated OCI acceptance tests.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
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
	var mu sync.Mutex
	transactions := []map[string]any{}
	http.HandleFunc("/transactions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"data": transactions})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		transaction := map[string]any{"id": "txn_crash_fixture", "status": "completed", "currency_code": "USD", "customer_id": "ctm_crash_fixture", "custom_data": body["custom_data"], "items": body["items"], "details": map[string]any{"totals": map[string]string{"subtotal": "1000"}}}
		mu.Lock()
		transactions = append(transactions, transaction)
		mu.Unlock()
		if os.WriteFile("/tmp/delcore-payment-created", []byte("fixture accepted"), 0600) != nil {
			w.WriteHeader(500)
			return
		}
		// The payment exists before the caller can persist its response. The OCI
		// test kills the caller here and verifies read-only reconciliation on restart.
		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		}
		json.NewEncoder(w).Encode(map[string]any{"data": transaction})
	})
	if err := http.ListenAndServe("127.0.0.1:8099", nil); err != nil {
		panic(err)
	}
}
