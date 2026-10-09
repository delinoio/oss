// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSubscriptionDeletionRequestReservationSurvivesRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	request, account := domain.NewID(), domain.NewID()
	input := struct {
		ID       string      `json:"id"`
		Revision uint64      `json:"revision"`
		Kind     domain.Kind `json:"kind"`
	}{string(account), 9007199254740993, domain.AccountKind}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.cleanup.admit", nil, func(tx *Tx) (any, error) {
		if err := tx.RequireUnusedSubscriptionDeletionRequest(request); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if _, err := tx.PutJob(request, 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionsJob, State: domain.JobQueued, Input: json.RawMessage(`{}`), AcceptedAt: now}); err != nil {
			return nil, err
		}
		child, _ := json.Marshal(struct {
			Version         uint32    `json:"version"`
			AccountID       domain.ID `json:"account_id"`
			DeleteRequestID domain.ID `json:"delete_request_id"`
			DeleteRevision  uint64    `json:"delete_revision,string"`
		}{2, account, request, input.Revision})
		// The later cleanup checkpoint cannot replace the confirmed wire revision.
		_, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionJob, State: domain.JobQueued, ParentID: request, Input: child, Output: json.RawMessage(`{"revision":"9007199254740995"}`), AcceptedAt: now})
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	changed := input
	changed.Revision += 2
	for _, command := range []struct {
		operation string
		input     any
	}{
		{"configuration.delete", changed},
		{"another.command", input},
	} {
		_, _, err := s.Replay(ctx, request, command.operation, command.input)
		assertCode(t, err, domain.Conflict)
		_, err = s.Mutate(ctx, request, command.operation, command.input, func(*Tx) (any, error) { t.Fatal("different command reached mutation"); return nil, nil })
		assertCode(t, err, domain.Conflict)
	}
	if _, found, err := s.Replay(ctx, request, "configuration.delete", input); err != nil || found {
		t.Fatal("pending original command did not remain admissible", found, err)
	}
	if _, err := s.Mutate(ctx, request, "configuration.delete", input, func(*Tx) (any, error) { return struct{ Deleted bool }{true}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.Replay(ctx, request, "configuration.delete", input); err != nil || !found {
		t.Fatal("completed original command did not replay", found, err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.cleanup.reuse", nil, func(tx *Tx) (any, error) { return nil, tx.RequireUnusedSubscriptionDeletionRequest(request) })
	assertCode(t, err, domain.Conflict)
}
