// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestOpenCodeNativeAccountingPublicationPreservesAPISourceAndGoService(t *testing.T) {
	for _, goService := range []bool{false, true} {
		t.Run(map[bool]string{false: "api", true: "go"}[goService], func(t *testing.T) {
			f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixtureForHarness(t, domain.OpenCode, domain.ExecuteMode), thread: "ses_01960dcbe1faabcdefghijklmn"}
			if goService {
				configureOpenCodeGoDispatch(t, f.firstDispatchFixture)
			}
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			f.turn = "msg_01960dcbe1faABCDEFGHIJKLMN"
			pf := &publicationFixture{authorityFixture: &authorityFixture{service: f.service, client: f.workerClient, workerToken: f.workerIdentity.Token, job: domain.ID(f.job.Id), device: f.workerDevice, instance: domain.ID(f.workerInstance), input: f.input}, revision: f.job.Revision, thread: f.thread, turn: f.turn}
			pf.publish(t, pf.event(domain.ExecutionThreadBound, 1))
			pf.publish(t, pf.event(domain.ExecutionInputAccepted, 2))
			one, two := "1", "2"
			var price store.PricingVersion
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.opencode-accounting-price", nil, func(tx *store.Tx) (any, error) {
				var err error
				price, err = tx.PutPricing(f.input.Configuration.ModelID, 0, domain.NewID(), domain.TokenPricing{Currency: "USD", Source: "Fixture", AsOf: "2026-09-29", InputMode: domain.UniformInputPrice, InputPerMillion: &one, OutputPerMillion: &two})
				return nil, err
			}); err != nil {
				t.Fatal(err)
			}
			value := originalOpenCodeUsage()
			e := pf.event(domain.ExecutionOpenCodeUsageObserved, 3)
			e.ObservationID, e.OpenCodeUsage = domain.NewID(), &value
			source := e.ObservationID
			receipt := pf.publish(t, e)
			if response, err := pf.call(receipt); err != nil || !response.Msg.Replayed {
				t.Fatal("original step receipt did not replay", err)
			}
			e.Sequence, e.ObservationID = 4, domain.NewID()
			value.Counts.Input = "13"
			if _, err := pf.call(pf.requestEvent(t, e)); err == nil {
				t.Fatal("conflicting native step accepted")
			}
			row, err := f.service.Store.Get(context.Background(), domain.UsageKind, source)
			if err != nil {
				t.Fatal(err)
			}
			o, err := store.Decode[domain.OpenCodeUsageRecord](row)
			if err != nil || o.ProviderID != f.input.Configuration.ProviderID || o.SubscriptionService != f.input.Configuration.SubscriptionService || o.AccountID != f.input.AccountID || o.ModelID != f.input.Configuration.ModelID || o.Usage.Counts.Input != "12" {
				t.Fatal("publication lost immutable source attribution", o, err)
			}
			c := delidevv1connect.NewUsageServiceClient(http.DefaultClient, f.endpoint.URL)
			query := &pb.GetUsageSummaryRequest{AccountingProfile: pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1, SessionId: string(f.input.SessionID)}
			service := pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_UNSPECIFIED
			if goService {
				service = pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_OPENCODE_GO
				query.SubscriptionService = service
			}
			response, err := c.GetUsageSummary(context.Background(), ownerRequest(f.identity, query))
			if err != nil || len(response.Msg.NativeAccounting) != 2 {
				t.Fatal("native accounting RPC failed", err)
			}
			a := response.Msg.NativeAccounting[1]
			if a.Totals.Units != 1 || a.Totals.Input.KnownTotal != "22" || a.Totals.Output.KnownTotal != "10" || len(a.Groups) != 1 || len(a.Models) != 1 || len(a.Pricing) != 1 || a.Pricing[0].Pricing.Id != string(price.ID) || a.Pricing[0].Pricing.SubscriptionService != service || response.Msg.Totals.Responses != 0 {
				t.Fatal("wire accounting changed counts or historical source", a)
			}
			g, m := a.Groups[0], a.Models[0]
			if g.ProviderId != string(o.ProviderID) || g.SubscriptionService != service || g.AccountId != string(o.AccountID) || g.Model.GetSubscriptionService() != service || m.ProviderId != string(o.ProviderID) || m.SubscriptionService != service || m.Model.GetSubscriptionService() != service {
				t.Fatal("wire groups lost original service identity", g, m)
			}
			raw, _ := protojson.Marshal(response.Msg)
			for _, private := range []string{f.input.Input.Prompt, f.workerIdentity.Token, o.ThreadID, o.TurnID, o.Usage.NativeID, "native_estimate"} {
				if strings.Contains(string(raw), private) {
					t.Fatal("aggregate exposed private native evidence", private)
				}
			}
			query.SubscriptionService = pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE
			empty, err := c.GetUsageSummary(context.Background(), ownerRequest(f.identity, query))
			if err != nil || empty.Msg.NativeAccounting[1].Totals.Units != 0 || len(empty.Msg.NativeAccounting[1].Pricing) != 0 {
				t.Fatal("foreign service filter retained accounting", err)
			}
		})
	}
}
