// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"strings"
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
	if value.Archive != domain.Archived || terminal.CleanupVerified || terminal.CloseRequestID == "" || foreign.CloseRequestID != "" || other.Revision != 1 {
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

func TestSessionArchiveWaitsForTerminalAndForwardCleanup(t *testing.T) {
	for _, terminalFirst := range []bool{true, false} {
		name := "forward-first"
		if terminalFirst {
			name = "terminal-first"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			sessionID, terminalID, forwardID := domain.NewID(), domain.NewID(), domain.NewID()
			mutate := func(call func(*Tx) (any, error)) {
				t.Helper()
				if _, err := s.Mutate(ctx, domain.NewID(), "archive.fixture", nil, call); err != nil {
					t.Fatal(err)
				}
			}
			mutate(func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.NotArchived, Recovery: domain.NoRecovery}); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.TerminalKind, terminalID, 0, sessionID, "", domain.Terminal{State: domain.TerminalRunning, Rows: 24, Columns: 80}); err != nil {
					return nil, err
				}
				return tx.Put(domain.ForwardKind, forwardID, 0, sessionID, "", domain.Forward{State: domain.ForwardActive, ClientClaimed: true, WorkerClaimed: true})
			})
			attemptCompletion := func(tx *Tx) (any, error) {
				row, err := tx.Get(domain.SessionKind, sessionID)
				if err != nil {
					return nil, err
				}
				value, err := Decode[domain.Session](row)
				if err != nil {
					return nil, err
				}
				value.Archive = domain.Archived
				return tx.Put(domain.SessionKind, row.ID, row.Revision, row.SessionID, row.ProjectID, value)
			}
			mutate(func(tx *Tx) (any, error) {
				if err := tx.StopForwards(sessionID, ""); err != nil {
					return nil, err
				}
				return attemptCompletion(tx)
			})
			assertArchive := func(want domain.ArchiveState) {
				t.Helper()
				row, err := s.Get(ctx, domain.SessionKind, sessionID)
				if err != nil {
					t.Fatal(err)
				}
				value, err := Decode[domain.Session](row)
				if err != nil || value.Archive != want {
					t.Fatalf("Archive = %s, want %s: %v", value.Archive, want, err)
				}
			}
			assertArchive(domain.Archived)
			for _, closeTerminal := range []bool{terminalFirst, !terminalFirst} {
				mutate(func(tx *Tx) (any, error) {
					if closeTerminal {
						row, err := tx.Get(domain.TerminalKind, terminalID)
						if err != nil {
							return nil, err
						}
						value, err := Decode[domain.Terminal](row)
						if err != nil {
							return nil, err
						}
						if value.CloseRequestID == "" {
							t.Fatal("Archive did not request original terminal cleanup")
						}
						value.State, value.CleanupVerified, value.CloseRequestID = domain.TerminalClosed, true, ""
						if _, err := tx.Put(domain.TerminalKind, row.ID, row.Revision, row.SessionID, row.ProjectID, value); err != nil {
							return nil, err
						}
						return nil, tx.CompleteTerminalArchive(sessionID)
					}
					row, err := tx.Get(domain.ForwardKind, forwardID)
					if err != nil {
						return nil, err
					}
					value, err := Decode[domain.Forward](row)
					if err != nil {
						return nil, err
					}
					if value.State != domain.ForwardStopping {
						t.Fatal("Archive did not stop the original forward")
					}
					value.State, value.ClientClean, value.WorkerClean = domain.ForwardStopped, true, true
					if _, err := tx.Put(domain.ForwardKind, row.ID, row.Revision, row.SessionID, row.ProjectID, value); err != nil {
						return nil, err
					}
					return attemptCompletion(tx)
				})
				if closeTerminal == terminalFirst {
					assertArchive(domain.Archived)
				} else {
					assertArchive(domain.Archived)
				}
			}
		})
	}
}

func TestTerminalArchiveLookupFailureRollsBackReportReceipt(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sessionID, terminalID, damagedID := domain.NewID(), domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "archive.fixture", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.ArchivePending, Recovery: domain.NoRecovery}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.TerminalKind, terminalID, 0, sessionID, "", domain.Terminal{State: domain.TerminalRunning, Rows: 24, Columns: 80}); err != nil {
			return nil, err
		}
		return tx.Put(domain.TerminalKind, damagedID, 0, sessionID, "", domain.Terminal{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80})
	})
	if err != nil {
		t.Fatal(err)
	}
	damaged, err := s.Get(ctx, domain.TerminalKind, damagedID)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON with an invalid field type models retained record corruption,
	// independently of the report transaction that must remain retryable.
	if _, err := s.db.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", []byte(`{"state":1}`), damagedID); err != nil {
		t.Fatal(err)
	}
	requestID := domain.NewID()
	report := func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.TerminalKind, terminalID, 1, sessionID, "", domain.Terminal{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80}); err != nil {
			return nil, err
		}
		return nil, tx.CompleteTerminalArchive(sessionID)
	}
	_, err = s.Mutate(ctx, requestID, "terminal.report.fixture", nil, report)
	assertCode(t, err, domain.InvalidArgument)
	original, err := s.Get(ctx, domain.TerminalKind, terminalID)
	if err != nil || original.Revision != 1 {
		t.Fatal("failed Archive lookup committed the terminal report", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", damaged.Data, damagedID); err != nil {
		t.Fatal(err)
	}
	result, err := s.Mutate(ctx, requestID, "terminal.report.fixture", nil, report)
	if err != nil || result.Replayed {
		t.Fatal("failed Archive lookup consumed the report receipt", err)
	}
	session, err := s.Get(ctx, domain.SessionKind, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Decode[domain.Session](session)
	if err != nil || value.Archive != domain.Archived {
		t.Fatal("exact report retry did not complete Archive after repair", err)
	}
}

func TestTerminalArchiveHistoryBoundFailureIsNotCleanupDeferral(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	sessionID := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "archive.fixture", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.ArchivePending, Recovery: domain.NoRecovery}); err != nil {
			return nil, err
		}
		for range domain.MaxTerminalRecords + 1 {
			if _, err := tx.Put(domain.TerminalKind, domain.NewID(), 0, sessionID, "", domain.Terminal{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "terminal.archive.fixture", nil, func(tx *Tx) (any, error) {
		return nil, tx.CompleteTerminalArchive(sessionID)
	})
	assertCode(t, err, domain.ResourceExhausted)
}

func TestTerminalReportReceiptDeletionRetainsOnlyOwnershipAcknowledgement(t *testing.T) {
	for _, sessionPurge := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal-delete", true: "session-purge"}[sessionPurge], func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			session, id, machine, device, request := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			terminal := domain.Terminal{MachineID: machine, DeviceID: device, State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80}
			receipt, err := s.Mutate(ctx, request, "terminal.report", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Archive: domain.NotArchived, Recovery: domain.NoRecovery}); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.TerminalKind, id, 0, session, "", terminal); err != nil {
					return nil, err
				}
				return map[string]any{"receipt_kind": TerminalReportReceiptKind, "terminal_id": id, "machine_id": machine, "device_id": device, "extra": "fixture-content"}, nil
			})
			if err != nil || receipt.Replayed {
				t.Fatal(err)
			}
			ordinaryID := domain.NewID()
			_, err = s.Mutate(ctx, ordinaryID, "fixture.ordinary", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.TerminalKind, id, 1, session, "", terminal); err != nil {
					return nil, err
				}
				return map[string]string{"fixture_content": "must-be-redacted"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Mutate(ctx, domain.NewID(), "fixture.purge", nil, func(tx *Tx) (any, error) {
				if sessionPurge {
					return nil, tx.purgeSession(SessionDeletion{SessionID: session})
				}
				return nil, tx.Delete(domain.TerminalKind, id, 2)
			})
			if err != nil {
				t.Fatal(err)
			}
			replay, found, err := s.Replay(ctx, request, "terminal.report", nil)
			var fields map[string]string
			if err != nil || !found || domain.Decode(replay.Data, &fields) != nil || len(fields) != 4 || fields["receipt_kind"] != string(TerminalReportReceiptKind) || fields["terminal_id"] != string(id) || fields["machine_id"] != string(machine) || fields["device_id"] != string(device) {
				t.Fatal("deletion lost acknowledgement binding or retained extra content", err)
			}
			ordinary, found, err := s.Replay(ctx, ordinaryID, "fixture.ordinary", nil)
			if err != nil || !found || strings.Contains(string(ordinary.Data), "must-be-redacted") {
				t.Fatal("ordinary deleted receipt retained resource content", err)
			}
			if _, err := s.Get(ctx, domain.TerminalKind, id); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("receipt preservation resurrected terminal", err)
			}
		})
	}
}
