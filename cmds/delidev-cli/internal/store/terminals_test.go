package store

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTerminalArchiveAndDeletionRequireSelectedOwnedCleanup(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, sibling, terminalID, siblingTerminal := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	mutate := func(call func(*Tx) (any, error)) error {
		_, err := s.Mutate(ctx, domain.NewID(), "terminal.fixture", nil, call)
		return err
	}
	err := mutate(func(tx *Tx) (any, error) {
		for _, id := range []domain.ID{session, sibling} {
			if _, err := tx.Put(domain.SessionKind, id, 0, id, "", domain.Session{Archive: domain.NotArchived, Recovery: domain.NoRecovery}); err != nil {
				return nil, err
			}
		}
		for id, owner := range map[domain.ID]domain.ID{terminalID: session, siblingTerminal: sibling} {
			if _, err := tx.Put(domain.TerminalKind, id, 0, owner, "", domain.Terminal{State: domain.TerminalRunning, Rows: 24, Columns: 80}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, mutate(func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, session, 1) }), domain.RecoveryRequired)
	assertCode(t, mutate(func(tx *Tx) (any, error) { return nil, tx.Delete(domain.TerminalKind, terminalID, 1) }), domain.RecoveryRequired)
	err = mutate(func(tx *Tx) (any, error) {
		return tx.Put(domain.SessionKind, session, 1, session, "", domain.Session{Archive: domain.Archived, Recovery: domain.NoRecovery})
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get(ctx, domain.SessionKind, session)
	value, _ := Decode[domain.Session](r)
	owned, _ := s.Get(ctx, domain.TerminalKind, terminalID)
	terminal, _ := Decode[domain.Terminal](owned)
	other, _ := s.Get(ctx, domain.TerminalKind, siblingTerminal)
	foreign, _ := Decode[domain.Terminal](other)
	if value.Archive != domain.ArchivePending || terminal.CloseRequestID == "" || foreign.CloseRequestID != "" || other.Revision != 1 {
		t.Fatal("Archive bypassed cleanup or closed a sibling session")
	}
	err = mutate(func(tx *Tx) (any, error) {
		terminal.State, terminal.CleanupVerified, terminal.CloseRequestID = domain.TerminalClosed, true, ""
		if _, err := tx.Put(domain.TerminalKind, terminalID, owned.Revision, session, "", terminal); err != nil {
			return nil, err
		}
		return nil, tx.CompleteTerminalArchive(session)
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ = s.Get(ctx, domain.SessionKind, session)
	value, _ = Decode[domain.Session](r)
	if value.Archive != domain.Archived {
		t.Fatal("confirmed cleanup did not complete Archive")
	}
	if err := mutate(func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, session, r.Revision) }); err != nil {
		t.Fatal("proven terminal cleanup still blocked deletion", err)
	}
}
