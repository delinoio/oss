// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func retainFixtureDiagnostics(t *testing.T, f *proxyFixture) *[]domain.RequestDiagnostic {
	t.Helper()
	var retained []domain.RequestDiagnostic
	f.authority.scope.Harness = domain.ClaudeCode
	f.authority.publishDiagnostic = func(_ context.Context, value domain.RequestDiagnostic) error {
		if err := value.Validate(); err != nil {
			return err
		}
		// Snapshot mutable optional fields as the actual persistence boundary does.
		raw, _ := json.Marshal(value)
		var copy domain.RequestDiagnostic
		if err := json.Unmarshal(raw, &copy); err != nil {
			return err
		}
		retained = append(retained, copy)
		return nil
	}
	return &retained
}

func TestRequestDiagnosticRetainsCredentialFailureBeforeAnyHTTPAttempt(t *testing.T) {
	f := newProxyFixture(t, domain.AnthropicMessages, []Operation{MessageCreate}, func(http.ResponseWriter, *http.Request) {
		t.Error("credential failure reached the provider")
	})
	retained := retainFixtureDiagnostics(t, f)
	f.authority.keyError = domain.Fail(domain.Unavailable, "PRIVATE_CREDENTIAL_FAILURE", "")
	response, raw, err := f.request(t, "/messages", `{"model":"fixed-model","messages":[{"role":"user","content":"PRIVATE_BODY"}],"max_tokens":32,"service_tier":"auto","output_config":{"effort":"high"}}`, func(r *http.Request) {
		r.Header.Del("Authorization")
		r.Header.Set("x-api-key", fixtureToken)
		r.Header.Set("X-Client-Request-Id", "req_private")
	})
	if err != nil || response.StatusCode != http.StatusServiceUnavailable || f.calls.Load() != 0 || f.authority.keys.Load() != 1 || f.authority.releases.Load() != 1 || len(*retained) != 2 {
		t.Fatal("credential failure lost original invocation", err, len(*retained))
	}
	initial, terminal := (*retained)[0], (*retained)[1]
	if initial.Revision != 0 || initial.State != domain.DiagnosticInProgress || terminal.Revision != 1 || terminal.ID != initial.ID || terminal.State != domain.RequestDiagnosticFailed || terminal.ErrorCode != domain.Unavailable || terminal.FinishedAt == nil || terminal.DurationMS == nil {
		t.Fatal("credential failure lost metadata settlement", terminal)
	}
	for _, value := range *retained {
		if value.HTTPAttempted == nil || *value.HTTPAttempted || value.RequestedEffort != nil || value.RequestedServiceTier != nil || value.NativeRequestID != "" || value.HTTPStatus != nil {
			t.Fatal("unguarded request fields or provider attempt were manufactured", value)
		}
	}
	encoded, _ := json.Marshal(*retained)
	for _, secret := range []string{"PRIVATE_BODY", "PRIVATE_CREDENTIAL_FAILURE", "req_private", fixtureToken} {
		if strings.Contains(string(encoded), secret) || strings.Contains(string(raw), secret) || strings.Contains(f.logs.String(), secret) {
			t.Fatal("private failure input escaped diagnostics")
		}
	}
}

func TestRequestDiagnosticReadsClaudeUsageTierInJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			f := newProxyFixture(t, domain.AnthropicMessages, []Operation{MessageCreate}, func(w http.ResponseWriter, _ *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_original\",\"usage\":{\"service_tier\":\"priority\"}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"msg_original","type":"message","role":"assistant","content":[{"type":"text","text":"result"}],"stop_reason":"end_turn","service_tier":"standard","usage":{"service_tier":"priority"}}`)
				}
			})
			retained := retainFixtureDiagnostics(t, f)
			body := fmt.Sprintf(`{"model":"fixed-model","messages":[{"role":"user","content":"fixture"}],"max_tokens":32,"stream":%t,"service_tier":"auto","output_config":{"effort":"high"}}`, stream)
			response, _, err := f.request(t, "/messages", body, func(r *http.Request) {
				r.Header.Del("Authorization")
				r.Header.Set("x-api-key", fixtureToken)
				r.Header.Set("X-Client-Request-Id", "req_original")
			})
			if err != nil || response.StatusCode != http.StatusOK || len(*retained) != 4 || f.calls.Load() != 1 {
				t.Fatal("request did not retain one guarded HTTP attempt", err, len(*retained))
			}
			initial, guarded, terminal := (*retained)[0], (*retained)[1], (*retained)[3]
			if initial.RequestedEffort != nil || initial.RequestedServiceTier != nil || initial.NativeRequestID != "" || guarded.RequestedEffort == nil || *guarded.RequestedEffort != "high" || guarded.RequestedServiceTier == nil || *guarded.RequestedServiceTier != "auto" || guarded.NativeRequestID != "req_original" || *guarded.HTTPAttempted {
				t.Fatal("settings were retained before the protected guard", initial, guarded)
			}
			if terminal.State != domain.DiagnosticSucceeded || terminal.EffectiveServiceTier == nil || *terminal.EffectiveServiceTier != "priority" || terminal.NativeResponseID != "msg_original" || terminal.HTTPAttempted == nil || !*terminal.HTTPAttempted {
				t.Fatal("Claude usage tier was lost or read from the wrong field", terminal)
			}
		})
	}
}
