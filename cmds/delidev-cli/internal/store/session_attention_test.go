package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func sessionAttention(t *testing.T, r Record) bool {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(r.Data, &document); err != nil {
		t.Fatal(err)
	}
	waiting, ok := document["awaiting_user_response"].(bool)
	if !ok {
		t.Fatal("session response omitted its boolean projection")
	}
	return waiting
}

func TestSessionAttentionUsesCurrentUnansweredRequestsAcrossHarnesses(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	project := domain.NewID()
	want := map[domain.ID]bool{}
	_, err := s.Mutate(ctx, domain.NewID(), "attention.fixture", nil, func(tx *Tx) (any, error) {
		for _, harness := range []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild} {
			for _, scenario := range []string{"question", "permission", "manual-plan", "unavailable-plan", "automatic-plan", "answered", "approved", "closed", "prior-execution", "archived", "archiving", "idle", "inbox-only"} {
				id, execution := domain.NewID(), domain.NewID()
				session := domain.Session{Name: string(harness) + "/" + scenario, Archive: domain.NotArchived, ActiveExecutionID: execution}
				if scenario == "archived" {
					session.Archive = domain.Archived
				}
				if scenario == "archiving" {
					session.Archive = domain.ArchivePending
				}
				if scenario == "idle" {
					session.ActiveExecutionID = ""
				}
				if _, err := tx.Put(domain.SessionKind, id, 0, id, project, session); err != nil {
					return nil, err
				}
				want[id] = scenario == "question" || scenario == "permission" || scenario == "manual-plan" || scenario == "unavailable-plan"
				// SQL predicate fixtures retain original harness/family metadata.
				// Native request validation and delivery have independent owners.
				interaction := map[string]any{"execution_id": execution, "closure": domain.InteractionOpen, "type": domain.UserQuestionInteraction, "harness": harness}
				switch scenario {
				case "permission", "manual-plan", "unavailable-plan", "automatic-plan", "approved":
					interaction["type"] = domain.NativeApprovalInteraction
				}
				switch scenario {
				case "manual-plan":
					interaction["plan_approval_policy"] = domain.PlanApprovalManual
				case "unavailable-plan":
					interaction["plan_approval_policy"] = domain.PlanApprovalUnavailable
				case "automatic-plan":
					interaction["plan_approval_policy"] = domain.PlanApprovalAutomatic
					interaction["approval_response"] = map[string]any{"id": domain.NewID()}
				case "answered":
					interaction["response"] = map[string]any{"id": domain.NewID()}
				case "approved":
					interaction["approval_response"] = map[string]any{"id": domain.NewID()}
				case "closed":
					interaction["closure"] = domain.InteractionNativeClosed
				case "prior-execution":
					interaction["execution_id"] = domain.NewID()
				case "idle":
					interaction["execution_id"] = ""
				}
				if scenario != "inbox-only" {
					if _, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, id, project, interaction); err != nil {
						return nil, err
					}
				}
				if _, err := tx.Put(domain.InboxKind, domain.NewID(), 0, id, project, map[string]any{"read_state": "unread"}); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, cursor, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil {
		t.Fatal(err)
	}
	receipts := func() string {
		var data string
		if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(group_concat(id||':'||digest||':'||hex(result)), '') FROM receipts").Scan(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	beforeReceipts := receipts()
	for _, scope := range []domain.ID{"", project, domain.NewID()} {
		count := 0
		var after domain.ID
		for {
			page, more, err := s.Sessions(ctx, SessionFilter{ProjectID: scope, IncludeArchived: true, After: after, Limit: 7})
			if err != nil {
				t.Fatal(err)
			}
			if len(page) > 7 {
				t.Fatal("attention projection exceeded page limit")
			}
			for _, record := range page {
				if record.ID <= after || sessionAttention(t, record) != want[record.ID] {
					t.Fatalf("wrong attention/order for %s: %s", record.ID, record.Data)
				}
				after = record.ID
				count++
			}
			if !more {
				break
			}
		}
		if scope != "" && scope != project && count != 0 || (scope == "" || scope == project) && count != len(want) {
			t.Fatalf("wrong scoped page coverage: %d", count)
		}
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		counts, err := tx.Overview(nil, time.Now())
		if err == nil && counts.PendingInteractions != 16 {
			t.Fatalf("Overview predicate drift: %+v", counts)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, afterCursor, err := s.Snapshot(ctx, Filter{Kind: domain.SessionKind, Limit: MaxPage})
	if err != nil || !reflect.DeepEqual(before, after) || cursor != afterCursor || beforeReceipts != receipts() {
		t.Fatal("session attention read changed documents, revisions, events or receipts")
	}
	for _, record := range after {
		if bytes.Contains(record.Data, []byte("awaiting_user_response")) {
			t.Fatal("attention field persisted in Session")
		}
	}
}

func TestSessionAttentionRetainsMultipleRequestsUntilFinalResponseOrClosure(t *testing.T) {
	for _, final := range []string{"response", "closure"} {
		t.Run(final, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			session, execution := domain.NewID(), domain.NewID()
			requests := []domain.ID{domain.NewID(), domain.NewID()}
			_, err := s.Mutate(ctx, domain.NewID(), "attention.multiple", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Archive: domain.NotArchived, ActiveExecutionID: execution}); err != nil {
					return nil, err
				}
				for _, id := range requests {
					if _, err := tx.Put(domain.InteractionKind, id, 0, session, "", map[string]any{"execution_id": execution, "closure": "open"}); err != nil {
						return nil, err
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			check := func(waiting bool) {
				page, more, err := s.Sessions(ctx, SessionFilter{Limit: 1})
				if err != nil || more || len(page) != 1 || page[0].Revision != 1 || sessionAttention(t, page[0]) != waiting {
					t.Fatalf("wrong unchanged-revision attention: %+v, %v", page, err)
				}
			}
			check(true)
			for index, id := range requests {
				_, err = s.Mutate(ctx, domain.NewID(), "attention.answer", nil, func(tx *Tx) (any, error) {
					r, err := tx.Get(domain.InteractionKind, id)
					if err != nil {
						return nil, err
					}
					var interaction map[string]any
					if err := json.Unmarshal(r.Data, &interaction); err != nil {
						return nil, err
					}
					if index == 1 && final == "closure" {
						interaction["closure"] = domain.InteractionNativeClosed
					} else {
						interaction["response"] = map[string]any{"id": domain.NewID()}
					}
					_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, interaction)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				check(index == 0)
			}
		})
	}
}

func TestSessionAttentionIncludesProjectedBytesInPageBoundary(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	_, err := s.Mutate(ctx, domain.NewID(), "attention.bytes", nil, func(tx *Tx) (any, error) {
		for i := 0; i < 8; i++ {
			id := domain.NewID()
			if _, err := tx.Put(domain.SessionKind, id, 0, id, "", map[string]any{"name": fmt.Sprint(i), "archive": "active", "retained": strings.Repeat("x", 600<<10)}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var after domain.ID
	for {
		page, more, err := s.Sessions(ctx, SessionFilter{After: after, Limit: MaxPage})
		if err != nil {
			t.Fatal(err)
		}
		size := 0
		for _, record := range page {
			size += len(record.Data)
			if sessionAttention(t, record) || record.ID <= after {
				t.Fatal("byte page changed order or invented attention")
			}
			after = record.ID
			count++
		}
		if size > 3<<20 {
			t.Fatal("projection exceeded aggregate byte boundary")
		}
		if !more {
			break
		}
	}
	if count != 8 {
		t.Fatal("byte pagination lost sessions")
	}
}

func TestSessionAttentionRejectsProjectionBeyondResourceDocumentLimit(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "attention.document-limit", nil, func(tx *Tx) (any, error) {
		// The original JSON is exactly the existing 1 MiB document limit.
		value := map[string]any{"archive": "active", "retained": strings.Repeat("x", (1<<20)-len(`{"archive":"active","retained":""}`))}
		_, err := tx.Put(domain.SessionKind, id, 0, id, "", value)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Sessions(ctx, SessionFilter{Limit: 1}); err == nil {
		t.Fatal("session projection exceeded the Resource document limit")
	}
}
