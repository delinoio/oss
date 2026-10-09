// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoopbackAuthorityCanonicalDefaultPortRPC(t *testing.T) {
	f := newAuthorityFixture(t, "http://127.0.0.1:46311")
	origins := []string{"http://127.0.0.1", "http://localhost", "http://[::1]", "https://127.0.0.1", "https://localhost", "https://[::1]"}
	handler := f.service.Handler(origins, true)
	send := func(host, origin string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, delidevv1connect.SystemServiceGetStatusProcedure, strings.NewReader("{}"))
		request.Host = host
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Connect-Protocol-Version", "1")
		request.Header.Set("Origin", origin)
		if authenticated {
			request.Header.Set("Authorization", "Bearer "+f.service.Identity.Token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]", "127.0.0.1:80", "localhost:443", "[::1]:46311", "127.0.0.2:46311", "[::ffff:127.0.0.1]:443"} {
		for _, origin := range origins {
			response := send(host, origin, true)
			var status pb.GetStatusResponse
			if response.Code != http.StatusOK || protojson.Unmarshal(response.Body.Bytes(), &status) != nil || status.ServerId != string(f.service.Identity.ServerID) {
				t.Fatalf("host=%s origin=%s status=%d body=%s", host, origin, response.Code, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("exact CORS origin changed")
			}
		}
	}
	for _, host := range []string{"127.1", "2130706433", "0177.0.0.1", "127.0.0.01", "::1", "[::1", "::1]", "[localhost]:80", "[127.0.0.1]:80", "[::1]:", "127.0.0.1:", "localhost:invalid", "localhost:+80", "localhost:-80", "localhost:0", "localhost:65536", "localhost:80:90", "localhost.", "evil.invalid", "192.0.2.1", "192.0.2.1:80", "[2001:db8::1]:443", "user@localhost:80", "127.1:80"} {
		response := send(host, origins[0], true)
		var problem struct {
			Code string `json:"code"`
		}
		if response.Code != http.StatusForbidden || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != "permission_denied" {
			t.Fatalf("host=%s escaped guard: %d %s", host, response.Code, response.Body.String())
		}
	}
	if response := send("localhost", origins[0], false); response.Code != http.StatusUnauthorized {
		t.Fatal("portless authority bypassed authentication", response.Code)
	}
	if response := send("localhost", "https://evil.invalid", true); response.Code != http.StatusForbidden {
		t.Fatal("portless authority broadened CORS", response.Code)
	}
	for i, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		request := httptest.NewRequest(http.MethodOptions, delidevv1connect.SystemServiceGetStatusProcedure, nil)
		request.Host = host
		request.Header.Set("Origin", origins[i])
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "authorization,content-type,connect-protocol-version")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != origins[i] {
			t.Fatal("canonical Connect preflight denied", response.Code)
		}
	}
}
