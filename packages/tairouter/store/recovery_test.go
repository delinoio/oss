// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/core"
	"os"
	"testing"
	"time"
)

func TestThirdMailSuccessSurvivesCleanup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	u := user(t, s, "third")
	i, e := s.Invite(ctx, sc, u.Email)
	if e != nil {
		t.Fatal(e)
	}
	var token string
	for attempt := 0; attempt < 3; attempt++ {
		execSQL(t, s, "UPDATE invitations SET next_attempt=clock_timestamp()-interval '1 second' WHERE id=$1", i.ID)
		m, e := s.ClaimMail(ctx)
		if e != nil {
			t.Fatal(e)
		}
		e = s.WithMailAttempt(ctx, m, func(_ context.Context, m Mail) MailOutcome {
			token = m.Token
			if attempt == 2 {
				return MailSuccess
			}
			return MailTransient
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	execSQL(t, s, "UPDATE invitations SET claim_until=clock_timestamp()-interval '1 minute' WHERE id=$1", i.ID)
	if e = s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInvite(ctx, u.ID, token); e != nil {
		t.Fatal(e)
	}
}

func TestAdmissionExplicitRegistrationAndTermination(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.RegisterAdmissionInstance(ctx); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	proof, e := s.DrainAdmissionInstance(ctx, s.InstanceID())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CheckDispatch(ctx, l); !errors.Is(e, ErrInstanceDraining) {
		t.Fatal("drain failed to fence dispatch", e)
	}
	if _, e = s.ReconcileTerminatedInstance(ctx, s.InstanceID(), proof, false); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.ReconcileTerminatedInstance(ctx, s.InstanceID(), "wrong", true); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	n, e := s.ReconcileTerminatedInstance(ctx, s.InstanceID(), proof, true)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e = s.CheckDispatch(ctx, l); e == nil {
		t.Fatal("terminated boot sent")
	}
	if e = s.RegisterAdmissionInstance(ctx); !errors.Is(e, ErrConflict) {
		t.Fatal("revived boot", e)
	}
}

func TestDestinationSettingsTypedAndLegacy(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	operator := core.Identity{Issuer: "issuer", Subject: "operator"}
	execSQL(t, s, `UPDATE settings SET destinations='["https://legacy.example"]'`)
	v, e := s.Settings(ctx)
	if e != nil || len(v.DestinationRules) != 1 || len(v.DestinationRules[0].AllowedCIDRs) != 0 {
		t.Fatal(v, e)
	}
	v.DestinationRules = []AllowedDestination{{ID: "stable", Origin: "https://example.com", AllowedCIDRs: []string{"::ffff:127.0.0.0/104"}}}
	v, e = s.UpdateSettingsCAS(ctx, operator, v.Revision, v)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Settings(ctx)
	if e != nil || got.DestinationRules[0].ID != "stable" || got.DestinationRules[0].AllowedCIDRs[0] != "127.0.0.0/8" {
		t.Fatal(got, e)
	}
	v.DestinationRules = append(v.DestinationRules, AllowedDestination{Origin: "https://example.com:443"})
	if _, e = s.UpdateSettingsCAS(ctx, operator, v.Revision, v); e == nil {
		t.Fatal("duplicate origin accepted")
	}
}

func TestExplicitBootRegistrationWithoutMigration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	other, e := Open(ctx, os.Getenv("TAIROUTER_TEST_DATABASE_URL"), Options{Schema: s.schema, Keys: s.keys})
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	before, e := s.ListAdmissionInstances(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = other.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	after, e := s.ListAdmissionInstances(ctx)
	if e != nil || len(before) != len(after) {
		t.Fatal("maintenance registered boot", e)
	}
	if e = other.RegisterAdmissionInstance(ctx); e != nil {
		t.Fatal(e)
	}
	_, sc := tenant(t, s)
	leaseFor(t, other, sc)
}
func TestReleaseDeletesWaitersPreservesProbe(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(ctx, sc, "provider", "m", []byte("secret")); e != nil {
		t.Fatal(e)
	}
	_, v, e := s.GetPrivateVersion(ctx, sc, "provider", "m")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetMistralHold(ctx, l, "m", v, "digest", "model", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	ticket, e := s.JoinMistralHold(ctx, l, "m", v, "digest", "model")
	if e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE mistral_holds SET hold_until=clock_timestamp()-interval '1 second'")
	p, e := s.TryMistralProbe(ctx, l, ticket)
	if e != nil || p.Claim == "" {
		t.Fatal(p, e)
	}
	if e = s.Release(ctx, l); e != nil {
		t.Fatal(e)
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var waiters, claims int
		if e := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM mistral_waiters),(SELECT count(*) FROM mistral_holds WHERE probe_hash IS NOT NULL)").Scan(&waiters, &claims); e != nil {
			return e
		}
		if waiters != 0 || claims != 1 {
			t.Fatalf("waiters=%d claims=%d", waiters, claims)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestDrainFencesFirstSendAndRetriesPreservesCompletion(t *testing.T) {
	for _, priorSend := range []bool{false, true} {
		name := "first-send"
		if priorSend {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			_, sc := tenant(t, s)
			l := leaseFor(t, s, sc)
			if e := s.PutPrivate(ctx, sc, "provider", "p", []byte("secret")); e != nil {
				t.Fatal(e)
			}
			_, version, e := s.GetPrivateVersion(ctx, sc, "provider", "p")
			if e != nil {
				t.Fatal(e)
			}
			sends := 0
			send := func(context.Context) error { sends++; return nil }
			if priorSend {
				if e = s.WithAccountDispatch(ctx, l, "p", version, send); e != nil {
					t.Fatal(e)
				}
			}
			_, refreshVersion, claim, e := s.ClaimPrivateRefresh(ctx, l, "provider", "p")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.WithPrivateRefreshDispatch(ctx, l, "provider", "p", claim, func(context.Context) error { return nil }); e != nil {
				t.Fatal(e)
			}
			if _, e = s.DrainAdmissionInstance(ctx, s.InstanceID()); e != nil {
				t.Fatal(e)
			}
			if e = s.WithDispatch(ctx, l, send); !errors.Is(e, ErrInstanceDraining) {
				t.Fatal("plain dispatch", e)
			}
			if e = s.WithAccountDispatch(ctx, l, "p", version, send); !errors.Is(e, ErrInstanceDraining) {
				t.Fatal("account dispatch", e)
			}
			if e = s.WithPrivateRefreshDispatch(ctx, l, "provider", "p", claim, send); !errors.Is(e, ErrInstanceDraining) {
				t.Fatal("refresh dispatch", e)
			}
			expected := 0
			if priorSend {
				expected = 1
			}
			if sends != expected {
				t.Fatal("physical send after drain", sends)
			}
			if e = s.CompletePrivateRefresh(ctx, l, "provider", "p", refreshVersion, claim, []byte("rotated")); e != nil {
				t.Fatal("already sent refresh could not complete", e)
			}
			if e = s.RecordUsage(ctx, l, Usage{Outcome: OutcomeSucceeded, Model: "m", Provider: "p"}); e != nil {
				t.Fatal("usage completion", e)
			}
			if e = s.Release(ctx, l); e != nil {
				t.Fatal("release after drain", e)
			}
			if e = s.ReleaseUntilRecorded(ctx, l); e != nil {
				t.Fatal("idempotent release", e)
			}
		})
	}
}
