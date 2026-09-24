package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type scheduleRPCFixture struct {
	*scheduleDispatchFixture
	http           *httptest.Server
	client         delidevv1connect.ScheduleServiceClient
	definition     domain.ScheduleDefinition
	paired, worker security.Identity
}

func newScheduleRPCFixture(t *testing.T) *scheduleRPCFixture {
	t.Helper()
	base := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	value, _ := store.Decode[domain.Schedule](base.record(t, domain.ScheduleKind, base.schedule))
	f := &scheduleRPCFixture{scheduleDispatchFixture: base, definition: value.Definition}
	f.now = time.Now().UTC()
	owner, _ := security.RandomToken()
	clientToken, _ := security.RandomToken()
	workerToken, _ := security.RandomToken()
	f.service.Identity = security.Identity{ServerID: domain.NewID(), Token: owner}
	f.paired = security.Identity{ServerID: f.service.Identity.ServerID, Token: clientToken}
	f.worker = security.Identity{ServerID: f.service.Identity.ServerID, Token: workerToken}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		if err := tx.Delete(r.Kind, r.ID, r.Revision); err != nil {
			return err
		}
		clientID := domain.NewID()
		if _, err := tx.Put(domain.DeviceKind, clientID, 0, "", "", domain.Device{Name: "Schedule client", Type: domain.ClientDevice, PairedAt: f.now}); err != nil {
			return err
		}
		clientDigest, workerDigest := sha256.Sum256([]byte(clientToken)), sha256.Sum256([]byte(workerToken))
		if err := tx.PutCredential(clientID, clientDigest[:]); err != nil {
			return err
		}
		if err := tx.PutCredential(f.device, workerDigest[:]); err != nil {
			return err
		}
		return tx.SetWorkerInstance(f.machine, f.instance, f.now)
	})
	f.http = httptest.NewServer(f.service.Handler(nil, true))
	t.Cleanup(func() { f.service.executionAuthority.cancel(); f.http.Close(); f.service.executionAuthority.close() })
	f.client = delidevv1connect.NewScheduleServiceClient(f.http.Client(), f.http.URL)
	return f
}
func (f *scheduleRPCFixture) saveRequest(t *testing.T, id string, revision uint64, value domain.ScheduleDefinition, token string) *pb.SaveScheduleRequest {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.SaveScheduleRequest{Mutation: &pb.Mutation{Id: id, ExpectedRevision: revision, RequestId: string(domain.NewID())}, SchemaVersion: 1, DefinitionJson: raw, LocalWorkerToken: token}
}
func (f *scheduleRPCFixture) save(t *testing.T, req *pb.SaveScheduleRequest) *pb.Resource {
	t.Helper()
	response, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, req))
	if err != nil {
		t.Fatal(err)
	}
	if response.Header().Get(rpc.CorrelationHeader) == "" {
		t.Fatal("missing schedule correlation")
	}
	return response.Msg.Schedule
}
func (f *scheduleRPCFixture) current(t *testing.T, id string) *pb.Resource {
	t.Helper()
	response, err := f.client.GetSchedule(f.ctx, ownerRequest(f.service.Identity, &pb.GetScheduleRequest{Id: id}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Schedule
}
func scheduleBody(t *testing.T, r *pb.Resource) domain.Schedule {
	t.Helper()
	var v domain.Schedule
	if err := domain.Decode(r.DocumentJson, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *scheduleRPCFixture) run(t *testing.T, r *pb.Resource) (*pb.RunScheduleNowRequest, *pb.RunScheduleNowResponse) {
	t.Helper()
	req := &pb.RunScheduleNowRequest{Mutation: acctMutation(r, domain.NewID())}
	response, err := f.client.RunScheduleNow(f.ctx, ownerRequest(f.service.Identity, req))
	if err != nil {
		t.Fatal(err)
	}
	return req, response.Msg
}

func TestScheduleRPCLifecycleRetriesAndIndependentHistory(t *testing.T) {
	f := newScheduleRPCFixture(t)
	definition := f.definition
	definition.Workspace = ""
	definition.Mode = ""
	definition.Overlap = ""
	request := f.saveRequest(t, "", 0, definition, "")
	var wg sync.WaitGroup
	responses := make(chan *pb.SaveScheduleResponse, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			response, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, request))
			if response != nil {
				responses <- response.Msg
			}
			errs <- err
		})
	}
	wg.Wait()
	close(responses)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := 0
	var schedule *pb.Resource
	for response := range responses {
		if !response.Replayed {
			fresh++
		}
		if schedule == nil {
			schedule = response.Schedule
		}
		if response.Schedule.Id != schedule.Id {
			t.Fatal("create retry duplicated schedule")
		}
	}
	if fresh != 1 {
		t.Fatal("create did not apply exactly once", fresh)
	}
	value := scheduleBody(t, schedule)
	if value.NextRunAt == nil || !value.NextRunAt.After(time.Now()) || value.ConfigurationRevision != 1 || value.Definition.Workspace != domain.Worktree || value.Definition.Mode != domain.ExecuteMode || value.Definition.Overlap != domain.ScheduleAllowOverlap {
		t.Fatal("invalid schedule defaults/timer")
	}
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.paired, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("foreign actor adopted receipt", err)
	}
	pause := &pb.ControlScheduleRequest{Mutation: acctMutation(schedule, domain.NewID()), Action: pb.ScheduleAction_SCHEDULE_ACTION_PAUSE}
	paused, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.paired, pause))
	if err != nil {
		t.Fatal(err)
	}
	value = scheduleBody(t, paused.Msg.Schedule)
	if value.Definition.Enabled || value.NextRunAt != nil {
		t.Fatal("pause retained due timer")
	}
	// Run now uses the paused definition and accepts one independently owned session.
	runRequest, run := f.run(t, paused.Msg.Schedule)
	var occurrence domain.ScheduleOccurrence
	if domain.Decode(run.Occurrence.DocumentJson, &occurrence) != nil || occurrence.Trigger != domain.ManualOccurrence || occurrence.State != domain.OccurrenceActive || run.Session == nil || occurrence.SessionID != domain.ID(run.Session.Id) {
		t.Fatal("Run now missing independent session")
	}
	session := sessionBody(t, run.Session)
	if session.Source != domain.ScheduledSession || session.ScheduleOrigin == nil || session.ScheduleOrigin.OccurrenceID != domain.ID(run.Occurrence.Id) {
		t.Fatal("Run now lost provenance")
	}
	resume := &pb.ControlScheduleRequest{Mutation: acctMutation(f.current(t, schedule.Id), domain.NewID()), Action: pb.ScheduleAction_SCHEDULE_ACTION_RESUME}
	resumed, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.service.Identity, resume))
	if err != nil {
		t.Fatal(err)
	}
	if !scheduleBody(t, resumed.Msg.Schedule).Definition.Enabled {
		t.Fatal("resume failed")
	}
	replayed, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.paired, pause))
	if err != nil || !replayed.Msg.Replayed || !scheduleBody(t, replayed.Msg.Schedule).Definition.Enabled {
		t.Fatal("old pause receipt reapplied", err)
	}
	runAgain, err := f.client.RunScheduleNow(f.ctx, ownerRequest(f.service.Identity, runRequest))
	if err != nil || !runAgain.Msg.Replayed || runAgain.Msg.Occurrence.Id != run.Occurrence.Id {
		t.Fatal("manual retry duplicated work", err)
	}
	createAgain, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !createAgain.Msg.Replayed || createAgain.Msg.Schedule.Revision != resumed.Msg.Schedule.Revision {
		t.Fatal("create receipt returned stale configuration", err)
	}
	deletion := &pb.DeleteScheduleRequest{Mutation: acctMutation(resumed.Msg.Schedule, domain.NewID())}
	if _, err := f.client.DeleteSchedule(f.ctx, ownerRequest(f.service.Identity, deletion)); err != nil {
		t.Fatal(err)
	}
	deletedAgain, err := f.client.DeleteSchedule(f.ctx, ownerRequest(f.service.Identity, deletion))
	if err != nil || !deletedAgain.Msg.Replayed {
		t.Fatal("delete retry failed", err)
	}
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted source recreated", err)
	}
	if _, err := f.client.RunScheduleNow(f.ctx, ownerRequest(f.service.Identity, runRequest)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted source receipt recreated work", err)
	}
	history, err := f.client.ListScheduleOccurrences(f.ctx, ownerRequest(f.paired, &pb.ListScheduleOccurrencesRequest{ScheduleId: schedule.Id}))
	if err != nil || len(history.Msg.Occurrences) != 1 || history.Msg.Occurrences[0].Id != run.Occurrence.Id {
		t.Fatal("history lost after deletion", err)
	}
	if _, err := f.service.Store.Get(f.ctx, domain.SessionKind, occurrence.SessionID); err != nil {
		t.Fatal("schedule deletion removed session", err)
	}
}

func TestScheduleRPCCursorScopeEpochAndRetainedOrder(t *testing.T) {
	f := newScheduleRPCFixture(t)
	first := f.save(t, f.saveRequest(t, "", 0, f.definition, ""))
	second := f.save(t, f.saveRequest(t, "", 0, f.definition, ""))
	page, err := f.client.ListSchedules(f.ctx, ownerRequest(f.paired, &pb.ListSchedulesRequest{ProjectId: string(f.project), PageSize: 1}))
	if err != nil || len(page.Msg.Schedules) != 1 || page.Msg.NextPageToken == "" {
		t.Fatal("schedule page missing", err)
	}
	next, err := f.client.ListSchedules(f.ctx, ownerRequest(f.paired, &pb.ListSchedulesRequest{ProjectId: string(f.project), PageSize: 1, PageToken: page.Msg.NextPageToken}))
	if err != nil || len(next.Msg.Schedules) != 1 || next.Msg.Schedules[0].Id == page.Msg.Schedules[0].Id {
		t.Fatal("schedule page repeated", err)
	}
	no := false
	if _, err := f.client.ListSchedules(f.ctx, ownerRequest(f.paired, &pb.ListSchedulesRequest{ProjectId: string(f.project), Enabled: &no, PageSize: 1, PageToken: page.Msg.NextPageToken})); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("cursor accepted foreign filter", err)
	}
	pause := &pb.ControlScheduleRequest{Mutation: acctMutation(second, domain.NewID()), Action: pb.ScheduleAction_SCHEDULE_ACTION_PAUSE}
	if _, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.service.Identity, pause)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.ListSchedules(f.ctx, ownerRequest(f.paired, &pb.ListSchedulesRequest{ProjectId: string(f.project), PageToken: page.Msg.NextPageToken})); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("stale membership cursor accepted", err)
	}
	_, one := f.run(t, first)
	_, two := f.run(t, f.current(t, first.Id))
	history, err := f.client.ListScheduleOccurrences(f.ctx, ownerRequest(f.paired, &pb.ListScheduleOccurrencesRequest{ScheduleId: first.Id, PageSize: 1}))
	if err != nil || len(history.Msg.Occurrences) != 1 || history.Msg.Occurrences[0].Id != one.Occurrence.Id || history.Msg.NextPageToken == "" {
		t.Fatal("history order missing", err)
	}
	historyNext, err := f.client.ListScheduleOccurrences(f.ctx, ownerRequest(f.paired, &pb.ListScheduleOccurrencesRequest{ScheduleId: first.Id, PageSize: 1, PageToken: history.Msg.NextPageToken}))
	if err != nil || len(historyNext.Msg.Occurrences) != 1 || historyNext.Msg.Occurrences[0].Id != two.Occurrence.Id {
		t.Fatal("history order repeated", err)
	}
	if _, err := f.client.GetScheduleOccurrence(f.ctx, ownerRequest(f.paired, &pb.GetScheduleOccurrenceRequest{ScheduleId: second.Id, Id: one.Occurrence.Id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("foreign occurrence accepted", err)
	}
	if _, err := f.client.ListScheduleOccurrences(f.ctx, ownerRequest(f.paired, &pb.ListScheduleOccurrencesRequest{ScheduleId: second.Id, PageToken: history.Msg.NextPageToken})); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("foreign history cursor accepted", err)
	}
	f.run(t, f.current(t, first.Id))
	if _, err := f.client.ListScheduleOccurrences(f.ctx, ownerRequest(f.paired, &pb.ListScheduleOccurrencesRequest{ScheduleId: first.Id, PageToken: history.Msg.NextPageToken})); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("stale history cursor accepted", err)
	}
}

func TestScheduleRPCLocalOriginEditsAndStrictOwnership(t *testing.T) {
	f := newScheduleRPCFixture(t)
	local := f.definition
	local.Workspace = domain.Local
	without := f.saveRequest(t, "", 0, local, "")
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, without)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Local creation lacked origin proof", err)
	}
	request := f.saveRequest(t, "", 0, local, f.worker.Token)
	created := f.save(t, request)
	origin := scheduleBody(t, created).LocalOrigin
	if origin == nil || origin.DeviceID != f.device || origin.MachineID != f.machine {
		t.Fatal("wrong Local origin")
	}
	local.Prompt = "updated Local prompt"
	edited := f.save(t, f.saveRequest(t, created.Id, created.Revision, local, ""))
	if got := scheduleBody(t, edited).LocalOrigin; got == nil || *got != *origin {
		t.Fatal("ordinary editing changed Local origin")
	}
	before := append([]byte(nil), edited.DocumentJson...)
	for _, mutate := range []func(*pb.SaveScheduleRequest){
		func(req *pb.SaveScheduleRequest) {
			req.DefinitionJson = bytes.Replace(req.DefinitionJson, []byte(`"name":`), []byte(`"local_origin":{},"name":`), 1)
		},
		func(req *pb.SaveScheduleRequest) {
			req.DefinitionJson = bytes.Replace(req.DefinitionJson, []byte(`"name":`), []byte(`"configuration_revision":22,"name":`), 1)
		},
		func(req *pb.SaveScheduleRequest) { req.Mutation.ExpectedRevision-- },
		func(req *pb.SaveScheduleRequest) { req.SchemaVersion = 2 },
	} {
		bad := f.saveRequest(t, edited.Id, edited.Revision, local, "")
		mutate(bad)
		if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, bad)); err == nil {
			t.Fatal("invalid schedule mutation accepted")
		}
	}
	if !bytes.Equal(before, f.current(t, edited.Id).DocumentJson) {
		t.Fatal("failed edit changed state")
	}
	for _, kind := range []domain.Kind{domain.ScheduleKind, domain.OccurrenceKind, domain.SessionKind, domain.JobKind} {
		rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: kind, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if bytes.Contains(r.Data, []byte(f.worker.Token)) {
				t.Fatal("Worker token persisted")
			}
		}
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.DeviceKind, f.device)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.Device](r)
		if err != nil {
			return err
		}
		v.Revoked = true
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v)
		return err
	})
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, f.saveRequest(t, edited.Id, edited.Revision, local, ""))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked origin permitted edit", err)
	}
}

func TestScheduleRPCReferencedDeletionDisablesAtomically(t *testing.T) {
	for _, kind := range []domain.Kind{domain.ProjectKind, domain.AgentKind} {
		t.Run(string(kind), func(t *testing.T) {
			f := newScheduleRPCFixture(t)
			created := f.save(t, f.saveRequest(t, "", 0, f.definition, ""))
			id := f.project
			if kind == domain.AgentKind {
				id = f.agent
			}
			r := f.record(t, kind, id)
			client := delidevv1connect.NewConfigurationServiceClient(f.http.Client(), f.http.URL)
			request := &pb.DeleteConfigurationRequest{Kind: rpc.WireKind(kind), Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: r.Revision + 1, RequestId: string(domain.NewID())}}
			if _, err := client.DeleteConfiguration(f.ctx, ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("stale deletion accepted", err)
			}
			if !bytes.Equal(created.DocumentJson, f.current(t, created.Id).DocumentJson) {
				t.Fatal("failed deletion disabled schedule")
			}
			request.Mutation.ExpectedRevision = r.Revision
			request.Mutation.RequestId = string(domain.NewID())
			if _, err := client.DeleteConfiguration(f.ctx, ownerRequest(f.service.Identity, request)); err != nil {
				t.Fatal(err)
			}
			current := f.current(t, created.Id)
			value := scheduleBody(t, current)
			if value.Definition.Enabled || value.NextRunAt != nil || value.Problem == nil || value.ConfigurationRevision != 2 {
				t.Fatal("reference deletion did not disable future schedule")
			}
			if _, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.service.Identity, &pb.ControlScheduleRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.ScheduleAction_SCHEDULE_ACTION_RESUME})); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("resume cleared disabling problem", err)
			}
			if _, err := f.client.RunScheduleNow(f.ctx, ownerRequest(f.service.Identity, &pb.RunScheduleNowRequest{Mutation: acctMutation(current, domain.NewID())})); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("Run now bypassed disabling problem", err)
			}
			// Reconfiguration selects a new independent identity; deleted IDs are never revived.
			replacement := domain.NewID()
			f.mutate(t, func(tx *store.Tx) error {
				var value any
				if kind == domain.ProjectKind {
					value = domain.Project{Name: "Replacement", Repositories: []domain.ID{f.repository}, PrimaryRepository: f.repository}
				} else {
					var agent domain.Agent
					if err := json.Unmarshal(r.Data, &agent); err != nil {
						return err
					}
					value = agent
				}
				_, err := tx.Put(kind, replacement, 0, "", "", value)
				return err
			})
			definition := f.definition
			if kind == domain.ProjectKind {
				definition.ProjectID = replacement
			} else {
				definition.AgentID = replacement
			}
			reconfigured := f.save(t, f.saveRequest(t, current.Id, current.Revision, definition, ""))
			if value := scheduleBody(t, reconfigured); !value.Definition.Enabled || value.Problem != nil {
				t.Fatal("valid reconfiguration did not restore scheduling")
			}
		})
	}
}

func TestScheduleRPCRejectsWorkersUnknownActionsAndForgedReceipts(t *testing.T) {
	f := newScheduleRPCFixture(t)
	request := f.saveRequest(t, "", 0, f.definition, "")
	created := f.save(t, request)
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.worker, f.saveRequest(t, "", 0, f.definition, ""))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker configured a schedule", err)
	}
	if _, err := f.client.GetSchedule(f.ctx, ownerRequest(f.worker, &pb.GetScheduleRequest{Id: created.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker read product schedule", err)
	}
	if _, err := f.client.RunScheduleNow(f.ctx, ownerRequest(f.worker, &pb.RunScheduleNowRequest{Mutation: acctMutation(created, domain.NewID())})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker initiated scheduled work", err)
	}
	if _, err := f.client.ControlSchedule(f.ctx, ownerRequest(f.service.Identity, &pb.ControlScheduleRequest{Mutation: acctMutation(created, domain.NewID()), Action: pb.ScheduleAction(99)})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown action accepted", err)
	}
	changed := proto.Clone(request).(*pb.SaveScheduleRequest)
	changed.DefinitionJson = bytes.Replace(changed.DefinitionJson, []byte("Fixture schedule"), []byte("Another schedule"), 1)
	if _, err := f.client.SaveSchedule(f.ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed definition adopted receipt", err)
	}
	if _, err := f.service.GetSchedule(context.Background(), connect.NewRequest(&pb.GetScheduleRequest{Id: created.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("missing actor bypassed product boundary", err)
	}
}
