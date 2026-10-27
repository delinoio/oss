// SPDX-License-Identifier: Apache-2.0
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelaySearchAndArtifactRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/v1/alpha/search", 200},
		{"GET", "/v1/aiprouter/artifacts/image", 200},
		{"GET", "/v1/opencodex/artifacts/image", 200},
		{"POST", "/v1/search", 404},
		{"GET", "/v1/images/artifacts/image", 404},
		{"POST", "/aiprouter.v1.OperatorService/Settings", 404},
		{"GET", "/v1/aiprouter/artifacts/../management", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			calls := 0
			credentials := &memoryStore{values: map[string]string{credentialID(testProfile()): "fixture-key"}}
			h, err := RelayHandler(testProfile(), credentials, roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != tc.path || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Fatal("relay changed the path or credential")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, "http://127.0.0.1:17843"+tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if (calls == 1) != (tc.status == 200) {
				t.Fatalf("upstream calls: %d", calls)
			}
		})
	}
}
