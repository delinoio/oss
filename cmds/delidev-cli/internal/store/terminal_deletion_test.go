// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPermanentSessionDeletionRequiresOriginalTerminalCleanup(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	session, _, _ := deletionSession(t, s, "selected")
	sibling, _, _ := deletionSession(t, s, "sibling")
	id, foreignID := domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.terminals", nil, func(tx *Tx) (any, error) {
		for terminalID, owner := range map[domain.ID]domain.ID{id: session.ID, foreignID: sibling.ID} {
			if _, err := tx.Put(domain.TerminalKind, terminalID, 0, owner, "", domain.Terminal{State: domain.TerminalRunning, Rows: 24, Columns: 80}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	if _, _, err := s.DeleteSession(ctx, request, session.ID, server, session.Revision); err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(ctx, domain.TerminalKind, id)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Decode[domain.Terminal](original)
	if err != nil || value.CloseRequestID == "" || value.CleanupVerified {
		t.Fatal("deletion did not retain original close intent", value, err)
	}
	foreign, err := s.Get(ctx, domain.TerminalKind, foreignID)
	if err != nil || foreign.Revision != 1 {
		t.Fatal("deletion changed another session's terminal", foreign, err)
	}
	if _, err := s.PurgeDeletedSession(ctx, session.ID); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("direct database purge bypassed terminal cleanup", err)
	}
	if _, replay, err := s.DeleteSession(ctx, request, session.ID, server, session.Revision); err != nil || !replay {
		t.Fatal("pending deletion receipt could not replay", err)
	}
	repeated, err := s.Get(ctx, domain.TerminalKind, id)
	if err != nil || repeated.Revision != original.Revision {
		t.Fatal("exact deletion replay replaced original terminal close", repeated, err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.cleanup", nil, func(tx *Tx) (any, error) {
		value.CleanupVerified, value.State = true, domain.TerminalClosed
		value.CloseRequestID = ""
		return tx.Put(domain.TerminalKind, id, original.Revision, session.ID, "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	purged, err := s.PurgeDeletedSession(ctx, session.ID)
	if err != nil || !purged.DatabaseRemoved {
		t.Fatal("confirmed original terminal cleanup did not release purge", purged, err)
	}
	if _, err := s.Get(ctx, domain.TerminalKind, id); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("deleted terminal metadata remained", err)
	}
	if _, err := s.Get(ctx, domain.TerminalKind, foreignID); err != nil {
		t.Fatal("another session's terminal was removed", err)
	}
}
