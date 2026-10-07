package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func diagnosticRead(t *testing.T, f *authorityFixture, body *pb.ListRequestDiagnosticsRequest) *pb.ListRequestDiagnosticsResponse {
	t.Helper()
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.http.URL)
	req := connect.NewRequest(body)
	req.Header().Set("Authorization", "Bearer "+f.service.Identity.Token)
	response, err := client.ListRequestDiagnostics(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func awaitDiagnosticRows(t *testing.T, f *authorityFixture, count int) []*pb.RequestDiagnostic {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := diagnosticRead(t, f, &pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID)}).Records
		finished := 0
		for _, row := range rows {
			if row.State != pb.RequestDiagnosticState_REQUEST_DIAGNOSTIC_STATE_IN_PROGRESS {
				finished++
			}
		}
		if len(rows) == count && finished == count {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("original request diagnostics did not settle")
	return nil
}

func TestRequestDiagnosticsConcurrentSameModelUsesExactIDs(t *testing.T) {
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		id := strings.ReplaceAll(r.Header.Get("X-Client-Request-Id"), "-", "")
		w.Header().Set("X-Request-Id", "req_"+id)
		_, _ = io.WriteString(w, `{"id":"resp_`+id+`","object":"response","status":"completed","service_tier":"priority","output":[]}`)
	}))
	defer upstream.Close()
	f := newAuthorityFixture(t, upstream.URL)
	f.registerGrant(t)
	var wg sync.WaitGroup
	correlations := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := f.request(t, f.token, `{"model":"fixture-model","reasoning":{"effort":"high"},"input":"PROMPT_SECRET_SENTINEL"}`)
			_, _ = io.Copy(io.Discard, response.Body)
			correlations <- response.Header.Get("X-Delidev-Correlation-Id")
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("simultaneous requests did not reach fixture")
		}
	}
	close(release)
	wg.Wait()
	close(correlations)
	rows := awaitDiagnosticRows(t, f, 2)
	want := map[string]bool{}
	for id := range correlations {
		want[id] = true
	}
	for _, row := range rows {
		if !want[row.CorrelationId] || row.Id != row.CorrelationId || row.NativeResponseId != "resp_"+strings.ReplaceAll(row.CorrelationId, "-", "") || row.ProviderRequestId != "req_"+strings.ReplaceAll(row.CorrelationId, "-", "") || row.AccountId != string(f.input.AccountID) || row.DurationMs == nil || row.HttpAttempted == nil || !*row.HttpAttempted || row.RequestedEffort == nil || *row.RequestedEffort != "high" || row.EffectiveEffort != nil || row.EffectiveServiceTier == nil || *row.EffectiveServiceTier != "priority" {
			t.Fatalf("lost exact request metadata: %+v", row)
		}
		delete(want, row.CorrelationId)
	}
	if len(want) != 0 {
		t.Fatal("requests were incorrectly joined")
	}
	page := diagnosticRead(t, f, &pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID), PageSize: 1})
	if len(page.Records) != 1 || page.NextPageToken == "" {
		t.Fatal("missing bounded continuation")
	}
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.http.URL)
	req := connect.NewRequest(&pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID), ExecutionId: string(domain.NewID()), PageSize: 1, PageToken: page.NextPageToken})
	req.Header().Set("Authorization", "Bearer "+f.service.Identity.Token)
	if _, err := client.ListRequestDiagnostics(context.Background(), req); err == nil {
		t.Fatal("cursor crossed execution scope")
	}
	for _, token := range []string{"", f.workerToken} {
		req := connect.NewRequest(&pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID)})
		req.Header().Set("Authorization", "Bearer "+token)
		if _, err := client.ListRequestDiagnostics(context.Background(), req); (err == nil) != (token != "") {
			t.Fatal("unauthorized diagnostics read")
		}
	}
	raw, _ := json.Marshal(rows)
	for _, secret := range []string{"PROMPT_SECRET_SENTINEL", "temporary-upstream-fixture-key", f.token, "fixture-model"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("diagnostics retained private content")
		}
	}
	// Diagnostics never populate or reconstruct the exact native usage ledger.
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	usage, err := f.service.GetUsageSummary(owner, connect.NewRequest(&pb.GetUsageSummaryRequest{SessionId: string(f.input.SessionID)}))
	if err != nil || usage.Msg.Totals.Responses != 0 {
		t.Fatal("proxy traffic became usage", err)
	}
}

func TestRequestDiagnosticsProviderErrorRetryAndCancellation(t *testing.T) {
	started := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"ERROR_SECRET_SENTINEL /private/path user@example.com","code":"rate_limit_exceeded"}}`)
			return
		}
		if call == 2 {
			_, _ = io.WriteString(w, `{"id":"resp_retry","object":"response","output":[]}`)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	f := newAuthorityFixture(t, upstream.URL)
	f.registerGrant(t)
	first := f.request(t, f.token, `{"model":"fixture-model"}`)
	_, _ = io.Copy(io.Discard, first.Body)
	second := f.request(t, f.token, `{"model":"fixture-model"}`)
	_, _ = io.Copy(io.Discard, second.Body)
	rows := awaitDiagnosticRows(t, f, 2)
	if first.StatusCode != 429 || second.StatusCode != 200 || rows[0].ErrorCode != "resource_exhausted" || rows[0].Id == rows[1].Id || rows[1].NativeResponseId != "resp_retry" {
		t.Fatal("retry/error observations collapsed")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, err := http.NewRequest(http.MethodPost, f.http.URL+"/api-proxy/v1/responses", strings.NewReader(`{"model":"fixture-model"}`))
		if err != nil {
			return
		}
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(r)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation request did not start")
	}
	f.service.executionAuthority.cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled relay did not join")
	}
	rows = awaitDiagnosticRows(t, f, 3)
	if rows[2].State != pb.RequestDiagnosticState_REQUEST_DIAGNOSTIC_STATE_CANCELED || rows[2].ErrorCode != "canceled" {
		t.Fatal("lost canceled original attempt")
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "SECRET_SENTINEL") || strings.Contains(string(raw), "user@example.com") || strings.Contains(string(raw), "/private/path") {
		t.Fatal("provider diagnostic content escaped")
	}
}

func TestNativeRequestDiagnosticReceiptReplayAndMissingSettings(t *testing.T) {
	f := newPublicationFixture(t)
	bound := f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	accepted := f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	if response, err := f.call(bound); err != nil || !response.Msg.Replayed {
		t.Fatal("binding receipt replay failed", err)
	}
	if response, err := f.call(accepted); err != nil || !response.Msg.Replayed {
		t.Fatal("acceptance receipt replay failed", err)
	}
	rows := diagnosticRead(t, f.authorityFixture, &pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID)}).Records
	if len(rows) != 1 || rows[0].Revision != 2 || rows[0].Id != string(f.input.TurnRequestID) || rows[0].PublicationRequestId != accepted.Mutation.RequestId || rows[0].NativeThreadId != string(f.thread) || rows[0].NativeTurnId != string(f.turn) || rows[0].EffectiveEffort != nil || rows[0].EffectiveServiceTier != nil || rows[0].HttpAttempted != nil || rows[0].DurationMs != nil {
		t.Fatal("native provenance was guessed or replayed", rows)
	}
	// Changing or deleting configuration cannot relabel event-time attribution.
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.account-label", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, f.input.AccountID)
		if err != nil {
			return nil, err
		}
		account, err := store.Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		account.Alias = "PRIVATE_EMAIL_SENTINEL@example.com"
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows = diagnosticRead(t, f.authorityFixture, &pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID)}).Records
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "PRIVATE_EMAIL") || rows[0].AccountId != string(f.input.AccountID) {
		t.Fatal("diagnostics read mutable private labels")
	}
}

func TestNativeRequestDiagnosticTerminalPreservesUnavailableLatency(t *testing.T) {
	for _, test := range []struct {
		outcome domain.ExecutionOutcome
		code    domain.Code
		state   pb.RequestDiagnosticState
	}{
		{domain.ExecutionSucceeded, "", pb.RequestDiagnosticState_REQUEST_DIAGNOSTIC_STATE_SUCCEEDED},
		{domain.ExecutionFailed, domain.Unavailable, pb.RequestDiagnosticState_REQUEST_DIAGNOSTIC_STATE_FAILED},
		{domain.ExecutionStopped, domain.Canceled, pb.RequestDiagnosticState_REQUEST_DIAGNOSTIC_STATE_CANCELED},
	} {
		t.Run(string(test.outcome), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			event := f.event(domain.ExecutionTurnFinished, 3)
			event.Outcome, event.ProblemCode = test.outcome, test.code
			publication := f.publish(t, event)
			if result, err := f.call(publication); err != nil || !result.Msg.Replayed {
				t.Fatal("terminal receipt replay failed", err)
			}
			rows := diagnosticRead(t, f.authorityFixture, &pb.ListRequestDiagnosticsRequest{SessionId: string(f.input.SessionID)}).Records
			if len(rows) != 1 || rows[0].Revision != 3 || rows[0].State != test.state || rows[0].FinishedAt == nil || rows[0].ErrorCode != string(test.code) || rows[0].DurationMs != nil || rows[0].HttpAttempted != nil || rows[0].NativeTurnId != string(f.turn) {
				t.Fatal("native terminal inferred HTTP latency or lost original provenance", rows)
			}
			retained, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](retained)
			if err != nil {
				t.Fatal(err)
			}
			if session.ActiveExecutionID != f.input.ExecutionID {
				t.Fatal("native terminal diagnostic falsely confirmed owned process cleanup")
			}
		})
	}
}
