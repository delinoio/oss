// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"testing"
)

func TestRecipientInvitationSelectors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	recipient := user(t, s, "recipient")
	other := user(t, s, "other")
	i, e := s.Invite(ctx, sc, recipient.Email)
	if e != nil {
		t.Fatal(e)
	}
	raw := core.Token()
	execSQL(t, s, "UPDATE invitations SET hash=$2 WHERE id=$1", i.ID, core.Hash(raw))
	if got, e := s.GetInvitationForUser(ctx, other.ID, raw); !errors.Is(e, ErrInvitationEmailMismatch) || got.ID != "" {
		t.Fatalf("metadata leaked: %+v %v", got, e)
	}
	if got, e := s.GetInvitationForUser(ctx, recipient.ID, raw); e != nil || got.OrganizationName != "Test" || got.CreatedAt.IsZero() {
		t.Fatalf("metadata: %+v %v", got, e)
	}
	if list, _, e := s.ListMyInvitations(ctx, recipient.ID, 10, ""); e != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, e)
	}
	if _, e = s.AcceptInviteID(ctx, other.ID, i.ID); !errors.Is(e, ErrInvitationEmailMismatch) {
		t.Fatal(e)
	}
	for _, state := range []string{"canceled", "superseded", "failed", "accepted"} {
		execSQL(t, s, "UPDATE invitations SET status=$2 WHERE id=$1", i.ID, state)
		if _, e = s.AcceptInviteID(ctx, recipient.ID, i.ID); e == nil {
			t.Fatalf("accepted %s", state)
		}
		if got, e := s.GetInvitationForUser(ctx, recipient.ID, raw); e != nil || got.Status != state {
			t.Fatalf("state %s: %+v %v", state, got, e)
		}
	}
	execSQL(t, s, "UPDATE invitations SET status='pending',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", i.ID)
	if _, e = s.AcceptInviteID(ctx, recipient.ID, i.ID); e == nil {
		t.Fatal("accepted expired")
	}
	execSQL(t, s, "UPDATE invitations SET expires_at=clock_timestamp()+interval '1 day' WHERE id=$1", i.ID)
	if _, e = s.AcceptInviteID(ctx, recipient.ID, i.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptInvite(ctx, recipient.ID, raw); e == nil {
		t.Fatal("token replay")
	}
}
func TestBrowserDeviceStates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, sc := tenant(t, s)
	other := user(t, s, "other")
	d, e := s.StartDevice(ctx, "fixture", "https://router.example")
	if e != nil {
		t.Fatal(e)
	}
	k, e := s.ApproveDevice(ctx, sc, d.Code, nil)
	if e != nil {
		t.Fatal(e)
	}
	if got, e := s.InspectDeviceForUser(ctx, other.ID, d.Code); !errors.Is(e, ErrNotFound) || got.ID != "" {
		t.Fatalf("metadata leaked %+v %v", got, e)
	}
	got, e := s.InspectDeviceForUser(ctx, u.ID, d.Code)
	if e != nil || got.State != DeviceApproved || got.Secret != "" || got.OrganizationID != sc.OrganizationID {
		t.Fatalf("approved %+v %v", got, e)
	}
	if e = s.ConfirmDevice(ctx, d.ID, d.Secret, k.ID); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".devices SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", d.ID)
	got, e = s.InspectDeviceForUser(ctx, u.ID, d.Code)
	if e != nil || got.State != DeviceDelivered {
		t.Fatalf("delivered %+v %v", got, e)
	}
}

func TestUsageFiltersPreservePersonalScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, sc := tenant(t, s)
	other := user(t, s, "other")
	member := addMember(t, s, sc.OrganizationID, other)
	for _, id := range []string{u.ID, other.ID} {
		for _, model := range []string{"one", "two"} {
			execSQL(t, s, `INSERT INTO usage(id,org_id,user_id,model,provider,error_code,price,provenance) VALUES($1,$2,$3,$4,'fixture','','{}','fixture')`, core.ID(), sc.OrganizationID, id, model)
		}
	}
	rows, next, e := s.QueryUsage(ctx, sc, UsageFilter{UserID: u.ID, Model: "one", Limit: 1})
	if e != nil || len(rows) != 1 || rows[0].UserID != u.ID || rows[0].Model != "one" || next != "" {
		t.Fatalf("filter: %+v %s %v", rows, next, e)
	}
	if _, _, e = s.QueryUsage(ctx, member, UsageFilter{UserID: u.ID}); !errors.Is(e, ErrForbidden) {
		t.Fatalf("cross-user read: %v", e)
	}
	rows, next, e = s.QueryUsage(ctx, sc, UsageFilter{UserID: u.ID, Limit: 1})
	if e != nil || len(rows) != 1 || next == "" {
		t.Fatalf("page: %+v %q %v", rows, next, e)
	}
	second, _, e := s.QueryUsage(ctx, sc, UsageFilter{UserID: u.ID, Limit: 1, After: next})
	if e != nil || len(second) != 1 || second[0].ID == rows[0].ID {
		t.Fatalf("next page: %+v %v", second, e)
	}
}
