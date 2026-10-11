package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestListSessionsProjectsAttentionOnlyForAuthorizedRead(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	session, execution := domain.NewID(), domain.NewID()
	_, err := s.Store.Mutate(ctx, domain.NewID(), "attention.fixture", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Question fixture", Archive: domain.NotArchived, ActiveExecutionID: execution}); err != nil {
			return nil, err
		}
		_, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, session, "", map[string]any{"execution_id": execution, "closure": "open", "type": "user-question"})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.ListSessions(ctx, connect.NewRequest(&pb.ListSessionsRequest{}))
	if err != nil || len(response.Msg.Sessions) != 1 {
		t.Fatal("authorized list failed", err)
	}
	projected := response.Msg.Sessions[0]
	var fields map[string]any
	if err := json.Unmarshal(projected.DocumentJson, &fields); err != nil || fields["awaiting_user_response"] != true || projected.Id != string(session) || projected.Revision != 1 || projected.SchemaVersion != 1 {
		t.Fatal("wrong response-only Resource projection", fields, err)
	}
	original, err := s.Store.Get(ctx, domain.SessionKind, session)
	if err != nil {
		t.Fatal(err)
	}
	fields = nil
	if err := json.Unmarshal(original.Data, &fields); err != nil || fields["awaiting_user_response"] != nil || original.Revision != projected.Revision {
		t.Fatal("list projection changed the retained Session", err)
	}
	client := domain.NewID()
	doctorPut(t, s, domain.DeviceKind, client, 0, domain.Device{Type: domain.ClientDevice, Revoked: true})
	revoked := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
	if _, err := s.ListSessions(revoked, connect.NewRequest(&pb.ListSessionsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client received session attention", err)
	}
}
