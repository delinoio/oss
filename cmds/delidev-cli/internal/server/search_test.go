package server

import (
	"context"
	"encoding/base64"
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
	"google.golang.org/protobuf/proto"
)

func seedSearchMessages(t *testing.T, f *publicationFixture, text string, count int) []domain.ID {
	t.Helper()
	ids := []domain.ID{}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.search-messages", nil, func(tx *store.Tx) (any, error) {
		for range count {
			id := domain.NewID()
			ids = append(ids, id)
			if _, err := tx.Put(domain.MessageKind, id, 0, f.input.SessionID, "", domain.ExecutionMessage{ExecutionID: f.input.ExecutionID, Text: text, Role: domain.AssistantMessage, State: domain.MessageComplete}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestSearchRPCScopeEpochRevocationAndNoQueryInCursor(t *testing.T) {
	f := newPublicationFixture(t)
	ids := seedSearchMessages(t, f, "private-search-marker searchable transcript", 3)
	c := delidevv1connect.NewSearchServiceClient(f.http.Client(), f.http.URL)
	ctx := context.Background()
	request := &pb.SearchConversationsRequest{Query: "private-search-marker", PageSize: 1, SessionId: string(f.input.SessionID), AccountId: string(f.input.AccountID)}
	first, err := c.SearchConversations(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(first.Msg.Hits) != 1 || first.Msg.NextPageToken == "" {
		t.Fatal("missing first page", err)
	}
	if first.Msg.Hits[0].Message.Id != string(ids[0]) || first.Header().Get(rpc.CorrelationHeader) == "" {
		t.Fatal("lost source/correlation")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(first.Msg.NextPageToken, ".")[0])
	if err != nil || strings.Contains(string(raw), "private-search-marker") {
		t.Fatal("cursor revealed query", err)
	}
	for _, field := range []string{"query", "session", "account", "agent", "project", "outcome", "archive"} {
		next := proto.Clone(request).(*pb.SearchConversationsRequest)
		next.PageToken = first.Msg.NextPageToken
		switch field {
		case "query":
			next.Query = "other"
		case "session":
			next.SessionId = string(domain.NewID())
		case "account":
			next.AccountId = string(domain.NewID())
		case "agent":
			next.AgentId = string(domain.NewID())
		case "project":
			next.ProjectId = string(domain.NewID())
		case "outcome":
			next.Outcome = pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_FAILED
		case "archive":
			next.Archive = pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_ARCHIVED
		}
		_, err := c.SearchConversations(ctx, ownerRequest(f.service.Identity, next))
		wantAccountCode(t, err, domain.CursorExpired)
	}
	paired, device := pairedQuestionClient(t, f)
	next := proto.Clone(request).(*pb.SearchConversationsRequest)
	next.PageToken = first.Msg.NextPageToken
	_, err = c.SearchConversations(ctx, ownerRequest(paired, next))
	wantAccountCode(t, err, domain.CursorExpired)
	if _, err = c.SearchConversations(ctx, ownerRequest(paired, request)); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []security.Identity{{}, {Token: f.workerToken}} {
		if _, err := c.SearchConversations(ctx, ownerRequest(actor, request)); err == nil {
			t.Fatal("unauthorized transcript search")
		}
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err = devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SearchConversations(ctx, ownerRequest(paired, request)); err == nil {
		t.Fatal("revoked client searched")
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err = f.service.SearchConversations(stale, connect.NewRequest(request)); err == nil {
		t.Fatal("stale authentication bypassed transactional revocation")
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.archive-search", nil, func(tx *store.Tx) (any, error) {
		r, v, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		v.Archive = domain.Archived
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SearchConversations(ctx, ownerRequest(f.service.Identity, next))
	wantAccountCode(t, err, domain.CursorExpired)
	request.PageSize = 200
	request.Archive = pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_ARCHIVED
	page, err := c.SearchConversations(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || len(page.Msg.Hits) != 3 {
		t.Fatal("Archive excluded", err)
	}
	for _, bad := range []*pb.SearchConversationsRequest{{Query: ""}, {Query: "   "}, {Query: "\x1f"}, {Query: "ok", PageSize: 201}, {Query: "ok", Outcome: 99}, {Query: "ok", Archive: 99}, {Query: "ok", AccountId: "bad"}, {Query: strings.Repeat("a", 1025)}} {
		_, err := c.SearchConversations(ctx, ownerRequest(f.service.Identity, bad))
		wantAccountCode(t, err, domain.InvalidArgument)
	}
}

func TestSearchRPCByteBoundsPreserveCompleteMessagesAndBothEncodings(t *testing.T) {
	f := newPublicationFixture(t)
	text := "needle " + strings.Repeat("z", 250<<10)
	ids := seedSearchMessages(t, f, text, 18)
	for _, jsonWire := range []bool{false, true} {
		var opts []connect.ClientOption
		if jsonWire {
			opts = append(opts, connect.WithProtoJSON())
		}
		c := delidevv1connect.NewSearchServiceClient(f.http.Client(), f.http.URL, opts...)
		request := &pb.SearchConversationsRequest{Query: "needle", PageSize: 200}
		seen := map[string]bool{}
		pages := 0
		for {
			response, err := c.SearchConversations(context.Background(), ownerRequest(f.service.Identity, request))
			if err != nil {
				t.Fatal(err)
			}
			pages++
			encoded, err := protojson.Marshal(response.Msg)
			if err != nil || len(encoded) >= 5<<20 || proto.Size(response.Msg) >= 5<<20 {
				t.Fatal("wire overflow", err)
			}
			for _, hit := range response.Msg.Hits {
				if seen[hit.Message.Id] {
					t.Fatal("duplicate result")
				}
				seen[hit.Message.Id] = true
				var value domain.ExecutionMessage
				if domain.Decode(hit.Message.DocumentJson, &value) != nil || value.Text != text {
					t.Fatal("truncated canonical text")
				}
			}
			if response.Msg.NextPageToken == "" {
				break
			}
			request.PageToken = response.Msg.NextPageToken
		}
		if len(seen) != len(ids) || pages < 2 {
			t.Fatal("missing byte-bounded page", pages, len(seen))
		}
	}
}
