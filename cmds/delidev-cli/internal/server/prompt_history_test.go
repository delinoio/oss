// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"strings"
	"testing"
)

func TestProjectPromptHistoryPublicCreationPaginationAndClear(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
	f.service.Identity = security.Identity{ServerID: domain.NewID(), Token: strings.Repeat("a", 64)}
	creation := domain.CreateSession{Name: "Fixture", AgentID: f.agent, MachineID: f.machine, ProjectID: f.project, Workspace: domain.Worktree, Prompt: "  first\n한글  ", Source: domain.ManualSession}
	raw, _ := json.Marshal(creation)
	req := connect.NewRequest(&pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw})
	response, err := f.service.CreateSession(f.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateSession(f.ctx, req); err != nil {
		t.Fatal(err)
	}
	creation.Source = domain.ExternalCLISession
	raw, _ = json.Marshal(creation)
	if _, err := f.service.CreateSession(f.ctx, connect.NewRequest(&pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw})); err != nil {
		t.Fatal(err)
	}
	list := func(project domain.ID, size uint32, token string) (*pb.ListProjectPromptHistoryResponse, error) {
		r, e := f.service.ListProjectPromptHistory(f.ctx, connect.NewRequest(&pb.ListProjectPromptHistoryRequest{ProjectId: string(project), PageSize: size, PageToken: token}))
		if e != nil {
			return nil, e
		}
		return r.Msg, nil
	}
	first, err := list(f.project, 1, "")
	if err != nil || len(first.Entries) != 1 || first.Entries[0].Prompt != creation.Prompt || first.NextPageToken == "" {
		t.Fatal(first, err)
	}
	second, err := list(f.project, 1, first.NextPageToken)
	if err != nil || len(second.Entries) != 1 || second.NextPageToken != "" || first.Entries[0].Id == second.Entries[0].Id {
		t.Fatal(second, err)
	}
	if _, err := list(domain.NewID(), 1, first.NextPageToken); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("cross-project cursor accepted", err)
	}
	// Follow-ups and projectless public creation retain their separate ownership.
	follow, _ := json.Marshal(domain.SessionInput{Prompt: "followup", Mode: domain.ExecuteMode})
	if _, err := f.service.EnqueueInput(f.ctx, connect.NewRequest(&pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: response.Msg.Change.Session.Id, DocumentJson: follow})); err != nil {
		t.Fatal(err)
	}
	creation.ProjectID = ""
	creation.Workspace = domain.GeneralChat
	raw, _ = json.Marshal(creation)
	if _, err := f.service.CreateSession(f.ctx, connect.NewRequest(&pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw})); err != nil {
		t.Fatal(err)
	}
	all, err := list(f.project, 100, "")
	if err != nil || len(all.Entries) != 2 {
		t.Fatal("excluded creation appended", all, err)
	}
	clear := connect.NewRequest(&pb.ClearProjectPromptHistoryRequest{ProjectId: string(f.project), RequestId: string(domain.NewID())})
	if _, err := f.service.ClearProjectPromptHistory(f.ctx, clear); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("unconfirmed clear accepted", err)
	}
	clear.Msg.Confirmed = true
	cleared, err := f.service.ClearProjectPromptHistory(f.ctx, clear)
	if err != nil || cleared.Msg.RemovedCount != 2 {
		t.Fatal(cleared, err)
	}
	f.mutate(t, func(tx *store.Tx) error { return tx.AppendProjectPromptHistory(f.project, "later canary") })
	replay, err := f.service.ClearProjectPromptHistory(f.ctx, clear)
	if err != nil || !replay.Msg.Replayed {
		t.Fatal(replay, err)
	}
	all, err = list(f.project, 100, "")
	if err != nil || len(all.Entries) != 1 || all.Entries[0].Prompt != "later canary" {
		t.Fatal(all, err)
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: f.device, MachineID: f.machine})
	if _, err := f.service.ListProjectPromptHistory(worker, connect.NewRequest(&pb.ListProjectPromptHistoryRequest{ProjectId: string(f.project)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("worker history access", err)
	}
}
func TestProjectPromptHistoryPagesRespectJSONAndBinaryByteBounds(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
	f.service.Identity = security.Identity{ServerID: domain.NewID(), Token: strings.Repeat("a", 64)}
	for i := 0; i < 30; i++ {
		f.mutate(t, func(tx *store.Tx) error {
			return tx.AppendProjectPromptHistory(f.project, strings.Repeat("\"", 256<<10))
		})
	}
	token := ""
	seen := map[string]bool{}
	for {
		r, err := f.service.ListProjectPromptHistory(f.ctx, connect.NewRequest(&pb.ListProjectPromptHistoryRequest{ProjectId: string(f.project), PageSize: 100, PageToken: token}))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Msg.Entries) == 0 {
			t.Fatal("empty continuation")
		}
		for _, e := range r.Msg.Entries {
			if seen[e.Id] {
				t.Fatal("duplicate page")
			}
			seen[e.Id] = true
		}
		if r.Msg.NextPageToken == "" {
			break
		}
		token = r.Msg.NextPageToken
	}
	if len(seen) != 30 {
		t.Fatal("byte paging skipped history", len(seen))
	}
}

func TestProjectPromptHistoryPairedClientRevocationAndCursorActor(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
	f.service.Identity = security.Identity{ServerID: domain.NewID(), Token: strings.Repeat("a", 64)}
	clientA, clientB := domain.NewID(), domain.NewID()
	f.mutate(t, func(tx *store.Tx) error {
		for _, id := range []domain.ID{clientA, clientB} {
			if _, err := tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: "Paired client", Type: domain.ClientDevice, PairedAt: f.now}); err != nil {
				return err
			}
		}
		for i := 0; i < 2; i++ {
			if err := tx.AppendProjectPromptHistory(f.project, "private text canary"); err != nil {
				return err
			}
		}
		return nil
	})
	actor := func(id domain.ID) context.Context {
		return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: id})
	}
	req := connect.NewRequest(&pb.ListProjectPromptHistoryRequest{ProjectId: string(f.project), PageSize: 1})
	response, err := f.service.ListProjectPromptHistory(actor(clientA), req)
	if err != nil || len(response.Msg.Entries) != 1 {
		t.Fatal(response, err)
	}
	req.Msg.PageToken = response.Msg.NextPageToken
	if _, err := f.service.ListProjectPromptHistory(actor(clientB), req); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("cursor crossed actor", err)
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.DeviceKind, clientA)
		if err != nil {
			return err
		}
		d, err := store.Decode[domain.Device](r)
		if err != nil {
			return err
		}
		d.Revoked = true
		_, err = tx.Put(domain.DeviceKind, clientA, r.Revision, "", "", d)
		return err
	})
	req.Msg.PageToken = ""
	if _, err := f.service.ListProjectPromptHistory(actor(clientA), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client retained read access", err)
	}
	if _, err := f.service.ClearProjectPromptHistory(actor(clientA), connect.NewRequest(&pb.ClearProjectPromptHistoryRequest{ProjectId: string(f.project), RequestId: string(domain.NewID()), Confirmed: true})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client retained clear access", err)
	}
}
