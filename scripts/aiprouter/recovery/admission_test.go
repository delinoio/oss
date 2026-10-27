// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

func TestStoppedInstanceRecovery(t *testing.T) {
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set AIPROUTER_TEST_DATABASE_URL for admission recovery fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	schema := "admission_" + strings.ReplaceAll(core.ID(), "-", "")[:20]
	db, err := sql.Open("pgx", dsn)
	must(t, err)
	defer db.Close()
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+"_temp CASCADE; DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	}()
	envelope, err := core.NewEnvelope("v1", map[string][]byte{"v1": bytes.Repeat([]byte{1}, 32)})
	must(t, err)
	operator := core.Identity{Issuer: "https://fixture.invalid", Subject: "operator"}
	open := func(id string) *store.Store {
		s, e := store.Open(ctx, dsn, store.Options{Schema: schema, Keys: envelope, InstanceID: id, Operators: []core.Identity{operator}})
		must(t, e)
		t.Cleanup(func() { _ = s.Close() })
		must(t, s.Migrate(ctx))
		return s
	}
	first := open(core.ID())
	second := open(core.ID())
	beforeRegistration, err := first.ListAdmissionInstances(ctx)
	must(t, err)
	for _, instance := range beforeRegistration {
		if instance.ID == first.InstanceID() || instance.ID == second.InstanceID() {
			t.Fatal("migration registered a runtime boot")
		}
	}
	must(t, first.RegisterAdmissionInstance(ctx))
	must(t, second.RegisterAdmissionInstance(ctx))
	user, err := first.RegisterIdentity(ctx, "https://fixture.invalid", "alice", "alice@example.invalid", true)
	must(t, err)
	org, err := first.CreateOrganization(ctx, user.ID, "fixture")
	must(t, err)
	scope, _, err := first.Membership(ctx, org.ID, user.ID)
	must(t, err)
	_, key, err := first.IssueKey(ctx, scope, "fixture", nil)
	must(t, err)
	session, err := first.CreateSession(ctx, user.ID)
	must(t, err)
	settings, err := first.Settings(ctx)
	must(t, err)
	settings.Concurrency = 3
	must(t, first.UpdateSettings(ctx, operator, settings))
	stale, err := first.Admit(ctx, key)
	must(t, err)
	completed, err := first.Admit(ctx, key)
	must(t, err)
	physicalSends := 0
	must(t, first.WithDispatch(ctx, completed, func(context.Context) error {
		physicalSends++
		return nil
	}))
	live, err := second.Admit(ctx, key)
	must(t, err)
	// Age both deadline and creation time. Cleanup must not guess upstream completion.
	_, err = db.ExecContext(ctx, "UPDATE "+schema+".admissions SET created_at=clock_timestamp()-interval '2 days',lease_until=clock_timestamp()-interval '1 day' WHERE id=$1", stale.ID)
	must(t, err)
	must(t, second.Cleanup(ctx))
	_, err = second.Admit(ctx, key)
	var limited *store.LimitError
	if !errors.As(err, &limited) {
		t.Fatal("expected concurrency limit while upstream completion is unconfirmed")
	}
	instances, err := second.ListAdmissionInstances(ctx)
	must(t, err)
	found := false
	for _, instance := range instances {
		if instance.ID == first.InstanceID() {
			found = true
			if instance.ActiveAdmissions != 2 {
				t.Fatal("stale admission missing from status")
			}
		}
	}
	if !found {
		t.Fatal("boot instance missing from status")
	}
	proof, err := second.DrainAdmissionInstance(ctx, first.InstanceID())
	must(t, err)
	if _, err = first.Admit(ctx, key); !errors.Is(err, store.ErrInstanceDraining) {
		t.Fatal("drained boot admitted a request")
	}
	if err = first.CheckDispatch(ctx, stale); !errors.Is(err, store.ErrInstanceDraining) {
		t.Fatalf("drained boot dispatch check: expected ErrInstanceDraining, got %v", err)
	}
	for _, lease := range []store.Lease{stale, completed} {
		err = first.WithDispatch(ctx, lease, func(context.Context) error {
			physicalSends++
			return nil
		})
		if !errors.Is(err, store.ErrInstanceDraining) {
			t.Fatalf("drained boot physical-send gate: expected ErrInstanceDraining, got %v", err)
		}
	}
	if physicalSends != 1 {
		t.Fatal("drained boot invoked a physical-send callback")
	}
	// Completion of the pre-drain send can still record usage and release its slot.
	must(t, first.RecordUsage(ctx, completed, store.Usage{Model: "fixture-completed", Provider: "fixture"}))
	must(t, first.Release(ctx, completed))
	var usageCount int
	must(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+".usage WHERE id=$1", completed.ID).Scan(&usageCount))
	if usageCount != 1 {
		t.Fatal("already-sent completion was not recorded after drain")
	}
	var completionReleased bool
	must(t, db.QueryRowContext(ctx, "SELECT released FROM "+schema+".admissions WHERE id=$1", completed.ID).Scan(&completionReleased))
	if !completionReleased {
		t.Fatal("already-sent completion did not release its slot after drain")
	}
	if _, err = second.ReconcileTerminatedInstance(ctx, first.InstanceID(), proof, false); err == nil {
		t.Fatal("reconciliation accepted no upstream-stop acknowledgment")
	}
	if _, err = second.ReconcileTerminatedInstance(ctx, first.InstanceID(), "wrong-proof", true); err == nil {
		t.Fatal("reconciliation accepted wrong proof")
	}
	replacement, err := second.DrainAdmissionInstance(ctx, first.InstanceID())
	must(t, err)
	if _, err = second.ReconcileTerminatedInstance(ctx, first.InstanceID(), proof, true); err == nil {
		t.Fatal("superseded proof accepted")
	}
	released, err := second.ReconcileTerminatedInstance(ctx, first.InstanceID(), replacement, true)
	must(t, err)
	if released != 1 {
		t.Fatal("reconciliation did not release exactly one instance slot")
	}
	if _, err = second.ReconcileTerminatedInstance(ctx, first.InstanceID(), replacement, true); err == nil {
		t.Fatal("one-use proof replay accepted")
	}
	if err = first.CheckDispatch(ctx, stale); err == nil {
		t.Fatal("terminated boot dispatched again")
	}
	resurrected := open(first.InstanceID())
	if err = resurrected.RegisterAdmissionInstance(ctx); err == nil {
		t.Fatal("reused boot ID was registered again")
	}
	if _, err = resurrected.Admit(ctx, key); err == nil {
		t.Fatal("reopening terminated boot bypassed fence")
	}
	must(t, second.CheckDispatch(ctx, live))
	next, err := second.Admit(ctx, key)
	must(t, err)
	// Targeted recovery must preserve unrelated keys and sessions; no restore invalidation.
	_, err = second.AuthenticateKey(ctx, key)
	must(t, err)
	_, err = second.AuthenticateSession(ctx, session)
	must(t, err)
	must(t, second.Release(ctx, live))
	must(t, second.Release(ctx, next))
}
