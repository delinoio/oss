package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestUsageRPCDeduplicatedSnapshotDefaultsFiltersAndPrivacy(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	counts := reportedCounts(20)
	e := f.event(domain.ExecutionResponseUsageObserved, 3)
	e.ObservationID, e.ResponseUsage = domain.NewID(), &domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), Counts: &counts, CostEvidence: domain.UsageCostUnspecified}
	f.publish(t, e)
	e.Sequence = 4
	e.ObservationID = domain.NewID()
	f.publish(t, e)
	c := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
	request := &pb.GetUsageSummaryRequest{AccountId: string(f.input.AccountID), ModelId: string(f.input.Configuration.ModelID)}
	response, err := c.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	result := response.Msg
	if result.Totals.Responses != 1 || result.Totals.Total.KnownTotal != "20" || len(result.Groups) != 1 || result.Analytics != nil || result.ActualCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE || result.Coverage != pb.UsageCoverage_USAGE_COVERAGE_OBSERVED_ROOT_RESPONSES || result.UntilUnixMs-result.FromUnixMs != (30*24*time.Hour).Milliseconds() || result.AcceptedExecutionsWithoutResponse != 0 || response.Header().Get(rpc.CorrelationHeader) == "" {
		t.Fatalf("wrong usage summary: %+v", result)
	}
	dailyRequest := &pb.GetUsageSummaryRequest{Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY, TimeZone: "Asia/Seoul"}
	daily, err := c.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, dailyRequest))
	if err != nil || daily.Msg.Analytics == nil || daily.Msg.Analytics.Granularity != dailyRequest.Granularity || daily.Msg.Analytics.TimeZone != dailyRequest.TimeZone || len(daily.Msg.Analytics.Days) == 0 {
		t.Fatalf("valid daily analytics request lost explicit zone or empty-day support: %+v %v", daily, err)
	}
	raw, _ := protojson.Marshal(result)
	for _, private := range []string{f.input.Input.Prompt, f.workerToken, string(f.thread), string(f.turn), e.ResponseUsage.ResponseDigest} {
		if strings.Contains(string(raw), private) {
			t.Fatal("usage exposed native/credential data")
		}
	}
	request.AccountId = string(domain.NewID())
	empty, err := c.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || empty.Msg.Totals.Responses != 0 || empty.Msg.Totals.Total.KnownTotal != "" {
		t.Fatal("filter not enforced", err)
	}
	for _, bad := range []*pb.GetUsageSummaryRequest{{FromUnixMs: -1}, {UntilUnixMs: -1}, {FromUnixMs: 100, UntilUnixMs: 99}, {ProjectId: string(domain.NewID()), GeneralChat: true}, {AccountId: "invalid"}, {Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY}, {Granularity: pb.UsageTimeGranularity(99), TimeZone: "UTC"}, {TimeZone: "UTC"}, {Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY, TimeZone: "Local"}, {Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY, TimeZone: " Asia/Seoul"}} {
		if _, err := c.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid filter accepted", err)
		}
	}
	worker := connect.NewRequest(&pb.GetUsageSummaryRequest{})
	worker.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err := c.GetUsageSummary(context.Background(), worker); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker read usage", err)
	}
	if _, err := c.GetUsageSummary(context.Background(), connect.NewRequest(&pb.GetUsageSummaryRequest{})); err == nil {
		t.Fatal("unauthenticated usage")
	}
	paired, device := pairedQuestionClient(t, f)
	if _, err := c.GetUsageSummary(context.Background(), ownerRequest(paired, &pb.GetUsageSummaryRequest{})); err != nil {
		t.Fatal("paired reader rejected", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err := devices.RevokeDevice(context.Background(), ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	stale := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.GetUsageSummary(stale, connect.NewRequest(&pb.GetUsageSummaryRequest{})); err == nil {
		t.Fatal("stale authority bypassed transactional revocation")
	}

}
