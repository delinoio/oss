package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestReplayPreflightDoesNotReserveMissingRequest(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	entity := domain.NewID()
	input := map[string]string{"name": "first"}
	if _, found, err := s.Replay(ctx, id, "create", input); err != nil || found {
		t.Fatalf("missing receipt: %v %v", found, err)
	}
	// A preflight read must not prevent a different input from being the first
	// accepted operation. Its exact committed input is authoritative afterward.
	input["name"] = "accepted"
	saved, err := s.Mutate(ctx, id, "create", input, func(tx *Tx) (any, error) { return tx.Put(domain.ProjectKind, entity, 0, "", "", input) })
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Replay(ctx, id, "create", input)
	if err != nil || !found || !got.Replayed || string(got.Data) != string(saved.Data) {
		t.Fatalf("accepted replay: %v %v", found, err)
	}
	_, _, err = s.Replay(ctx, id, "other-operation", input)
	assertCode(t, err, domain.Conflict)
	_, _, err = s.Replay(ctx, id, "create", map[string]string{"name": "changed"})
	assertCode(t, err, domain.Conflict)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, found, err = next.Replay(ctx, id, "create", input)
	if err != nil || !found || !json.Valid(got.Data) {
		t.Fatalf("restart: %v %v", found, err)
	}
}
