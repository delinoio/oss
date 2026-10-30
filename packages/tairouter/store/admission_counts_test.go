// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func TestAdmissionCountsExcludeReleasedHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	key, raw, err := s.IssueKey(ctx, sc, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, `UPDATE settings SET rpm=2,concurrency=1`)
	execSQL(t, s, `INSERT INTO admissions(id,org_id,user_id,key_id,generation,instance_id,created_at,released)
 SELECT 'history-'||i,$1,$2,$3,$4,$5,clock_timestamp()-interval '30 days',true FROM generate_series(1,10000) i`, sc.OrganizationID, sc.UserID, key.ID, sc.Generation, s.instanceID)
	execSQL(t, s, `INSERT INTO admissions(id,org_id,user_id,key_id,generation,instance_id,created_at,released)
 VALUES ('old-active',$1,$2,$3,$4,$5,clock_timestamp()-interval '1 day',false),
 ('recent-released',$1,$2,$3,$4,$5,clock_timestamp(),true)`, sc.OrganizationID, sc.UserID, key.ID, sc.Generation, s.instanceID)
	execSQL(t, s, "ANALYZE "+s.schema+".admissions")
	var plan string
	err = s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+admissionCountsQuery, sc.OrganizationID, sc.UserID).Scan(&plan)
	})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []struct{ Plan map[string]any }
	if err = json.Unmarshal([]byte(plan), &nodes); err != nil {
		t.Fatal(err)
	}
	scanned := float64(0)
	var walk func(map[string]any)
	walk = func(n map[string]any) {
		if n["Relation Name"] == "admissions" {
			scanned += n["Actual Rows"].(float64) * n["Actual Loops"].(float64)
		}
		if children, ok := n["Plans"].([]any); ok {
			for _, child := range children {
				walk(child.(map[string]any))
			}
		}
	}
	walk(nodes[0].Plan)
	if scanned > 2 {
		t.Fatalf("admission count scanned released history: %.0f rows", scanned)
	}
	var limit *LimitError
	if _, err = s.Admit(ctx, raw); !errors.As(err, &limit) {
		t.Fatalf("old unreleased admission must retain its slot: %v", err)
	}
	execSQL(t, s, `UPDATE admissions SET released=true WHERE id='old-active'`)
	lease, err := s.Admit(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, raw); !errors.As(err, &limit) || limit.RetryAfter <= 0 {
		t.Fatalf("released recent requests must still count toward RPM: %v", err)
	}
	var rows int
	if err = s.db.QueryRow("SELECT count(*) FROM " + s.schema + ".admissions WHERE id LIKE 'history-%'").Scan(&rows); err != nil || rows != 10000 {
		t.Fatalf("history IDs were not retained: %d %v", rows, err)
	}
}
