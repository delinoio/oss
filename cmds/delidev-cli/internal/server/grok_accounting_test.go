package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func grokAccountingSummary(t *testing.T, f *publicationFixture, profile pb.UsageAccountingProfile) *pb.GetUsageSummaryResponse {
	t.Helper()
	c := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
	response, err := c.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, &pb.GetUsageSummaryRequest{AccountingProfile: profile, Granularity: pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY, TimeZone: "UTC"}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func grokCompletion(f *publicationFixture, sequence uint64) domain.ExecutionCompletion {
	return domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: sequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
}

func TestGrokAccountingRequiresCleanupAndKeepsOriginalUnitOnce(t *testing.T) {
	for _, total := range []string{"16", "0", "18446744073709551615"} {
		t.Run(total, func(t *testing.T) {
			f, terminal := grokServerTerminalFixture(t, true)
			terminal.GrokTerminal.TotalTokens = total
			f.publish(t, terminal)
			profile := pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1
			if got := grokAccountingSummary(t, f, profile); len(got.Totals.Accounting) != 0 {
				t.Fatal("terminal alone produced an aggregate unit")
			}
			report, _ := f.reportCompletion(t, grokCompletion(f, terminal.Sequence))
			first := grokAccountingSummary(t, f, profile)
			unit := first.Totals.Accounting
			if first.AccountingProfile != profile || first.Totals.Responses != 0 || first.Totals.Total.KnownTotal != "" || len(unit) != 1 || unit[0].Kind != pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_GROK_CLOSED_INPUT || unit[0].Units != 1 || unit[0].KnownTotal != total || unit[0].MeasuredUnits != 1 || unit[0].ActualCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE || unit[0].EstimatedCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE || first.EstimatedCost != pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE || len(first.Estimates.Currencies) != 0 || first.Estimates.UnpricedResponses != 0 {
				t.Fatalf("overlap, precision or pricing changed: %+v", first)
			}
			if len(first.Groups) != 1 || first.Groups[0].AccountId != string(f.input.AccountID) || first.Groups[0].ModelId != string(f.input.Configuration.ModelID) || first.Groups[0].Totals.Accounting[0].KnownTotal != total || len(first.Analytics.Models) != 1 || first.Analytics.Models[0].Totals.Accounting[0].KnownTotal != total {
				t.Fatal("assignment/group/model attribution lost")
			}
			dayUnits := uint32(0)
			for _, day := range first.Analytics.Days {
				for _, unit := range day.Totals.Accounting {
					dayUnits += unit.Units
					if unit.KnownTotal != total {
						t.Fatal("daily total changed")
					}
				}
			}
			if dayUnits != 1 {
				t.Fatal("daily accounting repeated or lost the unit")
			}
			if replay, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, report)); err != nil || !replay.Msg.Replayed {
				t.Fatal("completion receipt replay lost", err)
			}
			if got := grokAccountingSummary(t, f, profile); got.Totals.Accounting[0].Units != 1 {
				t.Fatal("source replay duplicated unit")
			}
			legacy := grokAccountingSummary(t, f, pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_UNSPECIFIED)
			if len(legacy.Totals.Accounting) != 0 || len(legacy.Groups) != 0 || legacy.Totals.Responses != 0 {
				t.Fatal("negotiated data changed legacy shape")
			}
			raw, _ := protojson.Marshal(first)
			for _, private := range []string{f.input.Input.Prompt, string(f.thread), string(f.turn), terminal.GrokTerminal.HistoryDigest, string(terminal.GrokTerminal.ClosureID), report.Mutation.RequestId} {
				if strings.Contains(string(raw), private) {
					t.Fatal("aggregate exposed private source evidence")
				}
			}
			reader := delidevv1connect.NewUsageServiceClient(f.http.Client(), f.http.URL)
			for _, request := range []*pb.GetUsageSummaryRequest{{AccountingProfile: profile, AccountId: string(domain.NewID())}, {AccountingProfile: profile, ModelId: string(domain.NewID())}, {AccountingProfile: profile, SessionId: string(domain.NewID())}} {
				filtered, err := reader.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, request))
				if err != nil || len(filtered.Msg.Totals.Accounting) != 0 {
					t.Fatal("Grok filter ignored", err)
				}
			}
			bad := &pb.GetUsageSummaryRequest{AccountingProfile: pb.UsageAccountingProfile(99)}
			if _, err := reader.GetUsageSummary(context.Background(), ownerRequest(f.service.Identity, bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatal("unknown profile accepted", err)
			}
		})
	}
}

func TestGrokAccountingExcludesUnverifiedLegacyAndInterruptedInputs(t *testing.T) {
	for _, scenario := range []string{"missing-cleanup", "legacy-history", "changed-history", "missing-closure", "partial-text", "pre-text", "stop-success"} {
		t.Run(scenario, func(t *testing.T) {
			var f *publicationFixture
			var e domain.ExecutionEvent
			if scenario == "partial-text" || scenario == "pre-text" || scenario == "stop-success" {
				kind := domain.GrokInterruptedText
				if scenario == "pre-text" {
					kind = domain.GrokInterruptedBeforeText
				}
				if scenario == "stop-success" {
					kind = domain.GrokCompletedDuringStop
				}
				f, e = grokServerStopFixture(t, kind)
				requestOpenCodeStopFixture(t, f)
			} else {
				f, e = grokServerTerminalFixture(t, scenario != "legacy-history")
			}
			if scenario == "changed-history" {
				e.GrokTerminal.User.InputDigest = domain.GrokUserInputDigest("changed history")
			}
			if scenario == "missing-closure" {
				e.GrokTerminal.HistoryDigest = ""
			}
			if scenario == "changed-history" || scenario == "missing-closure" {
				if _, err := f.call(f.requestEvent(t, e)); err == nil {
					t.Fatal("invalid closure accepted")
				}
			} else {
				f.publish(t, e)
				completion := grokCompletion(f, e.Sequence)
				completion.Outcome = e.Outcome
				if scenario == "missing-cleanup" {
					completion.CleanupVerified = false
				}
				f.reportCompletion(t, completion)
			}
			got := grokAccountingSummary(t, f, pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1)
			if len(got.Totals.Accounting) != 0 {
				t.Fatal("unverified or interrupted input produced aggregate usage")
			}
		})
	}
}

func TestMixedCodexGrokAccountingKeepsUnitKindsAndLegacyCounts(t *testing.T) {
	f, e := grokServerTerminalFixture(t, true)
	f.publish(t, e)
	f.reportCompletion(t, grokCompletion(f, e.Sequence))
	counts := reportedCounts(20)
	record := domain.ResponseUsageRecord{SessionID: f.input.SessionID, ExecutionID: domain.NewID(), AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, ProviderID: f.input.Configuration.ProviderID, ModelID: f.input.Configuration.ModelID, Harness: domain.Codex, Version: domain.CodexProtocolVersion, ThreadID: string(domain.NewID()), TurnID: string(domain.NewID()), Sequence: 1, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("cd", 32), Counts: &counts, CostEvidence: domain.UsageCostMissing}}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.codex-response", nil, func(tx *store.Tx) (any, error) {
		id, _, err := tx.PutResponseUsage(domain.NewID(), record)
		return id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	got := grokAccountingSummary(t, f, pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1)
	if got.Totals.Responses != 1 || got.Totals.Total.KnownTotal != "20" || len(got.Totals.Accounting) != 2 {
		t.Fatalf("mixed sources changed legacy total: %+v", got.Totals)
	}
	byKind := map[pb.AccountingUnitKind]*pb.AccountingTotals{}
	for _, value := range got.Totals.Accounting {
		byKind[value.Kind] = value
	}
	if byKind[pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_CODEX_RESPONSE].KnownTotal != "20" || byKind[pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_GROK_CLOSED_INPUT].KnownTotal != "16" || got.Estimates.UnpricedResponses != 1 {
		t.Fatal("native unit kinds or cost coverage were combined")
	}
}

func TestGrokAccountingRetainsSourceAcrossStoreRestart(t *testing.T) {
	f, terminal := grokServerTerminalFixture(t, true)
	f.publish(t, terminal)
	f.reportCompletion(t, grokCompletion(f, terminal.Sequence))
	f.service.executionAuthority.close()
	f.http.Close()
	root := f.service.Store.Root()
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	selection := domain.UsageSelection{From: time.Now().UTC().Add(-time.Hour), Until: time.Now().UTC().Add(time.Hour), AccountingProfile: domain.NativeUnitsV1Accounting}
	if err := reopened.Read(context.Background(), func(tx *store.Tx) error {
		got, err := tx.UsageSummary(selection)
		if err == nil && (len(got.Totals.Accounting) != 1 || got.Totals.Accounting[0].Units != 1 || got.Totals.Accounting[0].KnownTotal != "16") {
			t.Fatal("restart lost or duplicated original source")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGrokAccountingRollsBackWithOriginalCompletionTransaction(t *testing.T) {
	f, terminal := grokServerTerminalFixture(t, true)
	f.publish(t, terminal)
	completion := grokCompletion(f, terminal.Sequence)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.completion-rollback", nil, func(tx *store.Tx) (any, error) {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		progress := *session.Execution
		progress.CleanupVerified = true
		if err := tx.PutGrokAccounting(f.job, "", f.input, progress, completion); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Conflict, "fixture rollback", "retain original terminal")
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal(err)
	}
	if got := grokAccountingSummary(t, f, pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1); len(got.Totals.Accounting) != 0 {
		t.Fatal("failed completion partially retained accounting")
	}
	f.reportCompletion(t, completion)
	if got := grokAccountingSummary(t, f, pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1); len(got.Totals.Accounting) != 1 || got.Totals.Accounting[0].Units != 1 {
		t.Fatal("rollback prevented exact original completion")
	}
}
