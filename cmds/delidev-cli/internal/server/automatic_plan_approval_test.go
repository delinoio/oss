// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setPlanAutomation(t *testing.T, f *publicationFixture, enabled bool) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.plan-policy", nil, func(tx *store.Tx) (any, error) {
		rows, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 2})
		if err != nil {
			return nil, err
		}
		v := domain.DefaultSettings()
		v.AutomaticPlanApproval = enabled
		id, revision := domain.NewID(), uint64(0)
		if len(rows) > 0 {
			id, revision = rows[0].ID, rows[0].Revision
		}
		return tx.Put(domain.SettingsKind, id, revision, "", "", v)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestAutomaticPlanApprovalRetainsFirstPublicationAndOriginalResponse(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "automatic"}[enabled], func(t *testing.T) {
			f, u, seq := claudeNamedCallbackPublicationFixture(t, domain.ClaudePlanApproval)
			f.registerGrant(t)
			setPlanAutomation(t, f, enabled)
			e := f.event(domain.ExecutionInteractionRequested, seq+1)
			e.Interaction = &u
			request := f.publish(t, e)
			_, v := readPublishedInteraction(t, f, u.ID)
			if enabled {
				if v.PlanApprovalPolicy != domain.PlanApprovalAutomatic || v.ApprovalResponse == nil || v.ApprovalResponse.Input.Claude.Behavior != domain.ClaudeReplyAllow {
					t.Fatalf("automatic response missing: %+v", v)
				}
			} else if v.PlanApprovalPolicy != domain.PlanApprovalManual || v.ApprovalResponse != nil {
				t.Fatal("disabled policy approved")
			}
			original := v.ApprovalResponse
			setPlanAutomation(t, f, !enabled)
			if _, err := f.call(request); err != nil {
				t.Fatal(err)
			}
			_, v = readPublishedInteraction(t, f, u.ID)
			if enabled && (v.ApprovalResponse == nil || v.ApprovalResponse.ID != original.ID) || !enabled && v.ApprovalResponse != nil {
				t.Fatal("receipt replay changed first decision")
			}
		})
	}
}
func TestAutomaticPlanApprovalOnlyTypedPlanRequests(t *testing.T) {
	for _, kind := range []domain.ClaudeInteractionKind{domain.ClaudeToolPermission, domain.ClaudeUserQuestion} {
		f, u, seq := claudeNamedCallbackPublicationFixture(t, kind)
		setPlanAutomation(t, f, true)
		e := f.event(domain.ExecutionInteractionRequested, seq+1)
		e.Interaction = &u
		f.publish(t, e)
		_, v := readPublishedInteraction(t, f, u.ID)
		if v.PlanApprovalPolicy != "" || v.ApprovalResponse != nil {
			t.Fatal("ordinary interaction automated")
		}
	}
	response, ok := nativePlanApproval(domain.ExecutionInteraction{Type: domain.NativeApprovalInteraction, Grok: &domain.GrokInteractionRequest{Event: domain.GrokToolEvent{Method: domain.GrokPlanMethod}}})
	if !ok || response.Grok.Outcome != domain.GrokPlanApproved {
		t.Fatal("original Grok plan response changed")
	}
}

func TestAutomaticPlanApprovalSharesManualAdmissionAndKeepsFailedScopeManual(t *testing.T) {
	for _, grant := range []bool{false, true} {
		f, u, seq := claudeNamedCallbackPublicationFixture(t, domain.ClaudePlanApproval)
		if grant {
			f.registerGrant(t)
		}
		setPlanAutomation(t, f, true)
		e := f.event(domain.ExecutionInteractionRequested, seq+1)
		e.Interaction = &u
		f.publish(t, e)
		row, v := readPublishedInteraction(t, f, u.ID)
		if !grant {
			if v.PlanApprovalPolicy != domain.PlanApprovalUnavailable || v.ApprovalResponse != nil {
				t.Fatal("failed authority approved")
			}
			continue
		}
		original := v.ApprovalResponse.ID
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.manual-plan-race", nil, func(tx *store.Tx) (any, error) {
			return f.service.acceptApprovalResponse(tx, domain.NewID(), u.ID, row.Revision, domain.ApprovalResponseInput{Claude: &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}})
		})
		if err == nil {
			t.Fatal("manual response duplicated automatic response")
		}
		_, after := readPublishedInteraction(t, f, u.ID)
		if after.ApprovalResponse.ID != original {
			t.Fatal("race replaced original response")
		}
	}
}

func TestAutomaticPlanApprovalPublicationRollbackAndRestartReceipt(t *testing.T) {
	f, u, seq := claudeNamedCallbackPublicationFixture(t, domain.ClaudePlanApproval)
	f.registerGrant(t)
	setPlanAutomation(t, f, false)
	e := f.event(domain.ExecutionInteractionRequested, seq+1)
	e.Interaction = &u
	request := f.publish(t, e)
	original, _ := readPublishedInteraction(t, f, u.ID)
	injected := errors.New("fixture aborts publication transaction")
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.plan-rollback", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.InteractionKind, u.ID)
		if err != nil {
			return nil, err
		}
		v, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil {
			return nil, err
		}
		v.PlanApprovalPolicy = domain.PlanApprovalAutomatic
		if _, err = tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, v); err != nil {
			return nil, err
		}
		if err = f.service.decideAutomaticPlanApproval(tx, u.ID); err != nil {
			return nil, err
		}
		return nil, injected
	})
	if err == nil || domain.SafeError(err).Code != domain.Internal {
		t.Fatalf("rollback failure: %v", err)
	}
	row, v := readPublishedInteraction(t, f, u.ID)
	if row.Revision != original.Revision || v.ApprovalResponse != nil || v.PlanApprovalPolicy != domain.PlanApprovalManual {
		t.Fatal("failed publication retained response")
	}
	// Simulate lost acknowledgement and a restart after policy changed. The receipt
	// retains the first manual decision and never queues a retrospective response.
	setPlanAutomation(t, f, true)
	f.service.executionAuthority.close()
	f.http.Close()
	root := f.service.Store.Root()
	if err = f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	f.service = &Service{Store: reopened, Identity: f.service.Identity, logger: f.service.logger, accountSecrets: f.service.accountSecrets}
	f.http = httptest.NewServer(f.service.Handler(nil, true))
	f.client = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.http.URL)
	reply, err := f.call(request)
	if err != nil || !reply.Msg.Replayed {
		t.Fatalf("restart receipt failed: %v", err)
	}
	_, v = readPublishedInteraction(t, f, u.ID)
	if v.PlanApprovalPolicy != domain.PlanApprovalManual || v.ApprovalResponse != nil {
		t.Fatal("restart changed manual policy")
	}
}
