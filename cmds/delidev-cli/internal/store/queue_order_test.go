// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestWaitingQueueMoveMembershipAndCursor(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	ids := []domain.ID{domain.NewID(), domain.NewID(), domain.NewID()}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.waiting", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Fixture"}); err != nil {
			return nil, err
		}
		for index, id := range ids {
			if _, err := tx.Put(domain.QueueKind, id, 0, session, "", domain.QueuedInput{Sequence: uint64(index + 1), ContentRevision: 1, Prompt: "Original text", Mode: domain.ExecuteMode, Delivery: domain.InputQueued}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, more, generation, count, err := s.WaitingQueue(ctx, session, nil, "", 1)
	if err != nil || !more || count != 3 || generation != 3 || rows[0].ID != ids[0] {
		t.Fatal(rows, more, generation, count, err)
	}
	request := domain.NewID()
	_, err = s.Mutate(ctx, request, "fixture.move", ids[2], func(tx *Tx) (any, error) {
		next, changed, err := tx.MoveWaitingInput(session, ids[2], ids[0], generation)
		if next != 4 || !changed {
			t.Fatal(next, changed)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := s.WaitingQueue(ctx, session, &generation, ids[0], 1); domain.SafeError(err).Code != domain.CursorExpired {
		t.Fatal("old cursor remained valid", err)
	}
	rows, _, generation, count, err = s.WaitingQueue(ctx, session, nil, "", 10)
	if err != nil || generation != 4 || count != 3 || !slices.Equal([]domain.ID{rows[0].ID, rows[1].ID, rows[2].ID}, []domain.ID{ids[2], ids[0], ids[1]}) {
		t.Fatal(rows, generation, count, err)
	}
	for index, row := range rows {
		value, _ := Decode[domain.QueuedInput](row)
		if row.Revision != 1 || value.ContentRevision != 1 || value.Sequence != uint64(slices.Index(ids, row.ID)+1) {
			t.Fatal("movement changed public history", index, row, value)
		}
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.noop", nil, func(tx *Tx) (any, error) {
		next, changed, err := tx.MoveWaitingInput(session, ids[2], ids[0], generation)
		if next != generation || changed {
			t.Fatal(next, changed)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.claim", nil, func(tx *Tx) (any, error) {
		row, err := tx.OldestQueuedInput(session)
		if err != nil {
			return nil, err
		}
		if row.ID != ids[2] {
			t.Fatal("claim ignored private order", row.ID)
		}
		value, _ := Decode[domain.QueuedInput](row)
		value.Delivery = domain.InputClaimed
		return tx.Put(domain.QueueKind, row.ID, row.Revision, session, "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Mutate(ctx, request, "fixture.move", ids[2], func(tx *Tx) (any, error) { t.Fatal("receipt re-executed movement"); return nil, nil })
	if err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
	rows, _, generation, count, err = s.WaitingQueue(ctx, session, nil, "", 10)
	if err != nil || generation != 5 || count != 2 || rows[0].ID != ids[0] {
		t.Fatal(rows, generation, count, err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.edit", nil, func(tx *Tx) (any, error) {
		row, err := tx.Get(domain.QueueKind, ids[0])
		if err != nil {
			return nil, err
		}
		value, _ := Decode[domain.QueuedInput](row)
		value.Prompt = "Changed text"
		value.ContentRevision++
		return tx.Put(domain.QueueKind, row.ID, row.Revision, session, "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, next, _, err := s.WaitingQueue(ctx, session, nil, "", 10)
	if err != nil || next != generation {
		t.Fatal("text edit changed ordering generation", next, err)
	}
}

func TestWaitingQueueRestoreKeepsCapturedOrderAndExpiresBothTimelines(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	first, second := domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.restore-waiting", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Fixture"}); err != nil {
			return nil, err
		}
		for index, id := range []domain.ID{first, second} {
			if _, err := tx.Put(domain.QueueKind, id, 0, session, "", domain.QueuedInput{Sequence: uint64(index + 1), Delivery: domain.InputQueued}); err != nil {
				return nil, err
			}
		}
		_, _, err := tx.MoveWaitingInput(session, second, first, 2)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "ATTACH ':memory:' AS current_state"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "DETACH current_state")
	if _, err := conn.ExecContext(ctx, "CREATE TABLE current_state.metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO current_state.metadata(key,value) VALUES(?,?)", queueOrderPrefix+string(session), `{"version":1,"generation":"9007199254740993","ordered_input_ids":null}`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreWaitingOrder(ctx, tx, time.Now()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	owner := &Tx{ctx: ctx, tx: tx}
	state, ids, err := owner.waitingOrder(session)
	if err != nil || state.Generation != 9007199254740994 || !slices.Equal(ids, []domain.ID{second, first}) {
		tx.Rollback()
		t.Fatal(state, ids, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingQueueMissingMetadataLargeGenerationAndCorruption(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.waiting-zero", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Fixture"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.QueueKind, id, 0, session, "", domain.QueuedInput{Sequence: 1, Delivery: domain.InputQueued}); err != nil {
			return nil, err
		}
		_, err := tx.tx.ExecContext(ctx, "DELETE FROM metadata WHERE key=?", queueOrderPrefix+string(session))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, generation, _, err := s.WaitingQueue(ctx, session, nil, "", 10)
	if err != nil || generation != 0 {
		t.Fatal(generation, err)
	}
	const large = uint64(9007199254740993)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.large", nil, func(tx *Tx) (any, error) {
		return nil, tx.saveWaitingOrder(session, queueOrder{Version: 1, Generation: large, IDs: []domain.ID{id}})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, generation, _, err = s.WaitingQueue(ctx, session, nil, "", 10)
	if err != nil || generation != large {
		t.Fatal(generation, err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.corrupt", nil, func(tx *Tx) (any, error) {
		_, err := tx.tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key=?", `{"version":1,"generation":"9","ordered_input_ids":[]}`, queueOrderPrefix+string(session))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { _, err := tx.OldestQueuedInput(session); return err }); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("corrupt order admitted dispatch", err)
	}
}
