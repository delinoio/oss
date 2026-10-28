// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	sessionauth "github.com/delinoio/oss/servers/tairouter/internal/auth"
	serverconfig "github.com/delinoio/oss/servers/tairouter/internal/config"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/pricing"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	rpc "github.com/delinoio/oss/protos/tairouter/v1/tairouterv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func usageOperatorFixture(t *testing.T) *acceptanceFixture {
	t.Helper()
	dsn := os.Getenv("TAIROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TAIROUTER_TEST_DATABASE_URL for real PostgreSQL integration")
	}
	keys, err := core.NewEnvelope("test", map[string][]byte{"test": make([]byte, 32)})
	acceptanceOK(t, err)
	schema := "usage_" + strings.ReplaceAll(core.ID(), "-", "")
	identity := core.Identity{Issuer: "https://identity.test", Subject: "operator"}
	s, err := store.Open(context.Background(), dsn, store.Options{Schema: schema, Keys: keys, Operators: []core.Identity{identity}})
	acceptanceOK(t, err)
	acceptanceOK(t, s.Migrate(context.Background()))
	acceptanceOK(t, s.RegisterAdmissionInstance(context.Background()))
	t.Cleanup(func() {
		s.Close()
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			defer db.Close()
			db.Exec("DROP SCHEMA IF EXISTS " + schema + "_temp CASCADE")
			db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		}
	})
	config := serverconfig.Config{PublicURL: "https://router.test", Operators: []serverconfig.OperatorIdentity{{Issuer: identity.Issuer, Subject: identity.Subject}}}
	srv := &server{config: config, store: s, auth: sessionauth.New(config, persistentAuth{s: s}), telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	h := httptest.NewServer(srv.handler())
	t.Cleanup(h.Close)
	return &acceptanceFixture{t: t, ctx: context.Background(), s: s, url: h.URL}
}

func TestUsageOperatorAtomicSettingsAndPrices(t *testing.T) {
	f := usageOperatorFixture(t)
	_, client := f.user("operator")
	op := rpc.NewOperatorServiceClient(client, f.url)
	initial, err := op.GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, err)
	revision := initial.Msg.Settings.Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := op.UpdateInstanceSettings(f.ctx, connect.NewRequest(&v1.UpdateInstanceSettingsRequest{Settings: initial.Msg.Settings, ExpectedRevision: revision}))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if connect.CodeOf(err) == connect.CodeAborted {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS results: %d successful, %d conflicts", successes, conflicts)
	}
	list, err := op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{}))
	acceptanceOK(t, err)
	if list.Msg.Revision == revision || list.Msg.Revision == "" {
		t.Fatal("collection revision did not advance")
	}
	_, err = op.SetPriceOverride(f.ctx, connect.NewRequest(&v1.SetPriceOverrideRequest{ProviderId: "openai", ModelId: "fixture", Rates: &v1.PriceRates{Currency: "USD"}}))
	acceptanceCode(t, err, connect.CodeInvalidArgument)
	set, err := op.SetPriceOverride(f.ctx, connect.NewRequest(&v1.SetPriceOverrideRequest{ProviderId: "openai", ModelId: "fixture", Rates: &v1.PriceRates{Currency: "USD", Input: ptr("0"), Output: ptr("2")}, ExpectedRevision: list.Msg.Revision}))
	acceptanceOK(t, err)
	_, err = op.DeletePriceOverride(f.ctx, connect.NewRequest(&v1.DeletePriceOverrideRequest{OverrideId: set.Msg.Override.Id, ExpectedRevision: list.Msg.Revision}))
	acceptanceCode(t, err, connect.CodeAborted)
	listed, err := op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{}))
	acceptanceOK(t, err)
	if len(listed.Msg.Overrides) != 1 || listed.Msg.Overrides[0].Rates.CacheRead != nil || listed.Msg.Overrides[0].Rates.GetInput() != "0" {
		t.Fatal("price presence or stale delete changed collection")
	}
	snapshot, err := currentPricingSnapshot(f.ctx, f.s, nil)
	acceptanceOK(t, err)
	estimate := snapshot.Estimate(pricing.Request{Provider: "openai", Model: "fixture", Status: pricing.Reported, Usage: &pricing.Usage{InputTokens: ptr(int64(10)), OutputTokens: ptr(int64(100))}}, time.Now())
	if !estimate.Known || estimate.Cost.Total != 0.0002 {
		t.Fatalf("override estimate: %+v", estimate)
	}
	deleted, err := op.DeletePriceOverride(f.ctx, connect.NewRequest(&v1.DeletePriceOverrideRequest{OverrideId: set.Msg.Override.Id, ExpectedRevision: listed.Msg.Revision}))
	acceptanceOK(t, err)
	if deleted.Msg.Revision == listed.Msg.Revision {
		t.Fatal("delete did not advance revision")
	}
	fresh, err := currentPricingSnapshot(f.ctx, f.s, snapshot)
	acceptanceOK(t, err)
	if _, ok := fresh.Resolve("openai", "fixture", pricing.Resolution{}); ok {
		t.Fatal("deleted override retained")
	}
	if _, ok := snapshot.Resolve("openai", "fixture", pricing.Resolution{}); !ok {
		t.Fatal("old admitted snapshot mutated")
	}
	_, ordinary := f.user("ordinary")
	_, err = rpc.NewOperatorServiceClient(ordinary, f.url).ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{}))
	acceptanceCode(t, err, connect.CodePermissionDenied)
}

func TestUsageOperatorUnknownMappings(t *testing.T) {
	unknown := store.Usage{Outcome: store.OutcomeUnknown}
	if storedUsageOutcome(unknown) != v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED || appliedPrice(unknown) != nil {
		t.Fatal("unknown record became known")
	}
	e := pricing.Estimate{Price: pricing.Price{Model: "fixture", Provider: "openai", Source: pricing.Operator}, EffectiveOptionalRates: &pricing.OptionalRates{Currency: "USD", Input: ptr(float64(0)), Output: ptr(float64(2))}, RecordedAt: time.Now()}
	raw, err := json.Marshal(e)
	acceptanceOK(t, err)
	mapped := appliedPrice(store.Usage{Price: raw})
	if mapped == nil || mapped.Rates.Input == nil || *mapped.Rates.Input != "0" || mapped.Rates.CacheRead != nil || mapped.Source != v1.PriceSource_PRICE_SOURCE_OPERATOR_OVERRIDE {
		t.Fatal("optional price mapping lost presence")
	}
	if unsigned(ptr(int64(-1))) != nil {
		t.Fatal("negative token count wrapped")
	}
	if auditOutcome(store.AuditUnknown) != v1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED || jobHealth(store.JobHealth{}) != v1.HealthState_HEALTH_STATE_UNSPECIFIED {
		t.Fatal("unknown health/audit became successful")
	}
	for _, raw := range []string{"", "0", "-1", "01", "1.0"} {
		if _, err := settingsRevision(raw); err == nil {
			t.Fatalf("invalid revision %q accepted", raw)
		}
	}
	start := time.Now()
	_, err = usageFilter(&v1.TimeRange{Start: timestamppb.New(start), End: timestamppb.New(start)}, "", "", nil)
	acceptanceCode(t, err, connect.CodeInvalidArgument)
	f := store.UsageFilter{}
	acceptanceOK(t, extendUsageFilter(&f, ptr("openai"), ptr(v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED)))
	if f.Provider != "openai" || f.Outcome != store.OutcomeUnknown {
		t.Fatal("explicit unknown filter lost")
	}
}

func TestUsageOperatorFilteredPagesAndExactTime(t *testing.T) {
	f := usageOperatorFixture(t)
	owner, client := f.user("owner")
	member, memberClient := f.user("member")
	org := f.org(client, "usage")
	f.join(client, memberClient, org, member.Email)
	sc, _, err := f.s.Membership(f.ctx, org, owner.ID)
	acceptanceOK(t, err)
	_, key, err := f.s.IssueKey(f.ctx, sc, "usage", nil)
	acceptanceOK(t, err)
	start := time.Now().Add(-time.Second)
	record := func(model, provider string, outcome store.RequestOutcome, cost *string) {
		t.Helper()
		lease, err := f.s.Admit(f.ctx, key)
		acceptanceOK(t, err)
		acceptanceOK(t, f.s.RecordUsage(f.ctx, lease, store.Usage{Model: model, Provider: provider, Outcome: outcome, InputTokens: ptr(int64(12)), OutputTokens: ptr(int64(4)), Cost: cost}))
		acceptanceOK(t, f.s.Release(f.ctx, lease))
	}
	record("model-a", "openai", store.OutcomeSucceeded, ptr("0.125"))
	record("model-a", "openai", store.OutcomeSucceeded, ptr("0.125"))
	record("model-a", "other", store.OutcomeCanceled, nil)
	record("model-b", "openai", store.OutcomeUnknown, nil)
	usage := rpc.NewUsageServiceClient(client, f.url)
	query := &v1.ListUsageRequest{OrganizationId: org, ProviderId: ptr("openai"), ModelId: ptr("model-a"), Outcome: ptr(v1.RequestOutcome_REQUEST_OUTCOME_SUCCEEDED), Page: &v1.PageRequest{PageSize: 1}, TimeRange: &v1.TimeRange{Start: timestamppb.New(start), End: timestamppb.New(time.Now().Add(time.Second))}}
	first, err := usage.ListUsage(f.ctx, connect.NewRequest(query))
	acceptanceOK(t, err)
	if len(first.Msg.Records) != 1 || first.Msg.Page.NextPageToken == "" {
		t.Fatal("filtered first page missing")
	}
	query.Page.PageToken = first.Msg.Page.NextPageToken
	second, err := usage.ListUsage(f.ctx, connect.NewRequest(query))
	acceptanceOK(t, err)
	if len(second.Msg.Records) != 1 || second.Msg.Records[0].Id == first.Msg.Records[0].Id || second.Msg.Page.NextPageToken != "" {
		t.Fatal("filtered pagination repeated or omitted rows")
	}
	// A page cursor cannot be replayed under another filter.
	changedQuery := &v1.ListUsageRequest{OrganizationId: org, ProviderId: ptr("other"), Page: &v1.PageRequest{PageSize: 1, PageToken: first.Msg.Page.NextPageToken}}
	if _, err = usage.ListUsage(f.ctx, connect.NewRequest(changedQuery)); err == nil {
		t.Fatal("cursor accepted for changed filters")
	}
	summary, err := usage.GetUsageSummary(f.ctx, connect.NewRequest(&v1.GetUsageSummaryRequest{OrganizationId: org, ProviderId: query.ProviderId, ModelId: query.ModelId, Outcome: query.Outcome, TimeRange: query.TimeRange}))
	acceptanceOK(t, err)
	if summary.Msg.Summary.RequestCount != 2 || summary.Msg.Summary.KnownUsage.GetInputTokens() != 24 {
		t.Fatalf("filtered summary: %+v", summary.Msg.Summary)
	}
	daily, err := usage.ListDailyUsage(f.ctx, connect.NewRequest(&v1.ListDailyUsageRequest{OrganizationId: org, ProviderId: query.ProviderId, ModelId: query.ModelId, Outcome: query.Outcome, TimeRange: query.TimeRange, Page: &v1.PageRequest{PageSize: 1}}))
	acceptanceOK(t, err)
	if len(daily.Msg.Days) != 1 || daily.Msg.Days[0].Summary.RequestCount != 2 {
		t.Fatal("daily filter or partial-day range ignored")
	}
	// A boundary copied from a stored timestamp must include that row at start and exclude it at end.
	boundary := first.Msg.Records[0].OccurredAt
	exact, err := usage.ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, TimeRange: &v1.TimeRange{Start: boundary}}))
	acceptanceOK(t, err)
	found := false
	for _, row := range exact.Msg.Records {
		found = found || row.Id == first.Msg.Records[0].Id
	}
	if !found {
		t.Fatal("start boundary excluded")
	}
	exact, err = usage.ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, TimeRange: &v1.TimeRange{End: boundary}}))
	acceptanceOK(t, err)
	for _, row := range exact.Msg.Records {
		if row.Id == first.Msg.Records[0].Id {
			t.Fatal("end boundary included")
		}
	}
	_, err = rpc.NewUsageServiceClient(memberClient, f.url).ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: &owner.ID}))
	acceptanceCode(t, err, connect.CodePermissionDenied)
	unknown, err := usage.ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, Outcome: ptr(v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED)}))
	acceptanceOK(t, err)
	if len(unknown.Msg.Records) != 1 || unknown.Msg.Records[0].ModelId != "model-b" {
		t.Fatal("explicit unknown outcome filter incorrect")
	}
}

func TestUsageOperatorAuditAndHealth(t *testing.T) {
	f := usageOperatorFixture(t)
	owner, client := f.user("operator")
	op := rpc.NewOperatorServiceClient(client, f.url)
	health, err := op.GetInstanceHealth(f.ctx, connect.NewRequest(&v1.GetInstanceHealthRequest{}))
	acceptanceOK(t, err)
	if health.Msg.Health.UsageRecording != v1.HealthState_HEALTH_STATE_UNSPECIFIED {
		t.Fatal("fresh recording health must be unknown")
	}
	org := f.org(client, "audit")
	sc, _, err := f.s.Membership(f.ctx, org, owner.ID)
	acceptanceOK(t, err)
	_, err = f.s.UpdateOrganization(f.ctx, sc, "changed")
	acceptanceOK(t, err)
	audits := rpc.NewAuditServiceClient(client, f.url)
	result, err := audits.ListOrganizationAudit(f.ctx, connect.NewRequest(&v1.ListOrganizationAuditRequest{OrganizationId: org, Action: ptr(v1.AuditAction_AUDIT_ACTION_ORGANIZATION_UPDATED), ActorUserId: &owner.ID, Outcome: ptr(v1.AuditOutcome_AUDIT_OUTCOME_SUCCEEDED), Page: &v1.PageRequest{PageSize: 1}}))
	acceptanceOK(t, err)
	if len(result.Msg.Records) != 1 || result.Msg.Records[0].Outcome != v1.AuditOutcome_AUDIT_OUTCOME_SUCCEEDED {
		t.Fatal("audit filters/outcome failed")
	}
	instance, err := op.ListInstanceAudit(f.ctx, connect.NewRequest(&v1.ListInstanceAuditRequest{ActorUserId: &owner.ID}))
	acceptanceOK(t, err)
	if len(instance.Msg.Records) != 0 {
		t.Fatal("instance audits exposed organization events")
	}
	acceptanceOK(t, f.s.RecordUsageFailure(f.ctx))
	health, err = op.GetInstanceHealth(f.ctx, connect.NewRequest(&v1.GetInstanceHealthRequest{}))
	acceptanceOK(t, err)
	if health.Msg.Health.UsageRecording != v1.HealthState_HEALTH_STATE_DEGRADED {
		t.Fatal("recording failure hidden")
	}
}

func TestUsageOperatorPricePaginationBindsRevision(t *testing.T) {
	f := usageOperatorFixture(t)
	_, client := f.user("operator")
	op := rpc.NewOperatorServiceClient(client, f.url)
	list, err := op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{}))
	acceptanceOK(t, err)
	revision := list.Msg.Revision
	for _, model := range []string{"one", "two", "three"} {
		set, err := op.SetPriceOverride(f.ctx, connect.NewRequest(&v1.SetPriceOverrideRequest{ProviderId: "openai", ModelId: model, Rates: &v1.PriceRates{Currency: "USD", Input: ptr("1")}, ExpectedRevision: revision}))
		acceptanceOK(t, err)
		revision = set.Msg.Revision
	}
	first, err := op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{Page: &v1.PageRequest{PageSize: 1}}))
	acceptanceOK(t, err)
	second, err := op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{Page: &v1.PageRequest{PageSize: 1, PageToken: first.Msg.Page.NextPageToken}}))
	acceptanceOK(t, err)
	if len(first.Msg.Overrides) != 1 || len(second.Msg.Overrides) != 1 || first.Msg.Overrides[0].Id == second.Msg.Overrides[0].Id {
		t.Fatal("price pagination repeats records")
	}
	_, err = op.DeletePriceOverride(f.ctx, connect.NewRequest(&v1.DeletePriceOverrideRequest{OverrideId: first.Msg.Overrides[0].Id, ExpectedRevision: revision}))
	acceptanceOK(t, err)
	_, err = op.ListPriceOverrides(f.ctx, connect.NewRequest(&v1.ListPriceOverridesRequest{Page: &v1.PageRequest{PageSize: 1, PageToken: second.Msg.Page.NextPageToken}}))
	acceptanceCode(t, err, connect.CodeInvalidArgument)
}

func TestUsageOperatorPriceValidation(t *testing.T) {
	for _, rate := range []string{"NaN", "Infinity", "-0.1", "1e3", " 1", "1000001", "01.0"} {
		err := validatePriceOverride(&v1.PriceOverride{ProviderId: "openai", ModelId: "test", Rates: &v1.PriceRates{Currency: "USD", Input: &rate}})
		acceptanceCode(t, err, connect.CodeInvalidArgument)
	}
	for _, rate := range []string{"0", "0.000000001", "1000000"} {
		acceptanceOK(t, validatePriceOverride(&v1.PriceOverride{ProviderId: "openai", ModelId: "test", Rates: &v1.PriceRates{Currency: "USD", Input: &rate}}))
	}
	if summary(store.Aggregate{Requests: 2, MissingCost: 2, Cost: "0"}).KnownCosts != nil {
		t.Fatal("unknown cost displayed as zero")
	}
}
