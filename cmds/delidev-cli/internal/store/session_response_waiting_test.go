// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func waitingProjection(t *testing.T, r Record) bool {
	t.Helper()
	var body map[string]json.RawMessage
	if json.Unmarshal(r.Data, &body) != nil {
		t.Fatal("invalid list projection")
	}
	var waiting bool
	if value, exists := body["awaiting_user_response"]; !exists || json.Unmarshal(value, &waiting) != nil {
		t.Fatal("missing literal boolean projection")
	}
	return waiting
}
func TestSessionResponseWaitingUsesOriginalUnansweredOwnership(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	expected := map[domain.ID]bool{}
	original := map[domain.ID]Record{}
	_, err := s.Mutate(ctx, domain.NewID(), "waiting.fixture", nil, func(tx *Tx) (any, error) {
		for _, harness := range []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild} {
			for _, scenario := range []string{"question", "permission", "manual-plan", "unavailable-plan", "automatic-plan-answered", "answered-open", "approved-open", "closed", "prior-execution", "archived", "idle"} {
				id, execution := domain.NewID(), domain.NewID()
				session := domain.Session{Name: fmt.Sprintf("%s %s", harness, scenario), Archive: domain.NotArchived, ActiveExecutionID: execution}
				if scenario == "archived" {
					session.Archive = domain.Archived
				}
				if scenario == "idle" {
					session.ActiveExecutionID = ""
				}
				r, err := tx.Put(domain.SessionKind, id, 0, id, "", session)
				if err != nil {
					return nil, err
				}
				original[id] = r
				i := domain.ExecutionInteraction{ExecutionID: execution, Type: domain.UserQuestionInteraction, Closure: domain.InteractionOpen}
				switch harness {
				case domain.ClaudeCode:
					i.Claude = &domain.ClaudeInteractionRequest{}
				case domain.OpenCode:
					i.OpenCode = &domain.OpenCodeInteractionRequest{}
				case domain.GrokBuild:
					i.Grok = &domain.GrokInteractionRequest{}
				}
				switch scenario {
				case "permission":
					i.Type = domain.NativeApprovalInteraction
				case "manual-plan":
					i.PlanApprovalPolicy = domain.PlanApprovalManual
				case "unavailable-plan":
					i.PlanApprovalPolicy = domain.PlanApprovalUnavailable
				case "automatic-plan-answered":
					i.PlanApprovalPolicy = domain.PlanApprovalAutomatic
					i.Response = &domain.QuestionResponse{ID: domain.NewID()}
				case "answered-open":
					i.Response = &domain.QuestionResponse{ID: domain.NewID()}
				case "approved-open":
					i.Type = domain.NativeApprovalInteraction
					i.ApprovalResponse = &domain.ApprovalResponse{ID: domain.NewID()}
				case "closed":
					i.Closure = domain.InteractionNativeClosed
				case "prior-execution":
					i.ExecutionID = domain.NewID()
				}
				if _, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, id, "", i); err != nil {
					return nil, err
				}
				expected[id] = scenario == "question" || scenario == "permission" || scenario == "manual-plan" || scenario == "unavailable-plan"
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeReceipts, afterReceipts int
	countReceipts := func(target *int) error {
		return s.Read(ctx, func(tx *Tx) error { return tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM receipts").Scan(target) })
	}
	if err := countReceipts(&beforeReceipts); err != nil {
		t.Fatal(err)
	}
	_, before, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil {
		t.Fatal(err)
	}
	rows, more, err := s.Sessions(ctx, SessionFilter{IncludeArchived: true, Limit: MaxPage})
	if err != nil || more || len(rows) != len(expected) {
		t.Fatal("bounded original page changed", err)
	}
	for _, r := range rows {
		if waitingProjection(t, r) != expected[r.ID] || r.Revision != original[r.ID].Revision {
			t.Fatal("waiting did not follow original current interaction", r.ID)
		}
		persisted, err := s.Get(ctx, domain.SessionKind, r.ID)
		if err != nil || !bytes.Equal(persisted.Data, original[r.ID].Data) || persisted.Revision != original[r.ID].Revision {
			t.Fatal("list persisted presentation metadata or changed revision", err)
		}
	}
	_, after, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil || before != after {
		t.Fatal("list emitted a mutation event", err)
	}
	if err := countReceipts(&afterReceipts); err != nil || afterReceipts != beforeReceipts {
		t.Fatal("list changed original receipts", err)
	}
}

func TestSessionResponseWaitingTracksResponsesWithoutNativeClosureOrInboxReads(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id, execution := domain.NewID(), domain.NewID()
	var requests []Record
	var inbox Record
	_, err := s.Mutate(ctx, domain.NewID(), "waiting.multiple", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, id, 0, id, "", domain.Session{Archive: domain.NotArchived, ActiveExecutionID: execution}); err != nil {
			return nil, err
		}
		for n := 0; n < 2; n++ {
			r, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, id, "", domain.ExecutionInteraction{ExecutionID: execution, Type: domain.UserQuestionInteraction, Closure: domain.InteractionOpen})
			if err != nil {
				return nil, err
			}
			requests = append(requests, r)
		}
		var err error
		inbox, err = tx.Put(domain.InboxKind, domain.NewID(), 0, id, "", map[string]any{"read_state": "unread"})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(expected bool) {
		t.Helper()
		rows, _, err := s.Sessions(ctx, SessionFilter{Limit: 1})
		if err != nil || len(rows) != 1 || waitingProjection(t, rows[0]) != expected {
			t.Fatal("waiting projection mismatch", err)
		}
	}
	check(true)
	for n, r := range requests {
		_, err := s.Mutate(ctx, domain.NewID(), "waiting.answer", n, func(tx *Tx) (any, error) {
			i, err := Decode[domain.ExecutionInteraction](r)
			if err != nil {
				return nil, err
			}
			i.Response = &domain.QuestionResponse{ID: domain.NewID()}
			if _, err := tx.Put(domain.InteractionKind, r.ID, r.Revision, id, "", i); err != nil {
				return nil, err
			}
			if n == 0 {
				_, err = tx.Put(domain.InboxKind, inbox.ID, inbox.Revision, id, "", map[string]any{"read_state": "read"})
			}
			return nil, err
		})
		if err != nil {
			t.Fatal(err)
		}
		check(n == 0)
	}
}

func TestSessionResponseWaitingProjectionPreservesPageBoundsAndCursors(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	project, other := domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "waiting.pages", nil, func(tx *Tx) (any, error) {
		for n := 0; n < 12; n++ {
			id, execution, selected := domain.NewID(), domain.NewID(), project
			if n%2 != 0 {
				selected = other
			}
			if _, err := tx.Put(domain.SessionKind, id, 0, id, selected, map[string]any{"archive": "active", "active_execution_id": execution, "padding": strings.Repeat("x", 512<<10)}); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, id, selected, domain.ExecutionInteraction{ExecutionID: execution, Closure: domain.InteractionOpen}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []domain.ID{"", project} {
		seen := map[domain.ID]bool{}
		after := domain.ID("")
		for {
			rows, more, err := s.Sessions(ctx, SessionFilter{ProjectID: selected, After: after, Limit: 7})
			if err != nil || len(rows) == 0 {
				t.Fatal("original continuation failed", err)
			}
			size := 0
			for _, r := range rows {
				if seen[r.ID] || r.ID <= after || selected != "" && r.ProjectID != selected || !waitingProjection(t, r) {
					t.Fatal("projection changed membership/order")
				}
				seen[r.ID] = true
				after = r.ID
				size += len(r.Data)
			}
			if size > 3<<20 || len(rows) > 7 {
				t.Fatal("projected response exceeded page bound")
			}
			if !more {
				break
			}
		}
		want := 12
		if selected != "" {
			want = 6
		}
		if len(seen) != want {
			t.Fatal("projection lost a continuation", len(seen))
		}
	}
}

func TestSessionResponseWaitingOversizeProjectionFailsWithoutTruncatingSource(t *testing.T) {
	s, _ := openTest(t)
	ctx, id := context.Background(), domain.NewID()
	var original Record
	_, err := s.Mutate(ctx, domain.NewID(), "waiting.document-bound", nil, func(tx *Tx) (any, error) {
		var err error
		original, err = tx.Put(domain.SessionKind, id, 0, id, "", map[string]any{"archive": "active", "padding": strings.Repeat("x", (1<<20)-45)})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Sessions(ctx, SessionFilter{Limit: 1}); err == nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized response-only metadata did not fail the read", err)
	}
	persisted, err := s.Get(ctx, domain.SessionKind, id)
	if err != nil || !bytes.Equal(persisted.Data, original.Data) || persisted.Revision != original.Revision {
		t.Fatal("failed projection changed the original source", err)
	}
}
