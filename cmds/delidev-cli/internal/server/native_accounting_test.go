package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestClaudeNativeAccountingRPCProfilesFailureBudgetAndPrivacy(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "uniform", true: "split"}[split], func(t *testing.T) {
			f := newClaudePublicationFixture(t, domain.ExecuteMode)
			ctx := context.Background()
			c := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
			input, output, read := "1", "0", "0.5"
			price := publicPrice("USD")
			price.InputPerMillion = &input
			price.OutputPerMillion = &output
			amount := "0.000025"
			if split {
				price.InputMode = pb.InputPricingMode_INPUT_PRICING_MODE_CACHED_DISCOUNT
				price.CachedInputPerMillion = &read
				amount = "0.0000165"
			}
			_, err := c.SetModelPricing(ctx, ownerRequest(f.service.Identity, &pb.SetModelPricingRequest{Mutation: &pb.Mutation{Id: string(f.input.Configuration.ModelID), RequestId: string(domain.NewID())}, ExpectedModelRevision: 1, Basis: price}))
			if err != nil {
				t.Fatal(err)
			}
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			user := &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: string(f.input.InputID), Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
			for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
				e := f.event(kind, 3+uint64(i))
				e.Message = user
				f.publish(t, e)
			}
			a, b, w, z, large := domain.ClaudeUsageCount("13"), domain.ClaudeUsageCount("7"), domain.ClaudeUsageCount("5"), domain.ClaudeUsageCount("0"), domain.ClaudeUsageCount("9007199254740993")
			cost := domain.ClaudeNativeUSD("999")
			resultID := string(domain.NewID())
			e := f.event(domain.ExecutionClaudeUsageObserved, 5)
			e.ObservationID = domain.NewID()
			e.ClaudeUsage = &domain.ClaudeUsageObservation{Source: domain.ClaudeInputResultUsage, NativeEventID: resultID, Result: &domain.ClaudeResultUsage{MainLoop: &domain.ClaudeProviderUsage{Input: &a, CacheRead: &b, CacheWrite: &w, Output: &z, OutputDetail: &domain.ClaudeOutputTokenDetails{Thinking: &z}}, Models: map[string]domain.ClaudeNativeModelUsage{"cumulative": {Input: &large}}, NativeCostUSD: &cost}}
			receipt := f.publish(t, e)
			if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
				t.Fatal("publication replay", err)
			}
			terminal := f.event(domain.ExecutionTurnFinished, 6)
			terminal.Outcome = domain.ExecutionFailed
			terminal.ClaudeTerminal = &domain.ClaudeTerminalObservation{InputID: f.input.InputID, ResultNativeID: resultID, CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), Kind: domain.ClaudeResultExecutionError, Reason: domain.ClaudeAPIError, Error: true, Command: domain.ClaudeCommandCancelled}
			f.publish(t, terminal)
			legacy, err := c.GetUsageSummary(ctx, ownerRequest(f.service.Identity, &pb.GetUsageSummaryRequest{}))
			if err != nil || legacy.Msg.NativeAccounting != nil || legacy.Msg.Totals.Responses != 0 {
				t.Fatal("legacy response-only result changed", err)
			}
			query := &pb.GetUsageSummaryRequest{AccountingProfile: pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1, Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY, TimeZone: "Asia/Seoul", SessionId: string(f.input.SessionID)}
			response, err := c.GetUsageSummary(ctx, ownerRequest(f.service.Identity, query))
			if err != nil {
				t.Fatal(err)
			}
			v := response.Msg.NativeAccounting[0]
			if v == nil || response.Msg.AccountingProfile != query.AccountingProfile || v.Totals.Units != 1 || v.Totals.Input.KnownTotal != "25" || v.Totals.Output.KnownTotal != "0" || v.Totals.Total.KnownTotal != "" || v.Totals.Currencies[0].KnownAmount != amount || len(v.Groups) != 1 || len(v.Models) != 1 || len(v.Days) == 0 || v.ActualCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE {
				t.Fatal("lost exact correlated failure usage", v)
			}
			if split && v.Pricing[0].CacheWrite.MissingPriceUnits != 1 {
				t.Fatal("cache write silently priced", v.Pricing)
			}
			raw, _ := protojson.Marshal(response.Msg)
			// This Claude fixture's native thread uses the public session UUID, so
			// its value legitimately appears as session_id in grouped analytics.
			for _, private := range []string{f.input.Input.Prompt, f.workerToken, string(f.turn), resultID, "native_cumulative_cost", `"999"`} {
				if strings.Contains(string(raw), private) {
					t.Fatal("aggregate exposed private native evidence", private)
				}
			}
			row, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			sessions := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			budget, err := sessions.SetSessionBudget(ctx, ownerRequest(f.service.Identity, &pb.SetSessionBudgetRequest{Mutation: &pb.Mutation{Id: string(row.ID), ExpectedRevision: row.Revision, RequestId: string(domain.NewID())}, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "USD", Threshold: amount}}}))
			if err != nil {
				t.Fatal(err)
			}
			if budget.Msg.View.State != pb.BudgetState_BUDGET_STATE_THRESHOLD_REACHED || budget.Msg.View.SelectedCurrency.KnownAmount != amount || budget.Msg.View.SelectedCurrency.CompleteResponses != 0 || budget.Msg.View.SelectedCurrency.PartialResponses != 0 || budget.Msg.View.SelectedCurrency.CompleteNativeUnits+budget.Msg.View.SelectedCurrency.PartialNativeUnits != 1 {
				t.Fatal("native gate lost unit distinction", budget.Msg.View)
			}
			query.AccountId = string(domain.NewID())
			empty, err := c.GetUsageSummary(ctx, ownerRequest(f.service.Identity, query))
			if err != nil || empty.Msg.NativeAccounting[0].Totals.Units != 0 {
				t.Fatal("event-time filter ignored", err)
			}
			query.AccountingProfile = pb.UsageAccountingProfile(99)
			if _, err := c.GetUsageSummary(ctx, ownerRequest(f.service.Identity, query)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatal("unknown profile accepted", err)
			}
			query.AccountingProfile = pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1
			worker := connect.NewRequest(query)
			worker.Header().Set("Authorization", "Bearer "+f.workerToken)
			if _, err := c.GetUsageSummary(ctx, worker); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("worker read accounting", err)
			}
		})
	}
}

func TestClaudeUncorrelatedDenialDoesNotCreateAccountingUnit(t *testing.T) {
	f, e := denialCompletionPublicationFixture(t)
	f.publish(t, e)
	var summary domain.UsageSummary
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		summary, err = tx.UsageSummary(domain.UsageSelection{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), AccountingProfile: domain.NativeUnitsV1Accounting})
		return err
	})
	if err != nil || summary.NativeAccounting == nil || summary.NativeAccounting[0].Totals.Units != 0 || summary.Totals.Responses != 0 {
		t.Fatal("uncorrelated denial entered accounting", summary, err)
	}
}
