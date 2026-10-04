// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type deliveryCancellationWriter struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	stream   bool
	complete bool
}

func (w *deliveryCancellationWriter) SetReadDeadline(time.Time) error  { return nil }
func (w *deliveryCancellationWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *deliveryCancellationWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if err == nil && (!w.stream || strings.Contains(string(p), "message_stop")) {
		w.complete = true
		w.cancel()
	}
	return n, err
}

func TestRequestDiagnosticPreservesDeliveryOutcomeBeforeCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/complete=%t", stream, complete), func(t *testing.T) {
				f := newProxyFixture(t, domain.AnthropicMessages, []Operation{MessageCreate}, func(w http.ResponseWriter, r *http.Request) {
					if !complete {
						fence := r.Context().Done()
						<-fence
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_original\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"msg_original","type":"message","content":[],"stop_reason":"end_turn"}`)
					}
				})
				retained := retainFixtureDiagnostics(t, f)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if !complete {
					// Cancel at the retained send claim, before a complete delivery.
					publish := f.authority.publishDiagnostic
					f.authority.publishDiagnostic = func(ctx context.Context, value domain.RequestDiagnostic) error {
						err := publish(ctx, value)
						if value.State == domain.DiagnosticInProgress && value.HTTPAttempted != nil && *value.HTTPAttempted {
							cancel()
						}
						return err
					}
				}
				w := &deliveryCancellationWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, stream: stream}
				req := httptest.NewRequest(http.MethodPost, Prefix+"/messages", strings.NewReader(fmt.Sprintf(`{"model":"fixed-model","messages":[],"max_tokens":32,"stream":%t}`, stream))).WithContext(ctx)
				req.RemoteAddr = "127.0.0.1:12345"
				req.Header.Set("x-api-key", fixtureToken)
				req.Header.Set("Content-Type", "application/json")
				f.server.Config.Handler.ServeHTTP(w, req)
				if len(*retained) != 4 || f.authority.releases.Load() != 1 || ctx.Err() != context.Canceled {
					t.Fatal("original cancellation or diagnostic lifetime lost", len(*retained))
				}
				terminal := (*retained)[3]
				if complete {
					if !w.complete || f.calls.Load() != 1 || terminal.State != domain.DiagnosticSucceeded || terminal.ErrorCode != "" || terminal.HTTPStatus == nil || *terminal.HTTPStatus != 200 || terminal.NativeResponseID != "msg_original" {
						t.Fatal("fully delivered success was replaced by cancellation", terminal)
					}
				} else if terminal.State != domain.DiagnosticCanceled || terminal.ErrorCode != domain.Canceled {
					t.Fatal("incomplete delivery lost cancellation", terminal)
				}
			})
		}
	}
}

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
	for _, code := range []domain.Code{domain.Unavailable, domain.ConfirmationRequired} {
		t.Run(string(code), func(t *testing.T) {
			f := newProxyFixture(t, domain.AnthropicMessages, []Operation{MessageCreate}, func(http.ResponseWriter, *http.Request) {
				t.Error("credential failure reached the provider")
			})
			retained := retainFixtureDiagnostics(t, f)
			f.authority.keyError = domain.Fail(code, "PRIVATE_CREDENTIAL_FAILURE", "")
			response, raw, err := f.request(t, "/messages", `{"model":"fixed-model","messages":[{"role":"user","content":"PRIVATE_BODY"}],"max_tokens":32,"service_tier":"auto","output_config":{"effort":"high"}}`, func(r *http.Request) {
				r.Header.Del("Authorization")
				r.Header.Set("x-api-key", fixtureToken)
				r.Header.Set("X-Client-Request-Id", "req_private")
			})
			if err != nil || response.StatusCode != errorStatus(f.authority.keyError) || f.calls.Load() != 0 || f.authority.keys.Load() != 1 || f.authority.releases.Load() != 1 || len(*retained) != 2 {
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
		})
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
			body := fmt.Sprintf(`{"model":"fixed-model","messages":[{"role":"user","content":"fixture"}],"max_tokens":32,"stream":%t,"service_tier":"standard_only","output_config":{"effort":"high"}}`, stream)
			response, _, err := f.request(t, "/messages", body, func(r *http.Request) {
				r.Header.Del("Authorization")
				r.Header.Set("x-api-key", fixtureToken)
				r.Header.Set("X-Client-Request-Id", "req_original")
			})
			if err != nil || response.StatusCode != http.StatusOK || len(*retained) != 4 || f.calls.Load() != 1 {
				t.Fatal("request did not retain one guarded HTTP attempt", err, len(*retained))
			}
			initial, guarded, terminal := (*retained)[0], (*retained)[1], (*retained)[3]
			if initial.RequestedEffort != nil || initial.RequestedServiceTier != nil || initial.NativeRequestID != "" || guarded.RequestedEffort == nil || *guarded.RequestedEffort != "high" || guarded.RequestedServiceTier == nil || *guarded.RequestedServiceTier != "standard_only" || guarded.NativeRequestID != "req_original" || *guarded.HTTPAttempted {
				t.Fatal("settings were retained before the protected guard", initial, guarded)
			}
			if terminal.State != domain.DiagnosticSucceeded || terminal.EffectiveServiceTier == nil || *terminal.EffectiveServiceTier != "priority" || terminal.NativeResponseID != "msg_original" || terminal.HTTPAttempted == nil || !*terminal.HTTPAttempted {
				t.Fatal("Claude usage tier was lost or read from the wrong field", terminal)
			}
		})
	}
}
