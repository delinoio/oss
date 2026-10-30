// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	"github.com/delinoio/oss/servers/tairouter/internal/observability"
)

func TestHostedUsageRecorderSignals(t *testing.T) {
	known := int64(3)
	writeFailure := errors.New("secret-database-error")
	for _, tc := range []struct {
		name          string
		input, output *int64
		priceKnown    bool
		failure       error
		wantUsage     bool
		wantCost      bool
	}{
		{name: "missing input", output: &known, priceKnown: true, wantUsage: true},
		{name: "missing output", input: &known, priceKnown: true, wantUsage: true},
		{name: "missing both", priceKnown: true, wantUsage: true},
		{name: "unknown cost", input: &known, output: &known, wantCost: true},
		{name: "write failure", input: &known, output: &known, priceKnown: true, failure: writeFailure},
		{name: "combined", wantUsage: true, wantCost: true, failure: writeFailure},
		{name: "known receipt", input: &known, output: &known, priceKnown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			telemetry := observability.New(slog.New(slog.NewJSONHandler(&logs, nil)))
			usage := store.Usage{InputTokens: tc.input, OutputTokens: tc.output, PriceKnown: tc.priceKnown, Provider: "secret-provider", Model: "secret-prompt"}
			var records int
			recorder := hostedUsageRecorder(telemetry, func(ctx context.Context, id string, got store.Usage) error {
				records++
				if ctx.Err() != nil || id != "operation-id" || got.InputTokens != usage.InputTokens || got.OutputTokens != usage.OutputTokens || got.PriceKnown != usage.PriceKnown {
					t.Fatalf("receipt changed: id=%q usage=%+v err=%v", id, got, ctx.Err())
				}
				return tc.failure
			})
			var recordErr error
			w := httptest.NewRecorder()
			telemetry.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				recordErr = recorder(r.Context(), "operation-id", usage)
			})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses?secret=secret-response", nil))
			if records != 1 || !errors.Is(recordErr, tc.failure) || (recordErr == nil) != (tc.failure == nil) {
				t.Fatalf("records=%d recording error=%v", records, recordErr)
			}
			metrics := httptest.NewRecorder()
			telemetry.Metrics().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
			for event, want := range map[string]bool{"usage_unknown": tc.wantUsage, "cost_unknown": tc.wantCost, "usage_write_failed": tc.failure != nil, "other": false} {
				found := strings.Contains(metrics.Body.String(), `tairouter_inference_events_total{event="`+event+`"} 1`)
				if found != want {
					t.Errorf("event %s present=%t, want=%t", event, found, want)
				}
			}
			failureLogs := 0
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var entry map[string]any
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "usage recording failed" {
					failureLogs++
					if entry["level"] != "ERROR" || entry["request_id"] != w.Header().Get("X-Request-ID") || entry["request_id"] == "" {
						t.Fatalf("failure log lost level or request identity: %v", entry)
					}
				}
			}
			if failureLogs != 0 && tc.failure == nil || failureLogs != 1 && tc.failure != nil {
				t.Fatalf("failure logs=%d", failureLogs)
			}
			for _, sentinel := range []string{"secret-database-error", "secret-provider", "secret-prompt", "secret-response"} {
				if strings.Contains(metrics.Body.String(), sentinel) || strings.Contains(logs.String(), sentinel) {
					t.Fatalf("sensitive value %q entered telemetry", sentinel)
				}
			}
		})
	}
}

func TestHostedUsageRecorderTransportFailureAndCanceledCleanup(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "record failure", true: "canceled cleanup"}[canceled], func(t *testing.T) {
			var logs bytes.Buffer
			telemetry := observability.New(slog.New(slog.NewJSONHandler(&logs, nil)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if canceled {
					cancel()
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"usage":{"input_tokens":2,"output_tokens":3}}`)
			}))
			defer srv.Close()
			var records int
			tr := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, f func(context.Context) error) error { return f(c) }}
			tr.price = func(u *store.Usage, _ inference.Usage) { u.PriceKnown = !canceled }
			tr.record = hostedUsageRecorder(telemetry, func(c context.Context, _ string, u store.Usage) error {
				records++
				if c.Err() != nil {
					t.Fatal("accounting cleanup context was canceled")
				}
				if canceled && (u.Outcome != store.OutcomeCanceled || u.InputTokens != nil || u.OutputTokens != nil) || !canceled && (u.InputTokens == nil || u.OutputTokens == nil) {
					t.Fatalf("receipt outcome or usage changed: %+v", u)
				}
				return errors.New("secret-receipt-error")
			})
			resp, err := tr.Do(ctx, inference.AdapterRequest{Method: "POST", URL: srv.URL})
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				err = errors.Join(err, resp.Body.Close())
			}
			if records != 1 || !errors.Is(err, inference.ErrUnavailable) {
				t.Fatalf("records=%d exchange error=%v", records, err)
			}
			metrics := httptest.NewRecorder()
			telemetry.Metrics().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
			if !strings.Contains(metrics.Body.String(), `event="usage_write_failed"} 1`) || strings.Contains(metrics.Body.String(), `event="other"}`) || strings.Contains(logs.String(), "secret-receipt-error") {
				t.Fatal("transport accounting signal or redaction failed")
			}
			if canceled && (!strings.Contains(metrics.Body.String(), `event="usage_unknown"} 1`) || !strings.Contains(metrics.Body.String(), `event="cost_unknown"} 1`)) {
				t.Fatal("canceled receipt lost unknown accounting signals")
			}
		})
	}
}

func TestHostedUsageRecorderWithoutTelemetry(t *testing.T) {
	var called bool
	recorder := hostedUsageRecorder(nil, func(context.Context, string, store.Usage) error { called = true; return nil })
	if err := recorder(context.Background(), "operation-id", store.Usage{}); err != nil || !called {
		t.Fatalf("nil telemetry changed recording: called=%t err=%v", called, err)
	}
}
