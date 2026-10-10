// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestNativeCompletionCannotArchiveOverRetainedTitleCleanup(t *testing.T) {
	for _, titleState := range []domain.JobState{domain.JobClaimed, domain.JobUncertain} {
		t.Run(string(titleState), func(t *testing.T) {
			ctx := context.Background()
			f := newPublicationFixture(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			terminal := f.event(domain.ExecutionTurnFinished, 3)
			terminal.Outcome = domain.ExecutionSucceeded
			f.publish(t, terminal)
			titleID := domain.NewID()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "title-archive.fixture", nil, func(tx *store.Tx) (any, error) {
				record, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.JobKind, titleID, 0, record.ID, record.ProjectID, domain.Job{Type: domain.GenerateSessionTitleJob, State: titleState}); err != nil {
					return nil, err
				}
				session.Archive, session.TitleJobID, session.TitleState = domain.ArchivePending, titleID, domain.TitleRunning
				if titleState == domain.JobUncertain {
					session.TitleState = domain.TitleUncertain
				}
				return tx.Put(record.Kind, record.ID, record.Revision, record.SessionID, record.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.service.Store.Get(ctx, domain.JobKind, titleID)
			if err != nil {
				t.Fatal(err)
			}
			request, _ := f.reportCompletion(t, f.completion())
			record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](record)
			if err != nil {
				t.Fatal(err)
			}
			if session.Archive != domain.ArchivePending || session.ActiveExecutionID != "" || session.Execution == nil || !session.Execution.CleanupVerified || session.Outcome != domain.ExecutionSucceeded {
				t.Fatal("conversation completion hid a retained title operation or lost its own cleanup")
			}
			after, err := f.service.Store.Get(ctx, domain.JobKind, titleID)
			if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("conversation completion changed independent title ownership")
			}
			response, err := f.client.ReportWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, request))
			if err != nil || !response.Msg.Replayed {
				t.Fatal("completion receipt did not replay")
			}
			replayed, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil || replayed.Revision != record.Revision {
				t.Fatal("completion replay repeated archive finalization")
			}
		})
	}
}
