package store

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestQueuePagesBoundBytesAndRetainAcceptanceOrder(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.queue", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "large", Archive: domain.NotArchived}); err != nil {
			return nil, err
		}
		for i := 1; i <= 20; i++ {
			state := domain.InputQueued
			if i == 3 {
				state = domain.InputRemoved
			}
			if _, err := tx.Put(domain.QueueKind, domain.NewID(), 0, session, "", domain.QueuedInput{Sequence: uint64(i), Prompt: strings.Repeat("x", domain.MaxPromptBytes), Delivery: state}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var after uint64
	for {
		records, more, err := s.Queue(ctx, session, after, 200)
		if err != nil {
			t.Fatal(err)
		}
		bytes := 0
		for _, r := range records {
			bytes += len(r.Data)
			q, err := Decode[domain.QueuedInput](r)
			if err != nil {
				t.Fatal(err)
			}
			if q.Sequence <= after || q.Sequence == 3 {
				t.Fatal("queue order/removal incorrect")
			}
			after = q.Sequence
			count++
		}
		if bytes > 3<<20 {
			t.Fatal("page exceeds byte boundary")
		}
		if !more {
			break
		}
	}
	if count != 19 || after != 20 {
		t.Fatal("byte-limited pagination lost entries")
	}
}
