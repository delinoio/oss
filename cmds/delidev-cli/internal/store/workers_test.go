package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRevokedPrincipalCannotCommitOrReplayReceipt(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "device", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: "client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: id})
	request := domain.NewID()
	calls := 0
	action := func(tx *Tx) (any, error) {
		calls++
		return tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": "accepted"})
	}
	if _, err := s.Mutate(actor, request, "create", nil, action); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "revoke", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, id)
		if err != nil {
			return nil, err
		}
		v, err := Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		v.Revoked = true
		return tx.Put(domain.DeviceKind, id, r.Revision, "", "", v)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []domain.ID{request, domain.NewID()} {
		_, _, err := s.Replay(actor, req, "create", nil)
		assertCode(t, err, domain.Unauthenticated)
		_, err = s.Mutate(actor, req, "create", nil, action)
		assertCode(t, err, domain.Unauthenticated)
	}
	if calls != 1 {
		t.Fatal("revoked in-flight principal mutated state")
	}
}
