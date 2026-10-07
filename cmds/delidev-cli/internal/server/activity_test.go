package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestActivityRPCNativeTerminalSourcesAreReadOnlyAndMetadataOnly(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			event := f.event(domain.ExecutionTurnFinished, 3)
			event.Outcome = outcome
			receipt := f.publish(t, event)
			if _, err := f.call(receipt); err != nil {
				t.Fatal(err)
			}
			c := delidevv1connect.NewActivityServiceClient(f.http.Client(), f.http.URL)
			ctx := context.Background()
			request := &pb.ListActivityRequest{SessionId: string(f.input.SessionID), PageSize: 1}
			first, err := c.ListActivity(ctx, ownerRequest(f.service.Identity, request))
			if err != nil || len(first.Msg.Entries) != 1 || first.Msg.NextPageToken == "" {
				t.Fatal("missing terminal page", err)
			}
			terminal := first.Msg.Entries[0]
			want := map[domain.ExecutionOutcome]pb.ActivityKind{domain.ExecutionSucceeded: pb.ActivityKind_ACTIVITY_KIND_EXECUTION_SUCCEEDED, domain.ExecutionFailed: pb.ActivityKind_ACTIVITY_KIND_EXECUTION_FAILED, domain.ExecutionStopped: pb.ActivityKind_ACTIVITY_KIND_EXECUTION_STOPPED}[outcome]
			if terminal.Kind != want || terminal.ExecutionId != string(f.input.ExecutionID) || terminal.AccountId != string(f.input.AccountID) || terminal.JobId != string(f.job) || first.Header().Get(rpc.CorrelationHeader) == "" {
				t.Fatal("lost terminal attribution")
			}
			request.PageToken = first.Msg.NextPageToken
			next, err := c.ListActivity(ctx, ownerRequest(f.service.Identity, request))
			if err != nil || len(next.Msg.Entries) != 1 || next.Msg.NextPageToken != "" || next.Msg.Entries[0].Kind != pb.ActivityKind_ACTIVITY_KIND_EXECUTION_ACCEPTED {
				t.Fatal("dispatch source missing or duplicated", err)
			}
			request.PageToken = ""
			request.PageSize = 200
			page, err := c.ListActivity(ctx, ownerRequest(f.service.Identity, request))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := protojson.Marshal(page.Msg)
			for _, private := range []string{f.input.Input.Prompt, f.workerToken, string(f.thread), string(f.turn)} {
				if private != "" && strings.Contains(string(raw), private) {
					t.Fatal("activity leaked native content or credential")
				}
			}
			_, inbox := readExecutionInbox(t, f, domain.ExecutionTerminalInbox, f.input.ExecutionID)
			if inbox.ReadState != domain.InboxUnread {
				t.Fatal("read activity marked inbox read")
			}
			r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := store.Decode[domain.Session](r)
			if s.Execution.CleanupVerified {
				t.Fatal("terminal activity fabricated cleanup")
			}
		})
	}
}

func TestActivityRPCScheduleHistorySurvivesConfigurationDeletion(t *testing.T) {
	f := newScheduleRPCFixture(t)
	r := f.save(t, f.saveRequest(t, "", 0, f.definition, ""))
	_, run := f.run(t, r)
	if _, err := f.client.DeleteSchedule(f.ctx, ownerRequest(f.service.Identity, &pb.DeleteScheduleRequest{Mutation: acctMutation(f.current(t, r.Id), domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	c := delidevv1connect.NewActivityServiceClient(f.http.Client(), f.http.URL)
	page, err := c.ListActivity(f.ctx, ownerRequest(f.paired, &pb.ListActivityRequest{ProjectId: string(f.project)}))
	if err != nil || len(page.Msg.Entries) != 1 {
		t.Fatal("lost Run now activity", err)
	}
	e := page.Msg.Entries[0]
	if e.Kind != pb.ActivityKind_ACTIVITY_KIND_SCHEDULE_RUN_NOW || e.ScheduleId != r.Id || e.OccurrenceId != run.Occurrence.Id {
		t.Fatal("schedule origin missing")
	}
	raw, _ := protojson.Marshal(page.Msg)
	if strings.Contains(string(raw), f.definition.Prompt) {
		t.Fatal("activity exposed schedule prompt")
	}
	for _, actor := range []security.Identity{{}} {
		if _, err := c.ListActivity(f.ctx, ownerRequest(actor, &pb.ListActivityRequest{})); err == nil {
			t.Fatal("unauthorized activity read")
		}
	}
	if _, err := c.ListActivity(f.ctx, ownerRequest(f.worker, &pb.ListActivityRequest{})); err != nil {
		t.Fatal("registered Worker activity read failed", err)
	}
	base := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	occurrence, _ := base.accept(t, domain.CronOccurrence)
	cron, err := base.service.ListActivity(base.ctx, connect.NewRequest(&pb.ListActivityRequest{}))
	if err != nil || len(cron.Msg.Entries) != 1 || cron.Msg.Entries[0].Kind != pb.ActivityKind_ACTIVITY_KIND_SCHEDULE_CRON || cron.Msg.Entries[0].OccurrenceId != string(occurrence.ID) {
		t.Fatal("cron source was relabeled Run now", err)
	}
}

func TestActivityRPCRejectsForeignAndStalePagesAndRevokedClients(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	event := f.event(domain.ExecutionTurnFinished, 3)
	event.Outcome = domain.ExecutionFailed
	f.publish(t, event)
	c := delidevv1connect.NewActivityServiceClient(f.http.Client(), f.http.URL)
	ctx := context.Background()
	page, err := c.ListActivity(ctx, ownerRequest(f.service.Identity, &pb.ListActivityRequest{PageSize: 1}))
	if err != nil || page.Msg.NextPageToken == "" {
		t.Fatal(err)
	}
	paired, device := pairedQuestionClient(t, f)
	_, err = c.ListActivity(ctx, ownerRequest(paired, &pb.ListActivityRequest{PageToken: page.Msg.NextPageToken}))
	wantAccountCode(t, err, domain.CursorExpired)
	_, err = c.ListActivity(ctx, ownerRequest(f.service.Identity, &pb.ListActivityRequest{PageToken: page.Msg.NextPageToken, SessionId: string(f.input.SessionID)}))
	wantAccountCode(t, err, domain.CursorExpired)
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err = devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListActivity(ctx, ownerRequest(paired, &pb.ListActivityRequest{})); err == nil {
		t.Fatal("revoked activity reader")
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err = f.service.ListActivity(stale, connect.NewRequest(&pb.ListActivityRequest{})); err == nil {
		t.Fatal("stale authority bypassed revocation")
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.activity-archive", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Archive = domain.Archived
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListActivity(ctx, ownerRequest(f.service.Identity, &pb.ListActivityRequest{PageToken: page.Msg.NextPageToken}))
	wantAccountCode(t, err, domain.CursorExpired)
	for _, bad := range []*pb.ListActivityRequest{{PageSize: 201}, {SessionId: "invalid"}, {ProjectId: "invalid"}} {
		_, err := c.ListActivity(ctx, ownerRequest(f.service.Identity, bad))
		wantAccountCode(t, err, domain.InvalidArgument)
	}
}

func TestActivityExposesPreNativeFailureWithoutInventingTerminalEvidence(t *testing.T) {
	f := newPublicationFixture(t)
	for _, state := range []domain.JobState{domain.JobFailed, domain.JobUncertain, domain.JobCanceled} {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.activity-job-state", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.JobKind, f.job)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return nil, err
			}
			job.State = state
			return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		})
		if err != nil {
			t.Fatal(err)
		}
		client := delidevv1connect.NewActivityServiceClient(f.http.Client(), f.http.URL)
		response, err := client.ListActivity(context.Background(), ownerRequest(f.service.Identity, &pb.ListActivityRequest{}))
		if err != nil || len(response.Msg.Entries) != 1 {
			t.Fatal("invented terminal source", err)
		}
		entry := response.Msg.Entries[0]
		if entry.Kind != pb.ActivityKind_ACTIVITY_KIND_EXECUTION_ACCEPTED || strings.ToLower(strings.TrimPrefix(entry.JobState.String(), "ACTIVITY_JOB_STATE_")) != string(state) {
			t.Fatal("lost pre-native failure/uncertainty state")
		}
	}
}
