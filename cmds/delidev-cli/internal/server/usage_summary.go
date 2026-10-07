package server

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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
	result := &pb.UsageTotals{Responses: value.Responses, Input: usageMeasure(value.Input), CachedInput: usageMeasure(value.CachedInput), CacheWriteInput: usageMeasure(value.CacheWriteInput), Output: usageMeasure(value.Output), ReasoningOutput: usageMeasure(value.ReasoningOutput), Total: usageMeasure(value.Total)}
	for _, unit := range value.Accounting {
		wire := &pb.AccountingTotals{Kind: pb.AccountingUnitKind(unit.Kind), Units: unit.Units, KnownTotal: unit.KnownTotal, MeasuredUnits: unit.MeasuredUnits, UnavailableUnits: unit.UnavailableUnits}
		wire.ActualCost = pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE
		if unit.Kind == domain.GrokClosedInput {
			wire.EstimatedCost = pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE
		}
		result.Accounting = append(result.Accounting, wire)
	}
	return result
}
func (s *Service) GetUsageSummary(ctx context.Context, req *connect.Request[pb.GetUsageSummaryRequest]) (*connect.Response[pb.GetUsageSummaryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	_, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential."), correlation)
	}
	until := req.Msg.UntilUnixMs
	if until == 0 {
		until = time.Now().UTC().UnixMilli()
	}
	from := req.Msg.FromUnixMs
	if from == 0 {
		from = until - (30 * 24 * time.Hour).Milliseconds()
	}
	f := domain.UsageSelection{From: time.UnixMilli(from), Until: time.UnixMilli(until), SessionID: domain.ID(req.Msg.SessionId), ProjectID: domain.ID(req.Msg.ProjectId), AccountID: domain.ID(req.Msg.AccountId), ProviderID: domain.ID(req.Msg.ProviderId), SubscriptionService: rpc.SubscriptionService(req.Msg.SubscriptionService), ModelID: domain.ID(req.Msg.ModelId), GeneralChat: req.Msg.GeneralChat, Granularity: domain.UsageTimeGranularity(req.Msg.Granularity), TimeZone: req.Msg.TimeZone, AccountingProfile: domain.AccountingProfile(req.Msg.AccountingProfile)}
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
		result.AccountingProfile = req.Msg.AccountingProfile
		result.Totals = usageTotals(summary.Totals)
		for _, value := range summary.NativeAccounting {
			result.NativeAccounting = append(result.NativeAccounting, nativeSummary(value))
		}
		result.Estimates = estimateTotals(summary.Estimates)
		for _, value := range summary.Estimates.Currencies {
			if value.KnownAmount != "" {
				result.EstimatedCost = pb.UsageCostState_USAGE_COST_STATE_KNOWN_SUBTOTAL
			}
		}
		for _, value := range summary.Pricing {
			result.Pricing = append(result.Pricing, pricingUsage(value))
		}
		result.AcceptedExecutionsWithoutResponse = summary.AcceptedExecutionsWithoutResponse
		result.AcceptedCompactionsWithoutResponse = summary.AcceptedCompactionsWithoutResponse
		type usageLabelKey struct {
			kind domain.Kind
			id   domain.ID
		}
		labels := map[usageLabelKey]string{}
		usageLabel := func(kind domain.Kind, id domain.ID) (string, error) {
			if id == "" {
				return "", nil
			}
			key := usageLabelKey{kind: kind, id: id}
			if label, known := labels[key]; known {
				return label, nil
			}
			resource, err := tx.Get(kind, id)
			if domain.SafeError(err).Code == domain.NotFound && (kind == domain.AccountKind || kind == domain.ModelKind || kind == domain.ProviderKind) {
				resource, err = tx.RetiredConfiguration(kind, id)
			}
			if err != nil && domain.SafeError(err).Code != domain.NotFound {
				return "", err
			}
			label := ""
			if err == nil {
				var value struct {
					Name  string `json:"name"`
					Alias string `json:"alias"`
				}
				if json.Unmarshal(resource.Data, &value) != nil {
					return "", invalidUsageSummary()
				}
				label = value.Name
				if kind == domain.AccountKind {
					label = value.Alias
				}
				if domain.Text(label, "usage display label", 1024, false) != nil {
					return "", invalidUsageSummary()
				}
			}
			labels[key] = label
			return label, nil
		}
		for _, group := range summary.Groups {
			row := &pb.UsageGroup{SessionId: string(group.SessionID), ProjectId: string(group.ProjectID), AccountId: string(group.AccountID), ProviderId: string(group.ProviderID), SubscriptionService: rpc.WireSubscriptionService(group.SubscriptionService), ModelId: string(group.ModelID), Totals: usageTotals(group.Totals), Estimates: estimateTotals(group.Estimates)}
			for _, part := range []struct {
				kind   domain.Kind
				id     domain.ID
				target *string
			}{{domain.SessionKind, group.SessionID, &row.SessionName}, {domain.ProjectKind, group.ProjectID, &row.ProjectName}, {domain.AccountKind, group.AccountID, &row.AccountName}, {domain.ProviderKind, group.ProviderID, &row.ProviderName}, {domain.ModelKind, group.ModelID, &row.ModelName}} {
				if part.id == "" {
					continue
				}
				label, err := usageLabel(part.kind, part.id)
				if err != nil {
					return err
				}
				*part.target = label
			}
			result.Groups = append(result.Groups, row)
		}
		if analytics := summary.Analytics; analytics != nil {
			wire := &pb.UsageAnalytics{Granularity: pb.UsageTimeGranularity(analytics.Granularity), TimeZone: analytics.TimeZone, Days: make([]*pb.UsageAnalyticsDay, 0, len(analytics.Days)), Models: make([]*pb.UsageAnalyticsModel, 0, len(analytics.Models))}
			for _, day := range analytics.Days {
				wire.Days = append(wire.Days, &pb.UsageAnalyticsDay{FromUnixMs: day.From.UnixMilli(), UntilUnixMs: day.Until.UnixMilli(), Totals: usageTotals(day.Totals)})
			}
			for _, model := range analytics.Models {
				provider, err := usageLabel(domain.ProviderKind, model.ProviderID)
				if err != nil {
					return err
				}
				name, err := usageLabel(domain.ModelKind, model.ModelID)
				if err != nil {
					return err
				}
				wire.Models = append(wire.Models, &pb.UsageAnalyticsModel{ProviderId: string(model.ProviderID), SubscriptionService: rpc.WireSubscriptionService(model.SubscriptionService), ModelId: string(model.ModelID), ProviderName: provider, ModelName: name, Totals: usageTotals(model.Totals)})
			}
			if other := analytics.OtherModels; other != nil {
				wire.OtherModels = &pb.UsageOtherModels{ModelCount: other.ModelCount, Totals: usageTotals(other.Totals)}
			}
			result.Analytics = wire
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
	dayBucketCount, modelGroupCount := 0, 0
	accountingUnits := uint32(0)
	for _, unit := range result.Totals.Accounting {
		accountingUnits += unit.Units
	}
	if result.Analytics != nil {
		dayBucketCount, modelGroupCount = len(result.Analytics.Days), len(result.Analytics.Models)
	}
	s.logger.DebugContext(ctx, "usage_summary_read_completed", "correlation_id", correlation, "accounting_unit_count", accountingUnits, "group_count", len(result.Groups), "response_count", result.Totals.Responses, "day_bucket_count", dayBucketCount, "model_group_count", modelGroupCount)
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func invalidUsageSummary() error {
	return domain.Fail(domain.RecoveryRequired, "Retained usage display metadata is inconsistent.", "Inspect the original records without rewriting usage attribution.")
}
