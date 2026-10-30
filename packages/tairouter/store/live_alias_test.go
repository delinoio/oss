// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/policy"
)

func stateRows(t *testing.T, s *Store, sc core.Scope, kind core.StateKind) (int, int64) {
	t.Helper()
	var count int
	var bytes int64
	if err := s.db.QueryRow("SELECT count(*),coalesce(sum(size),0) FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3", sc.OrganizationID, sc.UserID, kind).Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	return count, bytes
}

func TestLiveAliasAndResponsesIndependentQuotas(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	for i := range 1000 {
		if err := s.PutState(ctx, sc, core.Responses, fmt.Sprintf("response-%04d", i), []byte("response")); err != nil {
			t.Fatal(err)
		}
	}
	count, bytes := stateRows(t, s, sc, core.Responses)
	if count != 1000 {
		t.Fatal(count)
	}
	if err := s.PutState(ctx, sc, core.LiveAlias, "live/key/first", []byte("alias")); err != nil {
		t.Fatal(err)
	}
	if got, n := stateRows(t, s, sc, core.Responses); got != count || n != bytes {
		t.Fatalf("alias charged Responses: %d/%d", got, n)
	}
	for i := range 1000 {
		if _, err := s.GetState(ctx, sc, core.Responses, fmt.Sprintf("response-%04d", i)); err != nil {
			t.Fatalf("response %d lost after alias publication: %v", i, err)
		}
	}
	for i := 1; i < 1000; i++ {
		if err := s.PutState(ctx, sc, core.LiveAlias, fmt.Sprintf("live/key/%04d", i), []byte("alias")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutState(ctx, sc, core.Responses, "response-next", []byte("response")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetState(ctx, sc, core.LiveAlias, "live/key/first"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutState(ctx, sc, core.LiveAlias, "live/other-key/next", []byte("alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetState(ctx, sc, core.LiveAlias, "live/key/first"); !errors.Is(err, ErrContinuation) {
		t.Fatalf("old alias retained: %v", err)
	}
	if got, _ := stateRows(t, s, sc, core.LiveAlias); got != 1000 {
		t.Fatal(got)
	}
	if _, err := s.GetState(ctx, sc, core.Responses, "response-0001"); err != nil {
		t.Fatal(err)
	}
}

func TestLiveAliasAbsoluteDeadlineAndExpiredBudget(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	for _, duration := range []time.Duration{5 * time.Minute, 30 * time.Minute} {
		deadline := time.Now().Add(duration).UTC().Round(time.Microsecond)
		id := fmt.Sprintf("live/key/%d", duration)
		if err := s.putState(ctx, sc, core.LiveAlias, id, []byte("alias"), &deadline, nil); err != nil {
			t.Fatal(err)
		}
		var sqlDeadline time.Time
		if err := s.db.QueryRow("SELECT expires_at FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, core.LiveAlias, id).Scan(&sqlDeadline); err != nil {
			t.Fatal(err)
		}
		if !sqlDeadline.Equal(deadline) {
			t.Fatalf("deadline changed: %v != %v", sqlDeadline, deadline)
		}
	}
	execSQL(t, s, "UPDATE "+s.temp+".states SET expires_at=clock_timestamp()-interval '1 second' WHERE kind=$1 AND id=$2", core.LiveAlias, fmt.Sprintf("live/key/%d", 5*time.Minute))
	if err := s.PutState(ctx, sc, core.LiveAlias, "live/key/new", []byte("alias")); err != nil {
		t.Fatal(err)
	}
	if got, _ := stateRows(t, s, sc, core.LiveAlias); got != 2 {
		t.Fatalf("expired row consumed budget: %d", got)
	}
}

func TestLiveAliasCiphertextBytesStayInTheirDomain(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	_, other := tenant(t, s)
	for _, scope := range []core.Scope{sc, other} {
		for _, kind := range []core.StateKind{core.Responses, core.LiveAlias} {
			for _, id := range []string{"old", "new"} {
				if err := s.PutState(ctx, scope, kind, id, []byte("encrypted payload")); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	var oldSize, newSize int64
	responseCount, responseBytes := stateRows(t, s, sc, core.Responses)
	if err := s.db.QueryRow("SELECT size FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id='old'", sc.OrganizationID, sc.UserID, core.LiveAlias).Scan(&oldSize); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT size FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id='new'", sc.OrganizationID, sc.UserID, core.LiveAlias).Scan(&newSize); err != nil {
		t.Fatal(err)
	}
	if oldSize <= 0 || newSize <= 0 {
		t.Fatal("ciphertext sizes missing")
	}
	if err := s.tx(ctx, func(tx *sql.Tx) error { return s.pruneState(ctx, tx, sc, core.LiveAlias, 1000, newSize) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetState(ctx, sc, core.LiveAlias, "old"); !errors.Is(err, ErrContinuation) {
		t.Fatalf("old ciphertext not evicted: %v", err)
	}
	if _, err := s.GetState(ctx, sc, core.LiveAlias, "new"); err != nil {
		t.Fatal(err)
	}
	if got, bytes := stateRows(t, s, sc, core.Responses); got != responseCount || bytes != responseBytes {
		t.Fatalf("Responses charged: %d/%d", got, bytes)
	}
	if got, _ := stateRows(t, s, other, core.LiveAlias); got != 2 {
		t.Fatalf("other tenant charged: %d", got)
	}
}

func TestMigrateLegacyLiveAliases(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	key, _, err := s.IssueKey(ctx, sc, "creator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutPrivate(ctx, sc, "provider", "account", []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	_, version, err := s.GetPrivateVersion(ctx, sc, "provider", "account")
	if err != nil {
		t.Fatal(err)
	}
	validID := "live/" + key.ID + "/valid"
	deadline := time.Now().Add(5 * time.Minute).UTC().Round(time.Microsecond)
	valid, err := json.Marshal(struct {
		Version int
		Binding inference.LiveBinding
	}{1, inference.LiveBinding{Target: inference.Target{Policy: &policy.Resolved{}, Provider: "account", Model: "model"}, AccountID: "account", UpstreamCallID: "call", ServedCredentialVersion: version, ExpiresAt: deadline}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutState(ctx, sc, core.Responses, "ordinary", []byte("unchanged")); err != nil {
		t.Fatal(err)
	}
	if err = s.PutState(ctx, sc, core.Responses, validID, valid); err != nil {
		t.Fatal(err)
	}
	if err = s.PutState(ctx, sc, core.Responses, "live/"+key.ID+"/malformed", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err = s.PutState(ctx, sc, core.Responses, "live/"+key.ID+"/expired", valid); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, "UPDATE "+s.temp+".states SET value=$1,size=$2 WHERE kind=$3 AND id=$4", func() []byte {
		b, _ := s.keys.Encrypt([]byte(`{"Version":1,"Binding":{"ExpiresAt":"2000-01-01T00:00:00Z"}}`), ownership(sc, "state:responses", "live/"+key.ID+"/expired"))
		return b
	}(), len(valid), core.Responses, "live/"+key.ID+"/expired")
	var before []byte
	if err = s.db.QueryRow("SELECT value FROM "+s.temp+".states WHERE kind=$1 AND id='ordinary'", core.Responses).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var created time.Time
	if err = s.db.QueryRow("SELECT created_at FROM "+s.temp+".states WHERE kind=$1 AND id=$2", core.Responses, validID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err = s.db.QueryRow("SELECT value FROM "+s.temp+".states WHERE kind=$1 AND id='ordinary'", core.Responses).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("ordinary ciphertext changed")
	}
	got, err := s.GetState(ctx, sc, core.LiveAlias, validID)
	if err != nil || string(got) != string(valid) {
		t.Fatalf("valid alias changed: %v", err)
	}
	for _, id := range []string{validID, "live/" + key.ID + "/malformed", "live/" + key.ID + "/expired"} {
		if _, err = s.GetState(ctx, sc, core.Responses, id); !errors.Is(err, ErrContinuation) {
			t.Fatalf("legacy alias remained: %s %v", id, err)
		}
	}
	var sqlDeadline time.Time
	if err = s.db.QueryRow("SELECT expires_at FROM "+s.temp+".states WHERE kind=$1 AND id=$2", core.LiveAlias, validID).Scan(&sqlDeadline); err != nil || !sqlDeadline.Equal(deadline) {
		t.Fatalf("migrated deadline: %v %v", sqlDeadline, err)
	}
	var migratedCreated time.Time
	if err = s.db.QueryRow("SELECT created_at FROM "+s.temp+".states WHERE kind=$1 AND id=$2", core.LiveAlias, validID).Scan(&migratedCreated); err != nil || !migratedCreated.Equal(created) {
		t.Fatalf("migration changed order: %v %v", migratedCreated, err)
	}
	if _, err = s.keys.Decrypt(before, ownership(sc, "state:"+string(core.LiveAlias), "ordinary")); err == nil {
		t.Fatal("old ciphertext accepted with new AAD")
	}
	oldAlias, err := s.keys.Encrypt(valid, ownership(sc, "state:"+string(core.Responses), validID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.keys.Decrypt(oldAlias, ownership(sc, "state:"+string(core.LiveAlias), validID)); err == nil {
		t.Fatal("legacy alias accepted under new AAD")
	}
	s.keys, err = core.NewEnvelope("v2", map[string][]byte{"v1": make([]byte, 32), "v2": []byte("12345678901234567890123456789012")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RotateEncryption(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetState(ctx, sc, core.LiveAlias, validID); err != nil || string(got) != string(valid) {
		t.Fatalf("rotated alias: %v", err)
	}
	if got, err = s.GetState(ctx, sc, core.Responses, "ordinary"); err != nil || string(got) != "unchanged" {
		t.Fatalf("rotated response: %v", err)
	}
	if err = s.db.QueryRow("SELECT expires_at FROM "+s.temp+".states WHERE kind=$1 AND id=$2", core.LiveAlias, validID).Scan(&sqlDeadline); err != nil || !sqlDeadline.Equal(deadline) {
		t.Fatalf("rotation changed deadline: %v %v", sqlDeadline, err)
	}
}
