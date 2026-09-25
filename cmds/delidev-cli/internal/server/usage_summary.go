package server

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func usageMeasure(value domain.UsageMeasure) *pb.UsageMeasure {
	return &pb.UsageMeasure{KnownTotal: value.KnownTotal, MeasuredResponses: value.MeasuredResponses, UnavailableResponses: value.UnavailableResponses}
}
func usageTotals(value domain.UsageTotals) *pb.UsageTotals {
	return &pb.UsageTotals{Responses: value.Responses, Input: usageMeasure(value.Input), CachedInput: usageMeasure(value.CachedInput), CacheWriteInput: usageMeasure(value.CacheWriteInput), Output: usageMeasure(value.Output), ReasoningOutput: usageMeasure(value.ReasoningOutput), Total: usageMeasure(value.Total)}
}
func (s *Service) GetUsageSummary(ctx context.Context, req *connect.Request[pb.GetUsageSummaryRequest]) (*connect.Response[pb.GetUsageSummaryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can read usage.", "Use an authorized product client."), correlation)
	}
	until := req.Msg.UntilUnixMs
	if until == 0 {
		until = time.Now().UTC().UnixMilli()
	}
	from := req.Msg.FromUnixMs
	if from == 0 {
		from = until - (30 * 24 * time.Hour).Milliseconds()
	}
	f := domain.UsageSelection{From: time.UnixMilli(from), Until: time.UnixMilli(until), SessionID: domain.ID(req.Msg.SessionId), ProjectID: domain.ID(req.Msg.ProjectId), AccountID: domain.ID(req.Msg.AccountId), ProviderID: domain.ID(req.Msg.ProviderId), ModelID: domain.ID(req.Msg.ModelId), GeneralChat: req.Msg.GeneralChat}
	if err := f.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := &pb.GetUsageSummaryResponse{FromUnixMs: from, UntilUnixMs: until, Coverage: pb.UsageCoverage_USAGE_COVERAGE_OBSERVED_ROOT_RESPONSES, ActualCost: pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE, EstimatedCost: pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE}
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		summary, err := tx.UsageSummary(f)
		if err != nil {
			return err
		}
		result.Totals = usageTotals(summary.Totals)
		result.AcceptedExecutionsWithoutResponse = summary.AcceptedExecutionsWithoutResponse
		labels := map[domain.ID]string{}
		for _, group := range summary.Groups {
			row := &pb.UsageGroup{SessionId: string(group.SessionID), ProjectId: string(group.ProjectID), AccountId: string(group.AccountID), ProviderId: string(group.ProviderID), ModelId: string(group.ModelID), Totals: usageTotals(group.Totals)}
			for _, part := range []struct {
				kind   domain.Kind
				id     domain.ID
				target *string
			}{{domain.SessionKind, group.SessionID, &row.SessionName}, {domain.ProjectKind, group.ProjectID, &row.ProjectName}, {domain.AccountKind, group.AccountID, &row.AccountName}, {domain.ProviderKind, group.ProviderID, &row.ProviderName}, {domain.ModelKind, group.ModelID, &row.ModelName}} {
				if part.id == "" {
					continue
				}
				label, known := labels[part.id]
				if !known {
					resource, err := tx.Get(part.kind, part.id)
					if err != nil && domain.SafeError(err).Code != domain.NotFound {
						return err
					}
					if err == nil {
						var value struct {
							Name  string `json:"name"`
							Alias string `json:"alias"`
						}
						if json.Unmarshal(resource.Data, &value) != nil {
							return invalidUsageSummary()
						}
						label = value.Name
						if part.kind == domain.AccountKind {
							label = value.Alias
						}
						if domain.Text(label, "usage display label", 1024, false) != nil {
							return invalidUsageSummary()
						}
					}
					labels[part.id] = label
				}
				*part.target = label
			}
			result.Groups = append(result.Groups, row)
		}
		encoded, err := protojson.Marshal(result)
		if err != nil {
			return invalidUsageSummary()
		}
		if len(encoded) > 2<<20 || proto.Size(result) > 2<<20 {
			return domain.Fail(domain.ResourceExhausted, "The usage response exceeds its byte bound.", "Choose a shorter time range or more specific filters; no partial total was returned.")
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.DebugContext(ctx, "usage_summary_read_completed", "correlation_id", correlation, "group_count", len(result.Groups), "response_count", result.Totals.Responses)
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func invalidUsageSummary() error {
	return domain.Fail(domain.RecoveryRequired, "Retained usage display metadata is inconsistent.", "Inspect the original records without rewriting usage attribution.")
}
