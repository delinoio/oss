// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSessionForkRejectsFutureWorkerBeforeChildDispatch(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "empty-resume"
		if queued {
			name = "queued-resume"
		}
		t.Run(name, func(t *testing.T) {
			f, child, _ := publishedForkFixture(t)
			ctx := context.Background()
			id := domain.ID(child.Id)
			if queued {
				raw, _ := json.Marshal(domain.SessionInput{Prompt: "Keep this child input queued", Mode: domain.ExecuteMode})
				if _, err := sessionClient(f.accountFixture).EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: child.Id, DocumentJson: raw})); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.service.Store.Get(ctx, domain.SessionKind, id)
			if err != nil {
				t.Fatal(err)
			}
			filter := store.Filter{Kind: domain.JobKind, SessionID: id, Limit: 100}
			jobs, err := f.service.Store.List(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			// Set and observe the future heartbeat in one transaction so a live
			// fixture heartbeat cannot race the simulated backward host-clock move.
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "test.future-fork-heartbeat", nil, func(tx *store.Tx) (any, error) {
				if err := tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC().Add(time.Hour)); err != nil {
					return nil, err
				}
				r, session, err := sessionRecord(tx, id)
				if err != nil {
					return nil, err
				}
				return queueForkInitialExecution(tx, r, session, true)
			})
			if domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("future Worker observation permitted child Resume", err)
			}
			after, err := f.service.Store.Get(ctx, domain.SessionKind, id)
			if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("failed future-clock gate changed child dispatch", err)
			}
			afterJobs, err := f.service.Store.List(ctx, filter)
			if err != nil || len(afterJobs) != len(jobs) {
				t.Fatal("failed future-clock gate published execution work", err)
			}
			if queued {
				if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
					row, err := tx.OldestQueuedInput(id)
					if err != nil {
						return err
					}
					input, err := store.Decode[domain.QueuedInput](row)
					if err != nil || input.Delivery != domain.InputQueued || input.ExecutionID != "" || input.NativeRequestID != "" {
						t.Fatal("future-clock rejection consumed child input", err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
