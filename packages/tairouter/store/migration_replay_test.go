// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
)

func TestMigratePreservesExpandedUsageDimensions(t *testing.T) {
	s := testStore(t)
	_, sc := tenant(t, s)
	execSQL(t, s, `INSERT INTO daily_usage(id,org_id,user_id,day,model,provider,outcome,requests,input_tokens,output_tokens,cost,missing_usage,missing_cost)
 VALUES ('a',$1,$2,current_date,'model','provider-a','succeeded',1,0,0,0,0,0),
 ('b',$1,$2,current_date,'model','provider-b','succeeded',1,0,0,0,0,0),
 ('c',$1,$2,current_date,'model','provider-a','failed',1,0,0,0,0,0)`, sc.OrganizationID, sc.UserID)
	// Disaster recovery omits temporary state; migration replay must recreate it
	// without reinstating the obsolete uniqueness rule on durable usage rows.
	execSQL(t, s, "DROP SCHEMA "+s.temp+" CASCADE")
	for range 2 {
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM " + s.schema + ".daily_usage").Scan(&count); err != nil || count != 3 {
		t.Fatalf("usage rows: %d, error: %v", count, err)
	}
	var exists bool
	if err := s.db.QueryRow("SELECT to_regclass($1) IS NOT NULL", s.temp+".provider_states").Scan(&exists); err != nil || !exists {
		t.Fatalf("temporary state was not restored: %v", err)
	}
}
