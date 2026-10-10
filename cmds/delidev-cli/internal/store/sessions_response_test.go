// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestSessionsAwaitingResponseProjection(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	expected := map[domain.ID]bool{}
	ids := map[string]domain.ID{}
	interactions := map[string][]domain.ID{}
	_, err := s.Mutate(ctx, domain.NewID(), "sessions.response.fixture", nil, func(tx *Tx) (any, error) {
		for _, name := range []string{"question", "permission", "manual-plan", "automatic-plan-unavailable", "automatic-plan-answered", "answered", "approved", "closed", "prior-execution", "archived", "archiving", "idle", "several", "inbox-only"} {
			id, execution := domain.NewID(), domain.NewID()
			ids[name] = id
			session := domain.Session{Archive: domain.NotArchived, ActiveExecutionID: execution}
			switch name {
			case "archived":
				session.Archive = domain.Archived
			case "archiving":
				session.Archive = domain.ArchivePending
			case "idle":
				session.ActiveExecutionID = ""
			}
			if _, err := tx.Put(domain.SessionKind, id, 0, id, "", session); err != nil {
				return nil, err
			}
			expected[id] = name == "question" || name == "permission" || name == "manual-plan" || name == "automatic-plan-unavailable" || name == "several"
			if name == "inbox-only" {
				if _, err := tx.Put(domain.InboxKind, domain.NewID(), 0, id, "", map[string]any{"unread": true}); err != nil {
					return nil, err
				}
				continue
			}
			count := 1
			if name == "several" {
				count = 3
			}
			for n := 0; n < count; n++ {
				interaction := domain.ExecutionInteraction{Type: domain.UserQuestionInteraction, ExecutionID: execution, Closure: domain.InteractionOpen}
				switch name {
				case "manual-plan":
					interaction.PlanApprovalPolicy = domain.PlanApprovalManual
				case "automatic-plan-unavailable":
					interaction.PlanApprovalPolicy = domain.PlanApprovalUnavailable
				case "automatic-plan-answered":
					interaction.PlanApprovalPolicy = domain.PlanApprovalAutomatic
				}
				if name == "permission" || name == "manual-plan" || name == "automatic-plan-unavailable" || name == "automatic-plan-answered" {
					interaction.Type = domain.NativeApprovalInteraction
				}
				switch name {
				case "automatic-plan-answered", "approved":
					interaction.ApprovalResponse = &domain.ApprovalResponse{ID: domain.NewID()}
				case "answered":
					interaction.Response = &domain.QuestionResponse{ID: domain.NewID()}
				case "closed":
					interaction.Closure = domain.InteractionNativeClosed
				case "prior-execution":
					interaction.ExecutionID = domain.NewID()
				}
				interactionID := domain.NewID()
				interactions[name] = append(interactions[name], interactionID)
				if _, err := tx.Put(domain.InteractionKind, interactionID, 0, id, "", interaction); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		after := domain.ID("")
		seen := map[domain.ID]bool{}
		for {
			rows, more, err := s.Sessions(ctx, SessionFilter{IncludeArchived: true, After: after, Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				var data map[string]any
				if json.Unmarshal(row.Data, &data) != nil {
					t.Fatal("invalid response JSON")
				}
				value, ok := data["awaiting_user_response"].(bool)
				if !ok || value != expected[row.ID] {
					t.Fatalf("wrong literal predicate: %s %v", row.ID, data)
				}
				if seen[row.ID] {
					t.Fatal("pagination repeated row")
				}
				seen[row.ID] = true
				after = row.ID
				err = s.Read(ctx, func(tx *Tx) error {
					stored, e := tx.Get(domain.SessionKind, row.ID)
					if e != nil {
						return e
					}
					var retained map[string]any
					json.Unmarshal(stored.Data, &retained)
					if _, found := retained["awaiting_user_response"]; found || stored.Revision != row.Revision || stored.UpdatedAt != row.UpdatedAt {
						t.Fatal("projection changed retained record")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if !more {
				break
			}
		}
		if len(seen) != len(expected) {
			t.Fatal("projection changed page inventory")
		}
	}
	check()
	_, after, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil || before != after {
		t.Fatal("list projection changed resource revisions")
	}
	for n, id := range interactions["several"] {
		_, err = s.Mutate(ctx, domain.NewID(), "sessions.response.answer", nil, func(tx *Tx) (any, error) {
			row, e := tx.Get(domain.InteractionKind, id)
			if e != nil {
				return nil, e
			}
			var value domain.ExecutionInteraction
			if e = json.Unmarshal(row.Data, &value); e != nil {
				return nil, e
			}
			value.Response = &domain.QuestionResponse{ID: domain.NewID()}
			_, e = tx.Put(domain.InteractionKind, id, row.Revision, row.SessionID, row.ProjectID, value)
			return nil, e
		})
		if err != nil {
			t.Fatal(err)
		}
		expected[ids["several"]] = n < len(interactions["several"])-1
		check()
	}
	if err = s.Read(ctx, func(tx *Tx) error {
		v, e := tx.Overview(nil, time.Now().UTC())
		if e == nil && v.PendingInteractions != 4 {
			t.Fatalf("Overview predicate diverged: %d", v.PendingInteractions)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsAwaitingProjectionPreservesByteBound(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	project := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "sessions.response.bound", nil, func(tx *Tx) (any, error) {
		for n := 0; n < 4; n++ {
			id := domain.NewID()
			if _, e := tx.Put(domain.SessionKind, id, 0, id, project, map[string]any{"archive": "active", "padding": strings.Repeat("x", 900<<10)}); e != nil {
				return nil, e
			}
		}
		id := domain.NewID()
		_, e := tx.Put(domain.SessionKind, id, 0, id, "", map[string]any{"archive": "active"})
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, more, err := s.Sessions(ctx, SessionFilter{ProjectID: project, Limit: MaxPage})
	if err != nil || !more || len(rows) != 3 {
		t.Fatal("bounded project page changed", len(rows), more, err)
	}
	bytes := 0
	for _, r := range rows {
		bytes += len(r.Data)
		if r.ProjectID != project {
			t.Fatal("project scope escaped")
		}
	}
	if bytes > 3<<20 {
		t.Fatal("projection bytes escaped existing page bound")
	}
	next, more, err := s.Sessions(ctx, SessionFilter{ProjectID: project, After: rows[len(rows)-1].ID, Limit: MaxPage})
	if err != nil || more || len(next) != 1 {
		t.Fatal("continuation changed", len(next), more, err)
	}
}
