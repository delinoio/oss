package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func assertWirePageBound(t *testing.T, message proto.Message) {
	t.Helper()
	raw, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxResourcePageBytes || proto.Size(message) > maxResourcePageBytes {
		t.Fatalf("page exceeds wire bound: binary=%d JSON=%d", proto.Size(message), len(raw))
	}
}
func TestResourcePageFitsCompleteEnvelopeBoundary(t *testing.T) {
	message := &pb.ListSchedulesResponse{NextPageToken: "cursor", Schedules: []*pb.Resource{{Id: "a", DocumentJson: bytes.Repeat([]byte("\""), 3<<20)}}}
	raw, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	for len(raw) > maxResourcePageBytes {
		message.Schedules[0].DocumentJson = message.Schedules[0].DocumentJson[:len(message.Schedules[0].DocumentJson)-3]
		raw, err = protojson.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
	}
	message.NextPageToken += strings.Repeat("x", maxResourcePageBytes-len(raw))
	raw, _ = protojson.Marshal(message)
	if len(raw) != maxResourcePageBytes {
		t.Fatalf("boundary=%d", len(raw))
	}
	fits, err := resourcePageFits(message)
	if err != nil || !fits {
		t.Fatalf("exact boundary fits=%v error=%v", fits, err)
	}
	message.NextPageToken += "x"
	fits, err = resourcePageFits(message)
	if err != nil || fits {
		t.Fatalf("oversized envelope fits=%v error=%v", fits, err)
	}
	message.Schedules[0].DocumentJson = bytes.Repeat([]byte("a"), 4<<20)
	fits, err = resourcePageFits(message)
	if err != nil || fits {
		t.Fatalf("single unfit entry fits=%v error=%v", fits, err)
	}
	if domain.SafeError(resourcePageTooLarge()).Code != domain.ResourceExhausted {
		t.Fatal("wrong single-entry failure")
	}
}

func TestProviderAndModelPagesBoundBothWireEncodings(t *testing.T) {
	f := newAccountFixture(t)
	var providerIDs, modelIDs []string
	for i := range 5 {
		provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: fmt.Sprintf("wire-provider-%d", i), Endpoint: "https://fixture.invalid/" + strings.Repeat("x", 900<<10), Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth})
		providerIDs = append(providerIDs, provider.Id)
		for j := range 2 {
			model := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(provider.Id), NativeID: fmt.Sprintf("wire-model-%d-%d", i, j), Name: fmt.Sprintf("wire-model-%d-%d", i, j), MetadataSource: domain.UserDeclared, Harnesses: []domain.Harness{domain.Codex}})
			modelIDs = append(modelIDs, model.Id)
		}
	}
	httpClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	defer httpClient.CloseIdleConnections()
	for _, jsonEncoding := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonEncoding), func(t *testing.T) {
			var options []connect.ClientOption
			if jsonEncoding {
				options = []connect.ClientOption{connect.WithProtoJSON(), connect.WithAcceptCompression("gzip", nil, nil)}
			}
			client := delidevv1connect.NewProviderServiceClient(httpClient, f.endpoint.URL, options...)
			ids := []string{}
			token := ""
			pages := 0
			for {
				request := &pb.ListProviderInventoryRequest{Query: "wire-provider", PageSize: 5, PageToken: token}
				response, err := client.ListProviderInventory(context.Background(), ownerRequest(f.identity, request))
				if err != nil {
					t.Fatal(err)
				}
				assertWirePageBound(t, response.Msg)
				pages++
				for _, entry := range response.Msg.Entries {
					ids = append(ids, entry.ProviderId)
					if entry.Provider == nil {
						t.Fatal("missing complete provider")
					}
				}
				replay, err := client.ListProviderInventory(context.Background(), ownerRequest(f.identity, request))
				if err != nil || !proto.Equal(&pb.ListProviderInventoryResponse{Entries: response.Msg.Entries}, &pb.ListProviderInventoryResponse{Entries: replay.Msg.Entries}) {
					t.Fatalf("inventory replay changed: %v", err)
				}
				if response.Msg.NextPageToken == "" {
					break
				}
				if len(response.Msg.Entries) == 0 || response.Msg.NextPageToken == token {
					t.Fatal("nonadvancing inventory page")
				}
				token = response.Msg.NextPageToken
			}
			if !slices.Equal(ids, providerIDs) || pages < 2 {
				t.Fatalf("inventory enumeration ids=%v pages=%d", ids, pages)
			}
			seen := map[string]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Fatal("duplicate inventory identity")
				}
				seen[id] = true
			}
			ids = nil
			token = ""
			pages = 0
			for {
				request := &pb.SearchModelsRequest{Query: "wire-model", PageSize: 10, PageToken: token}
				response, err := client.SearchModels(context.Background(), ownerRequest(f.identity, request))
				if err != nil {
					t.Fatal(err)
				}
				assertWirePageBound(t, response.Msg)
				pages++
				represented := map[string]bool{}
				for _, model := range response.Msg.Models {
					ids = append(ids, model.Id)
					represented[string(catalogBody(t, model).ProviderID)] = true
				}
				if len(represented) != len(response.Msg.Providers) {
					t.Fatal("missing or duplicated represented providers")
				}
				for _, provider := range response.Msg.Providers {
					if !represented[provider.Id] {
						t.Fatal("unrepresented provider")
					}
				}
				replay, err := client.SearchModels(context.Background(), ownerRequest(f.identity, request))
				if err != nil || !proto.Equal(&pb.SearchModelsResponse{Models: response.Msg.Models, Providers: response.Msg.Providers}, &pb.SearchModelsResponse{Models: replay.Msg.Models, Providers: replay.Msg.Providers}) {
					t.Fatalf("model replay changed: %v", err)
				}
				if response.Msg.NextPageToken == "" {
					break
				}
				if len(response.Msg.Models) == 0 || response.Msg.NextPageToken == token {
					t.Fatal("nonadvancing model page")
				}
				token = response.Msg.NextPageToken
			}
			if !slices.Equal(ids, modelIDs) || pages < 2 {
				t.Fatalf("model enumeration count=%d pages=%d", len(ids), pages)
			}
			seen = map[string]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Fatal("duplicate model identity")
				}
				seen[id] = true
			}
		})
	}
}

func TestScheduleAndOccurrencePagesBoundBothWireEncodings(t *testing.T) {
	f := newScheduleRPCFixture(t)
	var original *pb.Resource
	for i := range 12 {
		definition := f.definition
		definition.Name = fmt.Sprintf("wire-schedule-%d", i)
		definition.Prompt = strings.Repeat("a", 262140)
		definition.Enabled = false
		row := f.save(t, f.saveRequest(t, "", 0, definition, ""))
		if original == nil {
			original = row
		}
	}
	for range 12 {
		f.run(t, f.current(t, original.Id))
	}
	httpClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	defer httpClient.CloseIdleConnections()
	for _, jsonEncoding := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonEncoding), func(t *testing.T) {
			var options []connect.ClientOption
			if jsonEncoding {
				options = []connect.ClientOption{connect.WithProtoJSON(), connect.WithAcceptCompression("gzip", nil, nil)}
			}
			client := delidevv1connect.NewScheduleServiceClient(httpClient, f.http.URL, options...)
			ids := map[string]bool{}
			token := ""
			pages := 0
			for {
				request := &pb.ListSchedulesRequest{PageSize: 200, PageToken: token}
				response, err := client.ListSchedules(f.ctx, ownerRequest(f.service.Identity, request))
				if err != nil {
					t.Fatal(err)
				}
				assertWirePageBound(t, response.Msg)
				pages++
				for _, row := range response.Msg.Schedules {
					if ids[row.Id] {
						t.Fatal("duplicate schedule")
					}
					ids[row.Id] = true
				}
				replay, err := client.ListSchedules(f.ctx, ownerRequest(f.service.Identity, request))
				if err != nil || !proto.Equal(&pb.ListSchedulesResponse{Schedules: response.Msg.Schedules}, &pb.ListSchedulesResponse{Schedules: replay.Msg.Schedules}) {
					t.Fatalf("schedule replay changed: %v", err)
				}
				if response.Msg.NextPageToken == "" {
					break
				}
				if len(response.Msg.Schedules) == 0 {
					t.Fatal("empty schedule continuation")
				}
				token = response.Msg.NextPageToken
			}
			if len(ids) != 12 || pages < 2 {
				t.Fatalf("schedule count=%d pages=%d", len(ids), pages)
			}
			ids = map[string]bool{}
			token = ""
			pages = 0
			sequence := uint64(0)
			for {
				request := &pb.ListScheduleOccurrencesRequest{ScheduleId: original.Id, PageSize: 200, PageToken: token}
				response, err := client.ListScheduleOccurrences(f.ctx, ownerRequest(f.service.Identity, request))
				if err != nil {
					t.Fatal(err)
				}
				assertWirePageBound(t, response.Msg)
				pages++
				for _, row := range response.Msg.Occurrences {
					if ids[row.Id] {
						t.Fatal("duplicate occurrence")
					}
					ids[row.Id] = true
					var occurrence domain.ScheduleOccurrence
					if err := domain.Decode(row.DocumentJson, &occurrence); err != nil {
						t.Fatal(err)
					}
					sequence++
					if occurrence.Sequence != sequence {
						t.Fatalf("sequence=%d want=%d", occurrence.Sequence, sequence)
					}
				}
				replay, err := client.ListScheduleOccurrences(f.ctx, ownerRequest(f.service.Identity, request))
				if err != nil || !proto.Equal(&pb.ListScheduleOccurrencesResponse{Occurrences: response.Msg.Occurrences}, &pb.ListScheduleOccurrencesResponse{Occurrences: replay.Msg.Occurrences}) {
					t.Fatalf("occurrence replay changed: %v", err)
				}
				if response.Msg.NextPageToken == "" {
					break
				}
				if len(response.Msg.Occurrences) == 0 {
					t.Fatal("empty occurrence continuation")
				}
				token = response.Msg.NextPageToken
			}
			if len(ids) != 12 || pages < 2 {
				t.Fatalf("occurrence count=%d pages=%d", len(ids), pages)
			}
		})
	}
}
