// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestCodexApprovalReviewerRequiresCapabilityBeforeDispatch(t *testing.T) {
	f := newFirstDispatchFixture(t)
	f.mutateAgent(t, func(a *domain.Agent) {
		a.Options.ApprovalsReviewer = domain.CodexReviewerAuto
		a.Options.ApprovalPolicy = "on-request"
	})
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported reviewer was dispatched", err)
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.review-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexApprovalReviewV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Decode[domain.Session](f.refresh(t))
	c := before.InitialExecution.Configuration
	if c.Options.ApprovalsReviewer != domain.CodexReviewerAuto || c.ReviewerNativeModel != domain.CodexReviewerNativeModel {
		t.Fatal("reviewer not frozen")
	}
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.ApprovalsReviewer = domain.CodexReviewerUser })
	after, _ := store.Decode[domain.Session](f.refresh(t))
	a, _ := json.Marshal(before.InitialExecution)
	b, _ := json.Marshal(after.InitialExecution)
	if string(a) != string(b) {
		t.Fatal("Agent edit rewrote original review")
	}
}

func TestCodexApprovalReviewerRelayIsBoundedSameAccountAndAttributed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "resp_reviewer_fixture", "model": domain.CodexReviewerNativeModel, "output": []any{}})
	}))
	defer upstream.Close()
	f := newConfiguredAuthorityFixture(t, upstream.URL, func(i *domain.ExecutionJobInput) {
		i.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerAuto
		i.Configuration.Options.ApprovalPolicy = "on-request"
		i.Configuration.ReviewerNativeModel = domain.CodexReviewerNativeModel
		i.ConfigurationDigest, _ = i.Configuration.Digest()
	}, false)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.reviewer-machine", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexApprovalReviewV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	reviewer, err := lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCreate)
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.Scope.Validate() != nil || reviewer.Scope.ModelID != "" || reviewer.Scope.Attribution != domain.BuiltinReviewerAttribution || reviewer.Scope.AccountID != lease.Scope.AccountID || reviewer.Scope.ConnectionID != lease.Scope.ConnectionID {
		t.Fatal("reviewer lost bounded attribution")
	}
	for _, model := range []string{"codex-auto-review", "foreign", "gpt-5.6-luna-extra"} {
		if _, err := lease.BindModel(context.Background(), model, apiproxy.ResponseCreate); err == nil {
			t.Fatal("foreign model allowed")
		}
	}
	if _, err := lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCompact); err == nil {
		t.Fatal("reviewer gained compaction")
	}
	if err := reviewer.ObserveReference(context.Background(), apiproxy.ResponseReference, "original-reviewer-response"); err != nil {
		t.Fatal(err)
	}
	if err := lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "original-reviewer-response"); err == nil {
		t.Fatal("reviewer response became root history")
	}
	response := f.request(t, f.token, `{"model":"gpt-5.6-luna","input":[]}`)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("reviewer relay failed", response.StatusCode)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		rows, _, e := tx.ListRequestDiagnostics(f.input.SessionID, f.input.ExecutionID, "", 20)
		if e != nil {
			return e
		}
		if len(rows) != 1 || rows[0].Attribution != domain.BuiltinReviewerAttribution || rows[0].ModelID != "" || rows[0].AccountID != f.input.AccountID {
			t.Fatal("reviewer diagnostics borrowed root attribution")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoReviewProgressPublishesDistinctImmutableHistory(t *testing.T) {
	f := newPublicationFixture(t)
	input := f.input
	input.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerAuto
	input.Configuration.Options.ApprovalPolicy = "on-request"
	input.Configuration.ReviewerNativeModel = domain.CodexReviewerNativeModel
	progress := domain.ExecutionProgress{}
	session, err := f.service.Store.Get(context.Background(), domain.SessionKind, input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for index, status := range []domain.AutoReviewStatus{domain.AutoReviewInProgress, domain.AutoReviewDenied} {
		observation := domain.AutoReviewObservation{ReviewID: "original-review", Status: status, StartedAtMS: 10}
		if index > 0 {
			end := int64(11)
			observation.CompletedAtMS = &end
		}
		event := f.event(domain.ExecutionProgressObserved, uint64(index+3))
		event.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.AutoReviewProgress, AutoReview: &observation}}
		_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.review-progress", event, func(tx *store.Tx) (any, error) {
			return nil, publishExecutionProgress(tx, input, session, &progress, event)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !progress.AutoReviews.Closed() || progress.AutoReviews["original-review"].Status != domain.AutoReviewDenied || progress.UnconfirmedResponses != 0 {
		t.Fatal("review conflated with manual acceptance")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("missing independent review history", err)
	}
	for _, row := range rows {
		message, e := store.Decode[domain.ExecutionMessage](row)
		if e != nil || message.Role != domain.ProgressMessage || message.Tool != nil || message.Text != "" || message.Progress.AutoReview == nil || row.Revision != 1 {
			t.Fatal("review became mutable tool/result")
		}
	}
}
