// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/artifacts"
	"github.com/delinoio/oss/packages/tairouter/core"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TAIROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TAIROUTER_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	keys, e := core.NewEnvelope("v1", map[string][]byte{"v1": make([]byte, 32)})
	if e != nil {
		t.Fatal(e)
	}
	s, e := Open(context.Background(), dsn, Options{Schema: "test_" + strings.ReplaceAll(core.ID(), "-", ""), Keys: keys, Operators: []core.Identity{{Issuer: "issuer", Subject: "operator"}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		s.db.Exec("DROP SCHEMA " + s.temp + " CASCADE")
		s.db.Exec("DROP SCHEMA " + s.schema + " CASCADE")
		s.Close()
	})
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterAdmissionInstance(context.Background()); e != nil {
		t.Fatal(e)
	}
	return s
}
func user(t *testing.T, s *Store, subject string) User {
	t.Helper()
	u, e := s.RegisterIdentity(context.Background(), "issuer", subject, subject+"@example.com", true)
	if e != nil {
		t.Fatal(e)
	}
	return u
}
func tenant(t *testing.T, s *Store) (User, core.Scope) {
	t.Helper()
	u := user(t, s, core.Token())
	o, e := s.CreateOrganization(context.Background(), u.ID, "Test")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(context.Background(), o.ID, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	return u, sc
}
func execSQL(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if e := s.tx(context.Background(), func(tx *sql.Tx) error { _, e := tx.Exec(q, args...); return e }); e != nil {
		t.Fatal(e)
	}
}
func addMember(t *testing.T, s *Store, org string, u User) core.Scope {
	execSQL(t, s, "INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member')", org, u.ID)
	sc, _, e := s.Membership(context.Background(), org, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	return sc
}
func TestIdentitySessionAndLoginState(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := user(t, s, "one")
	v, e := s.RegisterIdentity(ctx, "other", "one", u.Email, true)
	if e != nil || v.ID == u.ID {
		t.Fatalf("email merged: %+v %v", v, e)
	}
	if _, e = s.RegisterIdentity(ctx, "issuer", "bad", "x", false); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	raw, e := s.CreateSession(ctx, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateSession(ctx, raw); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE sessions SET last_seen=clock_timestamp()-interval '25 hours'")
	if _, e = s.AuthenticateSession(ctx, raw); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	raw, _ = s.CreateSession(ctx, u.ID)
	if e = s.Logout(ctx, raw); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateSession(ctx, raw); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	l := LoginState{core.Token(), core.Token(), core.Token(), core.Token()}
	if e = s.SaveLoginState(ctx, l); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeLoginState(ctx, l.State, "wrong"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	got, e := s.ConsumeLoginState(ctx, l.State, l.Binding)
	if e != nil || got != l {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e = s.ConsumeLoginState(ctx, l.State, l.Binding); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestIsolationRemovalRejoinAndExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "member")
	sc := addMember(t, s, owner.OrganizationID, u)
	o, e := s.CreateOrganization(ctx, u.ID, "Other")
	if e != nil {
		t.Fatal(e)
	}
	other, _, _ := s.Membership(ctx, o.ID, u.ID)
	if e = s.PutPrivate(ctx, sc, "provider", "same", []byte("secret")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetPrivate(ctx, other, "provider", "same"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.PutState(ctx, sc, core.Responses, "same", []byte("prompt")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetState(ctx, owner, core.Responses, "same"); !errors.Is(e, ErrContinuation) {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".states SET expires_at=clock_timestamp()-interval '1 second'")
	if _, e = s.GetState(ctx, sc, core.Responses, "same"); !errors.Is(e, ErrContinuation) {
		t.Fatal(e)
	}
	if e = s.RemoveMember(ctx, owner, u.ID); e != nil {
		t.Fatal(e)
	}
	newScope := addMember(t, s, owner.OrganizationID, u)
	if newScope.Generation == sc.Generation {
		t.Fatal("generation reused")
	}
	if e = s.PutPrivate(ctx, sc, "provider", "same", []byte("stale refresh")); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.GetPrivate(ctx, newScope, "provider", "same"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestConcurrentAdmissionAndDispatchRevocation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	k, raw, e := s.IssueKey(ctx, sc, "test", nil)
	if e != nil {
		t.Fatal(e)
	}
	settings, _ := s.Settings(ctx)
	settings.Concurrency = 1
	if e = s.UpdateSettings(ctx, core.Identity{Issuer: "issuer", Subject: "operator"}, settings); e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var lease Lease
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, e := s.Admit(ctx, raw)
			if e == nil {
				successes.Add(1)
				mu.Lock()
				lease = l
				mu.Unlock()
			} else {
				var limit *LimitError
				if !errors.As(e, &limit) {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
	entered := make(chan struct{})
	finish := make(chan struct{})
	sent := make(chan error, 1)
	go func() {
		sent <- s.WithDispatch(ctx, lease, func(context.Context) error { close(entered); <-finish; return nil })
	}()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- s.RevokeKey(ctx, sc, k.ID) }()
	select {
	case e := <-revoked:
		t.Fatalf("revocation crossed active dispatch gate: %v", e)
	case <-time.After(80 * time.Millisecond):
	}
	close(finish)
	if e = <-sent; e != nil {
		t.Fatal(e)
	}
	if e = <-revoked; e != nil {
		t.Fatal(e)
	}
	if e = s.WithDispatch(ctx, lease, func(context.Context) error { t.Fatal("send after revoke"); return nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE admissions SET lease_until=clock_timestamp()-interval '1 day'")
	_, raw, _ = s.IssueKey(ctx, sc, "next", nil)
	if _, e = s.Admit(ctx, raw); e == nil {
		t.Fatal("expired lease reclaimed under partition")
	}
	if e = s.Release(ctx, lease); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Admit(ctx, raw); e != nil {
		t.Fatal(e)
	}
}
func TestContinuationQuotasAndEncryptionRotation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	for n := range 66 {
		if e := s.PutState(ctx, sc, core.Reasoning, string(rune('A'+n)), []byte("private prompt")); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.GetState(ctx, sc, core.Reasoning, "A"); !errors.Is(e, ErrContinuation) {
		t.Fatal(e)
	}
	if e := s.PutPrivate(ctx, sc, "provider", "p", []byte("credential")); e != nil {
		t.Fatal(e)
	}
	var stored []byte
	e := s.tx(ctx, func(tx *sql.Tx) error { return tx.QueryRow("SELECT value FROM private_data").Scan(&stored) })
	if e != nil || strings.Contains(string(stored), "credential") {
		t.Fatal("plaintext storage", e)
	}
	newkey := make([]byte, 32)
	newkey[0] = 9
	s.keys, _ = core.NewEnvelope("v2", map[string][]byte{"v1": make([]byte, 32), "v2": newkey})
	if e = s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	s.keys, _ = core.NewEnvelope("v2", map[string][]byte{"v2": newkey})
	if p, e := s.GetPrivate(ctx, sc, "provider", "p"); e != nil || string(p) != "credential" {
		t.Fatalf("%s %v", p, e)
	}
	if _, e = s.GetState(ctx, sc, core.Reasoning, string(rune('A'+65))); e != nil {
		t.Fatal(e)
	}
}
func TestDeviceHandoffAndDurableExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	d, e := s.StartDevice(ctx, "CLI", "https://router.example")
	if e != nil {
		t.Fatal(e)
	}
	k, e := s.ApproveDevice(ctx, sc, d.Code, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApproveDevice(ctx, sc, d.Code, nil); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.PollDevice(ctx, d.ID, "wrong"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	p, e := s.PollDevice(ctx, d.ID, d.Secret)
	if e != nil || p.KeyID != k.ID || p.RawKey == "" {
		t.Fatalf("%+v %v", p, e)
	}
	if _, e = s.PollDevice(ctx, d.ID, d.Secret); e == nil {
		t.Fatal("poll interval not enforced")
	}
	execSQL(t, s, "UPDATE "+s.temp+".devices SET last_poll=clock_timestamp()-interval '6 seconds'")
	again, e := s.PollDevice(ctx, d.ID, d.Secret)
	if e != nil || again.RawKey != p.RawKey {
		t.Fatal("handoff not replayable", e)
	}
	if e = s.ConfirmDevice(ctx, d.ID, d.Secret, k.ID); e != nil {
		t.Fatal(e)
	}
	confirmed, e := s.PollDevice(ctx, d.ID, d.Secret)
	if e != nil || confirmed.RawKey != "" || confirmed.Status != "confirmed" {
		t.Fatalf("%+v %v", confirmed, e)
	}
	if _, e = s.AuthenticateKey(ctx, p.RawKey); e != nil {
		t.Fatal(e)
	}
	d, _ = s.StartDevice(ctx, "lost", "https://router.example")
	_, e = s.ApproveDevice(ctx, sc, d.Code, nil)
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.PollDevice(ctx, d.ID, d.Secret)
	if e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "DELETE FROM "+s.temp+".devices WHERE id=$1", d.ID)
	execSQL(t, s, "UPDATE api_keys SET handoff_deadline=clock_timestamp()-interval '1 second' WHERE NOT handed_off")
	if _, e = s.AuthenticateKey(ctx, p.RawKey); !errors.Is(e, ErrForbidden) {
		t.Fatal("lost temp state allowed orphan key", e)
	}
}
func TestInvitationsRetrySupersessionAndSingleUse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	u := user(t, s, "invitee")
	i, e := s.Invite(ctx, sc, u.Email)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Invite(ctx, sc, u.Email); e == nil {
		t.Fatal("resend throttle")
	}
	m, e := s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var token string
	if e = s.WithMailAttempt(ctx, m, func(_ context.Context, m Mail) MailOutcome { token = m.Token; return MailTransient }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimMail(ctx); !errors.Is(e, ErrNotFound) {
		t.Fatal("retry too soon", e)
	}
	execSQL(t, s, "UPDATE invitations SET next_attempt=clock_timestamp()-interval '1 second' WHERE id=$1", i.ID)
	m, e = s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithMailAttempt(ctx, m, func(_ context.Context, m Mail) MailOutcome {
		if m.Token != token {
			t.Fatal("retry changed link")
		}
		return MailSuccess
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInvite(ctx, u.ID, token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInvite(ctx, u.ID, token); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 minutes'")
	i, e = s.Invite(ctx, sc, "second@example.com")
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CancelInvite(ctx, sc, i.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.WithMailAttempt(ctx, m, func(context.Context, Mail) MailOutcome { t.Fatal("canceled job sent"); return MailSuccess }); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestUsageRetentionAnonymizationAndOperatorBoundary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "usage")
	sc := addMember(t, s, owner.OrganizationID, u)
	_, raw, _ := s.IssueKey(ctx, sc, "k", nil)
	l, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	cost := "0.01234"
	in := int64(42)
	out := int64(5)
	usage := Usage{Model: "m", Provider: "p", InputTokens: &in, OutputTokens: &out, Cost: &cost, Provenance: "fixture-v1"}
	if e = s.RecordUsage(ctx, l, usage); e != nil {
		t.Fatal(e)
	}
	cost = "99"
	if e = s.RecordUsage(ctx, l, usage); e != nil {
		t.Fatal(e)
	}
	a, e := s.AggregateUsage(ctx, owner, "")
	if e != nil || a.Requests != 1 || a.Cost != "0.01234" {
		t.Fatalf("%+v %v", a, e)
	}
	if _, e = s.AggregateUsage(ctx, sc, ""); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.InstanceAggregate(ctx, core.Identity{Issuer: "issuer", Subject: "usage"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.DeleteAccount(ctx, u.ID); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ListUsage(ctx, owner, "", 10)
	if e != nil || len(rows) != 1 || rows[0].UserID != "" {
		t.Fatalf("%+v %v", rows, e)
	}
	if e = s.RecordUsage(ctx, l, usage); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE usage SET expires_at=clock_timestamp()-interval '1 second'")
	rows, e = s.ListUsage(ctx, owner, "", 10)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}
func TestArtifactPendingDeletionFences(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	o := artifacts.Owner{OrganizationID: sc.OrganizationID, UserID: sc.UserID, Generation: sc.Generation}
	now := time.Now()
	id := core.ID()
	a := artifacts.Artifact{ID: id, ObjectKey: "artifacts/" + id, Owner: o, ContentType: "image/png", Size: 5, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), UploadDeadline: now.Add(5 * time.Minute)}
	if e := s.Reserve(ctx, o, []artifacts.Artifact{a}, now); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get(ctx, o, id, now); !errors.Is(e, artifacts.ErrNotFound) {
		t.Fatal(e)
	}
	if e := s.DeleteOrganization(ctx, sc); e != nil {
		t.Fatal(e)
	}
	deletes, e := s.PendingDeletes(ctx, now, 10)
	if e != nil || len(deletes) != 0 {
		t.Fatal("early upload deletion", deletes, e)
	}
	execSQL(t, s, "UPDATE object_deletes SET not_before=clock_timestamp()-interval '1 second'")
	deletes, e = s.PendingDeletes(ctx, now, 10)
	if e != nil || len(deletes) != 1 {
		t.Fatal("lost deletion intent", deletes, e)
	}
	if e = s.Publish(ctx, o, []string{id}, now); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.CompleteDelete(ctx, deletes[0], now); e != nil {
		t.Fatal(e)
	}
	deletes, e = s.PendingDeletes(ctx, now, 10)
	if e != nil || len(deletes) != 0 {
		t.Fatal(deletes, e)
	}
}

func TestRoleTransferDemotionAndRestore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	a := user(t, s, "admin")
	admin := addMember(t, s, owner.OrganizationID, a)
	b := user(t, s, "recipient")
	member := addMember(t, s, owner.OrganizationID, b)
	if e := s.ChangeRole(ctx, owner, a.ID, core.Administrator); e != nil {
		t.Fatal(e)
	}
	if e := s.ChangeRole(ctx, admin, b.ID, core.Administrator); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.RequestTransfer(ctx, owner, a.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.RequestTransfer(ctx, owner, b.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.AcceptTransfer(ctx, admin); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e := s.AcceptTransfer(ctx, member); e != nil {
		t.Fatal(e)
	}
	if e := s.DeleteOrganization(ctx, owner); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.ChangeRole(ctx, member, a.ID, core.Member); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Invite(ctx, admin, "x@example.com"); !errors.Is(e, ErrForbidden) {
		t.Fatal("stale admin authority", e)
	}
	if e := s.RemoveMember(ctx, member, b.ID); !errors.Is(e, ErrForbidden) {
		t.Fatal("owner left", e)
	}
	_, raw, e := s.IssueKey(ctx, admin, "restore", nil)
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.CreateSession(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.InvalidateAfterRestore(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateKey(ctx, raw); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateSession(ctx, session); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestRefreshCASAndConcurrentDeletion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "refresh")
	sc := addMember(t, s, owner.OrganizationID, u)
	_, raw, _ := s.IssueKey(ctx, sc, "k", nil)
	l, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, sc, "provider", "p", []byte("old")); e != nil {
		t.Fatal(e)
	}
	_, version, e := s.GetPrivateVersion(ctx, sc, "provider", "p")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeletePrivate(ctx, sc, "provider", "p"); e != nil {
		t.Fatal(e)
	}
	if e = s.CompareAndSwapPrivate(ctx, l, "provider", "p", version, []byte("late")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, sc, "provider", "p", []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	if e = s.CompareAndSwapPrivate(ctx, l, "provider", "p", version, []byte("late")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := s.PutState(ctx, sc, core.Responses, core.Token(), []byte("racing completion"))
			if e != nil && !errors.Is(e, ErrForbidden) {
				t.Error(e)
			}
		}()
	}
	if e = s.RemoveMember(ctx, owner, u.ID); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	var count int
	e = s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow("SELECT count(*) FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID).Scan(&count)
	})
	if e != nil || count != 0 {
		t.Fatal("deletion resurrected state", count, e)
	}
}
func TestSMTPHourlyLimitAndFinalFailure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	i, e := s.Invite(ctx, sc, "delivery@example.com")
	if e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "INSERT INTO mail_attempts(org_id) SELECT $1 FROM generate_series(1,100)", sc.OrganizationID)
	m, e := s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithMailAttempt(ctx, m, func(context.Context, Mail) MailOutcome { t.Fatal("hourly limit bypass"); return MailSuccess }); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "DELETE FROM mail_attempts")
	var token string
	for n := range 3 {
		execSQL(t, s, "UPDATE invitations SET next_attempt=clock_timestamp()-interval '1 second' WHERE id=$1", i.ID)
		m, e = s.ClaimMail(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.WithMailAttempt(ctx, m, func(_ context.Context, m Mail) MailOutcome {
			token = m.Token
			if m.Attempts != n+1 {
				t.Error(m.Attempts)
			}
			return MailTransient
		}); e != nil {
			t.Fatal(e)
		}
	}
	u, e := s.RegisterIdentity(ctx, "issuer", "delivery", "delivery@example.com", true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInvite(ctx, u.ID, token); !errors.Is(e, ErrNotFound) {
		t.Fatal("failed link accepted", e)
	}
	if _, e = s.ClaimMail(ctx); !errors.Is(e, ErrNotFound) {
		t.Fatal("fourth attempt", e)
	}
}
func TestArtifactPublicationAndNeverReuse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	o := artifacts.Owner{OrganizationID: sc.OrganizationID, UserID: sc.UserID, Generation: sc.Generation}
	now := time.Now()
	id := core.ID()
	a := artifacts.Artifact{ID: id, ObjectKey: "artifacts/" + id, Owner: o, ContentType: "image/png", Size: 5, CreatedAt: now, ExpiresAt: now.Add(time.Hour), UploadDeadline: now.Add(time.Minute)}
	if e := s.Reserve(ctx, o, []artifacts.Artifact{a}, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Publish(ctx, o, []string{id}, now); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get(ctx, o, id, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Tombstone(ctx, o, []string{id}, now); e != nil {
		t.Fatal(e)
	}
	deletes, e := s.PendingDeletes(ctx, now, 10)
	if e != nil || len(deletes) != 1 {
		t.Fatal(deletes, e)
	}
	if e = s.CompleteDelete(ctx, deletes[0], now); e != nil {
		t.Fatal(e)
	}
	if e = s.Reserve(ctx, o, []artifacts.Artifact{a}, now); e == nil {
		t.Fatal("reused artifact id")
	}
}
func TestCleanupAndUsageIdempotenceAfterExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	_, raw, _ := s.IssueKey(ctx, sc, "k", nil)
	l, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RecordUsage(ctx, l, Usage{Model: "unknown"}); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE usage SET expires_at=clock_timestamp()-interval '1 second'")
	if e = s.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordUsage(ctx, l, Usage{Model: "unknown"}); e != nil {
		t.Fatal(e)
	}
	a, e := s.AggregateUsage(ctx, sc, sc.UserID)
	if e != nil || a.Requests != 1 || a.MissingCost != 1 || a.MissingUsage != 1 {
		t.Fatal(a, e)
	}
	u, e := s.ListUsage(ctx, sc, sc.UserID, 10)
	if e != nil || len(u) != 0 {
		t.Fatal("expired usage resurrected", u, e)
	}
}

func TestDispatchGateReleasesBeforeResponseWait(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "parallel")
	sc := addMember(t, s, owner.OrganizationID, u)
	_, raw1, _ := s.IssueKey(ctx, owner, "one", nil)
	_, raw2, _ := s.IssueKey(ctx, sc, "two", nil)
	l, e := s.Admit(ctx, raw1)
	if e != nil {
		t.Fatal(e)
	}
	sent := make(chan struct{})
	headers := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		e := s.WithDispatch(ctx, l, func(context.Context) error { close(sent); return nil })
		if e == nil {
			<-headers
		}
		finished <- e
	}()
	<-sent
	admitted := make(chan error, 1)
	go func() { _, e := s.Admit(ctx, raw2); admitted <- e }()
	select {
	case e = <-admitted:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("response wait blocked another user's admission")
	}
	close(headers)
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeKey(ctx, owner, l.Principal.KeyID); e != nil {
		t.Fatal(e)
	}
	if e = s.WithDispatch(ctx, l, func(context.Context) error { t.Fatal("physical send initiated after revocation"); return nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestArtifactCountEvictionAndExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	o := artifacts.Owner{OrganizationID: sc.OrganizationID, UserID: sc.UserID, Generation: sc.Generation}
	now := time.Now()
	var oldest string
	for _, count := range []int{200, 1} {
		var batch []artifacts.Artifact
		var ids []string
		for range count {
			id := core.ID()
			if oldest == "" {
				oldest = id
			}
			ids = append(ids, id)
			batch = append(batch, artifacts.Artifact{ID: id, ObjectKey: "artifacts/" + id, Owner: o, ContentType: "image/png", Size: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), UploadDeadline: now.Add(time.Minute)})
		}
		if e := s.Reserve(ctx, o, batch, now); e != nil {
			t.Fatal(e)
		}
		if e := s.Publish(ctx, o, ids, now); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Get(ctx, o, oldest, now); !errors.Is(e, artifacts.ErrNotFound) {
		t.Fatal("oldest artifact not evicted", e)
	}
	execSQL(t, s, "UPDATE artifacts SET expires_at=clock_timestamp()-interval '1 second'")
	var id string
	e := s.tx(ctx, func(tx *sql.Tx) error { return tx.QueryRow("SELECT id FROM artifacts LIMIT 1").Scan(&id) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, o, id, now); !errors.Is(e, artifacts.ErrNotFound) {
		t.Fatal("expired artifact visible", e)
	}
}
func TestExpiredOwnershipTransferAndReplacedID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "transfer")
	sc := addMember(t, s, owner.OrganizationID, u)
	if e := s.RequestTransfer(ctx, owner, u.ID); e != nil {
		t.Fatal(e)
	}
	old, e := s.GetTransfer(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RequestTransfer(ctx, owner, u.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.AcceptTransferID(ctx, sc, old.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("replacement id replay", e)
	}
	execSQL(t, s, "UPDATE transfers SET expires_at=clock_timestamp()-interval '1 second'")
	if e = s.AcceptTransfer(ctx, sc); !errors.Is(e, ErrNotFound) {
		t.Fatal("expired transfer", e)
	}
}

func TestRotationCoversAllTemporarySecrets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	login := LoginState{State: core.Token(), Binding: core.Token(), Nonce: core.Token(), Verifier: core.Token()}
	if e := s.SaveLoginState(ctx, login); e != nil {
		t.Fatal(e)
	}
	d, e := s.StartDevice(ctx, "rotation", "https://router.example")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApproveDevice(ctx, sc, d.Code, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Invite(ctx, sc, "rotation@example.com"); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	key[0] = 7
	s.keys, e = core.NewEnvelope("much-longer-key-version", map[string][]byte{"v1": make([]byte, 32), "much-longer-key-version": key})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	s.keys, _ = core.NewEnvelope("much-longer-key-version", map[string][]byte{"much-longer-key-version": key})
	if got, e := s.ConsumeLoginState(ctx, login.State, login.Binding); e != nil || got.Verifier != login.Verifier {
		t.Fatal(got, e)
	}
	if got, e := s.PollDevice(ctx, d.ID, d.Secret); e != nil || got.RawKey == "" {
		t.Fatal("device rotation", e)
	}
	m, e := s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithMailAttempt(ctx, m, func(_ context.Context, m Mail) MailOutcome {
		if m.Token == "" {
			t.Fatal("missing invitation token")
		}
		return MailSuccess
	}); e != nil {
		t.Fatal(e)
	}
}
func TestDatabaseFailureClosesAdmissionAndDispatch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	_, raw, e := s.IssueKey(ctx, sc, "db", nil)
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	} // A separate pool remains available to drop the isolated schemas after failure.
	cleanup, e := sql.Open("pgx", os.Getenv("TAIROUTER_TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup.Close()
	defer cleanup.Exec("DROP SCHEMA " + s.schema + " CASCADE")
	defer cleanup.Exec("DROP SCHEMA " + s.temp + " CASCADE")
	s.Close()
	if _, e = s.Admit(ctx, raw); e == nil {
		t.Fatal("cached admission during DB outage")
	}
	if e = s.WithDispatch(ctx, l, func(context.Context) error { t.Fatal("dispatch during DB outage"); return nil }); e == nil {
		t.Fatal("missing DB failure")
	}
}

func TestStateAndAdmissionAcrossInstances(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	if e := s.PutState(ctx, sc, core.Responses, "restart", []byte("continuation")); e != nil {
		t.Fatal(e)
	}
	_, raw, e := s.IssueKey(ctx, sc, "shared", nil)
	if e != nil {
		t.Fatal(e)
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings.Concurrency = 1
	if e = s.UpdateSettings(ctx, core.Identity{Issuer: "issuer", Subject: "operator"}, settings); e != nil {
		t.Fatal(e)
	}
	lease, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Open(ctx, os.Getenv("TAIROUTER_TEST_DATABASE_URL"), Options{Schema: s.schema, Keys: s.keys})
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if e = second.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = second.RegisterAdmissionInstance(ctx); e != nil {
		t.Fatal(e)
	}
	value, e := second.GetState(ctx, sc, core.Responses, "restart")
	if e != nil || string(value) != "continuation" {
		t.Fatal("state not durable across instances", e)
	}
	if _, e = second.Admit(ctx, raw); e == nil {
		t.Fatal("separate instance bypassed concurrency")
	}
	if e = second.Release(ctx, lease); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Admit(ctx, raw); e != nil {
		t.Fatal("cross-instance release not visible", e)
	}
}
