// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionArchiveRetainsIndependentTitleCleanup(t *testing.T) {
	for _, state := range []domain.JobState{domain.JobQueued, domain.JobClaimed, domain.JobUncertain, domain.JobSucceeded, domain.JobFailed, domain.JobCanceled} {
		for _, titleState := range []domain.SessionTitleState{domain.TitleRunning, domain.TitleUncertain, domain.TitleSucceeded} {
			t.Run(string(state)+"/"+string(titleState), func(t *testing.T) {
				s, _ := openTest(t)
				ctx := context.Background()
				sessionID, titleID := domain.NewID(), domain.NewID()
				_, err := s.Mutate(ctx, domain.NewID(), "title-archive.fixture", nil, func(tx *Tx) (any, error) {
					if _, err := tx.Put(domain.JobKind, titleID, 0, sessionID, "", domain.Job{Type: domain.GenerateSessionTitleJob, State: state}); err != nil {
						return nil, err
					}
					return tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.Archived, Recovery: domain.NoRecovery, TitleJobID: titleID, TitleState: titleState})
				})
				if err != nil {
					t.Fatal(err)
				}
				before, err := s.Get(ctx, domain.JobKind, titleID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Mutate(ctx, domain.NewID(), "title-archive.finalize", nil, func(tx *Tx) (any, error) { return nil, tx.CompleteTerminalArchive(sessionID) })
				if err != nil {
					t.Fatal(err)
				}
				record, err := s.Get(ctx, domain.SessionKind, sessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := Decode[domain.Session](record)
				if err != nil {
					t.Fatal(err)
				}
				pending := state == domain.JobQueued || state == domain.JobClaimed || state == domain.JobUncertain || titleState == domain.TitleRunning || titleState == domain.TitleUncertain
				want := domain.Archived
				if pending {
					want = domain.ArchivePending
				}
				if session.Archive != want {
					t.Fatalf("archive = %s, want %s", session.Archive, want)
				}
				after, err := s.Get(ctx, domain.JobKind, titleID)
				if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
					t.Fatal("archive inspection changed retained title ownership")
				}
			})
		}
	}
}

func TestSessionArchiveTitleAndConversationCleanupInEitherOrder(t *testing.T) {
	for _, titleFirst := range []bool{false, true} {
		name := "conversation-first"
		if titleFirst {
			name = "title-first"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			sessionID, titleID, executionID := domain.NewID(), domain.NewID(), domain.NewID()
			mutate := func(call func(*Tx) (any, error)) {
				t.Helper()
				if _, err := s.Mutate(ctx, domain.NewID(), "title-archive.fixture", nil, call); err != nil {
					t.Fatal(err)
				}
			}
			mutate(func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.JobKind, titleID, 0, sessionID, "", domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed}); err != nil {
					return nil, err
				}
				return tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.ArchivePending, Recovery: domain.NoRecovery, ActiveExecutionID: executionID, TitleJobID: titleID, TitleState: domain.TitleRunning})
			})
			for step := 0; step < 2; step++ {
				mutate(func(tx *Tx) (any, error) {
					record, err := tx.Get(domain.SessionKind, sessionID)
					if err != nil {
						return nil, err
					}
					session, err := Decode[domain.Session](record)
					if err != nil {
						return nil, err
					}
					if (step == 0) == titleFirst {
						jobRecord, err := tx.Get(domain.JobKind, titleID)
						if err != nil {
							return nil, err
						}
						job, err := Decode[domain.Job](jobRecord)
						if err != nil {
							return nil, err
						}
						job.State = domain.JobSucceeded
						if _, err := tx.Put(domain.JobKind, titleID, jobRecord.Revision, sessionID, "", job); err != nil {
							return nil, err
						}
						session.TitleState = domain.TitleSucceeded
					} else {
						session.ActiveExecutionID = ""
					}
					if _, err := tx.Put(domain.SessionKind, sessionID, record.Revision, sessionID, "", session); err != nil {
						return nil, err
					}
					return nil, tx.CompleteTerminalArchive(sessionID)
				})
				record, err := s.Get(ctx, domain.SessionKind, sessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := Decode[domain.Session](record)
				if err != nil {
					t.Fatal(err)
				}
				want := domain.ArchivePending
				if step == 1 {
					want = domain.Archived
				}
				if session.Archive != want {
					t.Fatalf("step %d archive = %s, want %s", step, session.Archive, want)
				}
			}
			before, err := s.Get(ctx, domain.SessionKind, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			mutate(func(tx *Tx) (any, error) { return nil, tx.CompleteTerminalArchive(sessionID) })
			after, err := s.Get(ctx, domain.SessionKind, sessionID)
			if err != nil || after.Revision != before.Revision {
				t.Fatal("repeated finalization republished completed archive")
			}
		})
	}
}

func TestSessionArchiveRejectsMalformedRetainedTitleOwnership(t *testing.T) {
	for _, scenario := range []string{"missing", "foreign-session", "wrong-type", "unknown-state", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			sessionID, titleID := domain.NewID(), domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "title-archive.fixture", nil, func(tx *Tx) (any, error) {
				owner := sessionID
				job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobSucceeded}
				if scenario == "foreign-session" {
					owner = domain.NewID()
				}
				if scenario == "wrong-type" {
					job.Type = domain.ExecuteSessionJob
				}
				if scenario == "unknown-state" {
					job.State = domain.JobState("future-state")
				}
				var value any = job
				if scenario == "malformed" {
					value = map[string]any{"state": 123}
				}
				if scenario != "missing" {
					if _, err := tx.Put(domain.JobKind, titleID, 0, owner, "", value); err != nil {
						return nil, err
					}
				}
				return tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", domain.Session{Archive: domain.ArchivePending, Recovery: domain.NoRecovery, TitleJobID: titleID, TitleState: domain.TitleSucceeded})
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(ctx, domain.SessionKind, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Mutate(ctx, domain.NewID(), "title-archive.finalize", nil, func(tx *Tx) (any, error) {
				session, err := Decode[domain.Session](before)
				if err != nil {
					return nil, err
				}
				session.Archive = domain.Archived
				return tx.Put(domain.SessionKind, sessionID, before.Revision, sessionID, "", session)
			})
			if err == nil {
				t.Fatal("malformed retained title ownership granted archive visibility")
			}
			after, err := s.Get(ctx, domain.SessionKind, sessionID)
			if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("failed archive changed the retained session")
			}
		})
	}
}
