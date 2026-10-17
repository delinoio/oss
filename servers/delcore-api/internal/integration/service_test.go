//go:build integration

// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/protos/gen/go/delcore/v1/delcorev1connect"
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/auth"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/delinoio/oss/servers/delcore-api/internal/rpc"
	"github.com/delinoio/oss/servers/delcore-api/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type verifier struct{}

func (verifier) Verify(_ context.Context, h string) (auth.Identity, error) {
	switch h {
	case "Bearer owner":
		return auth.Identity{SubjectHash: "owner", Email: "owner@example.test", Operator: true}, nil
	case "Bearer member":
		return auth.Identity{SubjectHash: "member", Email: "member@example.test"}, nil
	case "Bearer stranger":
		return auth.Identity{SubjectHash: "stranger", Email: "stranger@example.test"}, nil
	case "Bearer service":
		return auth.Identity{ClientID: "service", Scopes: []string{"authorize", "usage:write", "usage:read"}}, nil
	default:
		return auth.Identity{}, errors.New("invalid token")
	}
}
func req[T any](m *T, token string) *connect.Request[T] {
	r := connect.NewRequest(m)
	r.Header().Set("Authorization", "Bearer "+token)
	return r
}
func key() string { return uuid.Must(uuid.NewV7()).String() }
func setup(t *testing.T) (*postgres.Store, delcorev1connect.DelcoreServiceClient, config.Config) {
	t.Helper()
	dsn := os.Getenv("DELCORE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("DELCORE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	root, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + strings.ReplaceAll(key(), "-", "")
	if _, e = root.Pool.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := postgres.Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Pool.Close()
		root.Pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		root.Pool.Close()
	})
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(ctx); e != nil {
		t.Fatal("migration replay", e)
	}
	if e = db.CheckSchema(ctx); e != nil {
		t.Fatal(e)
	}
	c := config.Config{Issuer: "https://issuer.example.test", Currency: "USD", MinorUnits: 2, TimeoutSeconds: 10, MaxPayloadBytes: 1 << 20, InvitationSeconds: 3600, UsageLatenessSeconds: 86400, ReportingGraceSeconds: 86400, ReturnURLs: []string{"https://consumer.example.test/return"}, RetryLimit: 3, RetrySeconds: 1}
	server := &rpc.Server{DB: db, Auth: verifier{}, Config: c, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	path, handler := server.Handler()
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return db, delcorev1connect.NewDelcoreServiceClient(httpServer.Client(), httpServer.URL), c
}
func TestSampleClientAndAccounting(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	service, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "register test service", Service: &pb.Service{Name: "test", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"authorize", "usage:write", "usage:read"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	plan, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "bootstrap free plan", Plan: &pb.Plan{Name: "free", Free: true, DefaultFree: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.PutMeter(ctx, req(&pb.PutMeterRequest{RequestKey: key(), Reason: "register usage meter", Meter: &pb.Meter{ServiceId: service.Msg.Service.Id, Name: "requests"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "register usage price", Price: &pb.Price{PlanId: plan.Msg.Plan.Id, ServiceId: service.Msg.Service.Id, Meter: "requests", UnitPrice: "0.125", Included: "2", Currency: "USD"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	me, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	again, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil || len(again.Msg.Tenants) != 1 || me.Msg.Tenants[0].Id != again.Msg.Tenants[0].Id {
		t.Fatal("personal tenant is not idempotent", e)
	}
	orgReq := &pb.CreateTenantRequest{RequestKey: key(), Name: "Sample organization"}
	org, e := client.CreateTenant(ctx, req(orgReq, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := client.CreateTenant(ctx, req(orgReq, "owner"))
	if e != nil || replay.Msg.Tenant.Id != org.Msg.Tenant.Id {
		t.Fatal("mutation replay", e)
	}
	orgReq.Name = "different"
	if _, e = client.CreateTenant(ctx, req(orgReq, "owner")); connect.CodeOf(e) != connect.CodeAlreadyExists {
		t.Fatal("conflicting mutation accepted", e)
	}
	tenant := org.Msg.Tenant.Id
	if _, e = client.GetTenant(ctx, req(&pb.GetTenantRequest{TenantId: tenant}, "stranger")); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("cross-tenant read", e)
	}
	invitation, e := client.CreateInvitation(ctx, req(&pb.CreateInvitationRequest{RequestKey: key(), TenantId: tenant, Email: "member@example.test", Roles: []pb.Role{pb.Role_ROLE_MEMBER}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if invitation.Msg.InvitationId == "" {
		t.Fatal("missing invitation")
	}
	var tokenURL string
	e = db.Pool.QueryRow(ctx, "SELECT payload->>'text' FROM jobs WHERE kind='email'").Scan(&tokenURL)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(strings.TrimPrefix(tokenURL, "Accept your organization invitation: "))
	token := u.Query().Get("invitation_token")
	if _, e = client.AcceptInvitation(ctx, req(&pb.AcceptInvitationRequest{RequestKey: key(), Token: token}, "stranger")); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("wrong recipient accepted", e)
	}
	if _, e = client.AcceptInvitation(ctx, req(&pb.AcceptInvitationRequest{RequestKey: key(), Token: token}, "member")); e != nil {
		t.Fatal(e)
	}
	if _, e = client.AcceptInvitation(ctx, req(&pb.AcceptInvitationRequest{RequestKey: key(), Token: token}, "member")); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("invitation replay accepted", e)
	}
	member, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "member"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.SetGroup(ctx, req(&pb.SetGroupRequest{RequestKey: key(), TenantId: tenant, Group: &pb.Group{Name: "users", UserIds: []string{member.Msg.UserId}, Permissions: []*pb.Permission{{ServiceId: service.Msg.Service.Id, Capability: "execute"}}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	decision, e := client.Authorize(ctx, req(&pb.AuthorizeRequest{TenantId: tenant, UserId: member.Msg.UserId, Capability: "execute", Paid: true}, "service"))
	if e != nil || !decision.Msg.Allowed {
		t.Fatal("included usage denied", decision, e)
	}
	usage := &pb.Usage{TenantId: tenant, ServiceId: service.Msg.Service.Id, EventKey: "event-1", Meter: "requests", Quantity: "4", OccurredAt: timestamppb.New(time.Now().UTC())}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := client.ReportUsage(ctx, req(&pb.ReportUsageRequest{RequestKey: key(), Usage: usage}, "service"))
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	var balance string
	if e = db.Pool.QueryRow(ctx, "SELECT count(*),sum(amount)::text FROM ledger WHERE tenant_id=$1", tenant).Scan(&count, &balance); e != nil || count != 1 || balance != "-0.25" {
		t.Fatal(count, balance, e)
	}
	decision, e = client.Authorize(ctx, req(&pb.AuthorizeRequest{TenantId: tenant, UserId: member.Msg.UserId, Capability: "execute", Paid: true}, "service"))
	if e != nil || decision.Msg.Allowed {
		t.Fatal("negative balance allowed", e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE ledger SET amount=0 WHERE tenant_id=$1", tenant); e == nil {
		t.Fatal("ledger update accepted")
	}
	topupRequest := &pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}
	topup, e := client.RequestTopUp(ctx, req(topupRequest, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	transaction := provider.Transaction{ID: "txn_test", Status: "completed", Currency: "USD"}
	transaction.Custom.JobID = topup.Msg.JobId
	transaction.Custom.TenantID = tenant
	transaction.Details.Totals.Subtotal = "1000"
	if e = w.ApplyTransaction(ctx, transaction, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = w.ApplyTransaction(ctx, transaction, time.Now()); e != nil {
		t.Fatal(e)
	}
	var credits int
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM ledger WHERE source_key='payment:txn_test'").Scan(&credits); e != nil || credits != 1 {
		t.Fatal("payment replay", credits, e)
	}
	// A new catalog version must not reprice the accepted event or its correction.
	newPrice, e := client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "new price version", Price: &pb.Price{PlanId: plan.Msg.Plan.Id, ServiceId: service.Msg.Service.Id, Meter: "requests", UnitPrice: "99", Included: "2", Currency: "USD"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: plan.Msg.Plan.Id, PriceIds: []string{newPrice.Msg.Price.Id}, ReturnUrl: cfg.ReturnURLs[0]}, "owner")); e != nil {
		t.Fatal("explicit free price refresh", e)
	}
	var originalID string
	if e = db.Pool.QueryRow(ctx, "SELECT id::text FROM usage_events WHERE event_key='event-1'").Scan(&originalID); e != nil {
		t.Fatal(e)
	}
	correction := &pb.Usage{TenantId: tenant, ServiceId: service.Msg.Service.Id, EventKey: "correction-1", Meter: "requests", Quantity: "1", OccurredAt: usage.OccurredAt, CorrectionOf: originalID}
	if _, e = client.CorrectUsage(ctx, req(&pb.CorrectUsageRequest{RequestKey: key(), Usage: correction}, "service")); e != nil {
		t.Fatal(e)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT sum(amount)::text FROM ledger WHERE tenant_id=$1 AND kind IN ('credit','usage','refund','correction')", tenant).Scan(&balance); e != nil || balance != "10.000" && balance != "10.00" && balance != "10" {
		t.Fatal("correction repriced historical usage", balance, e)
	}
	if _, e = client.ReportUsage(ctx, req(&pb.ReportUsageRequest{RequestKey: key(), Usage: usage}, "owner")); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("user impersonated service", e)
	}
	_, e = client.SetMemberRoles(ctx, req(&pb.SetMemberRolesRequest{RequestKey: key(), TenantId: tenant, UserId: member.Msg.UserId, Roles: []pb.Role{pb.Role_ROLE_ADMIN}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.SetMemberRoles(ctx, req(&pb.SetMemberRolesRequest{RequestKey: key(), TenantId: tenant, UserId: member.Msg.UserId, Roles: []pb.Role{pb.Role_ROLE_BILLING}}, "member"))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("admin elevated role", e)
	}
	transfer, e := client.TransferOwnership(ctx, req(&pb.TransferOwnershipRequest{RequestKey: key(), TenantId: tenant, RecipientId: member.Msg.UserId}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.AcceptOwnership(ctx, req(&pb.AcceptOwnershipRequest{RequestKey: key(), TenantId: tenant, TransferId: transfer.Msg.TransferId}, "member")); e != nil {
		t.Fatal(e)
	}
	if _, e = client.RequestTopUp(ctx, req(topupRequest, "owner")); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("cached mutation bypassed revoked billing role", e)
	}
	if _, e = client.RemoveMember(ctx, req(&pb.RemoveMemberRequest{RequestKey: key(), TenantId: tenant, UserId: member.Msg.UserId}, "member")); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("last owner left", e)
	}
}

func TestPaidActivationAndRenewal(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "create service", Service: &pb.Service{Name: "paid-service", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"authorize", "usage:write"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "default free plan", Plan: &pb.Plan{Name: "free", Free: true, DefaultFree: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	paid, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "paid plan", Plan: &pb.Plan{Name: "paid", Rank: 10, Postpaid: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	price, e := client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "base subscription", Price: &pb.Price{PlanId: paid.Msg.Plan.Id, ServiceId: svc.Msg.Service.Id, Meter: "base", UnitPrice: "10", Included: "0", Currency: "USD", ExternalPriceId: "pri_base", Kind: pb.PriceKind_PRICE_KIND_RECURRING}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	me, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	tenant := me.Msg.Tenants[0].Id
	change, e := client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: paid.Msg.Plan.Id, PriceIds: []string{price.Msg.Price.Id}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	var before string
	if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if before == paid.Msg.Plan.Id {
		t.Fatal("checkout intent activated paid plan")
	}
	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"id":"txn_initial","status":"completed","currency_code":"USD","customer_id":"ctm_fixture","subscription_id":"sub_fixture","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q},"items":[{"price":{"id":"pri_base"},"quantity":1}],"billing_period":{"starts_at":%q,"ends_at":%q}}`, change.Msg.JobId, tenant, now.Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano))
	var transaction provider.Transaction
	if e = json.Unmarshal([]byte(payload), &transaction); e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e = w.ApplyTransaction(ctx, transaction, now); e != nil {
		t.Fatal(e)
	}
	var active string
	if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&active); e != nil || active != paid.Msg.Plan.Id {
		t.Fatal("paid activation", active, e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE tenants SET renewal_failed=true WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	change, e = client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: paid.Msg.Plan.Id, PriceIds: []string{price.Msg.Price.Id}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	transaction.ID = "txn_change"
	transaction.Custom.JobID = change.Msg.JobId
	if e = w.ApplyTransaction(ctx, transaction, now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	var stillBlocked bool
	if e = db.Pool.QueryRow(ctx, "SELECT renewal_failed FROM tenants WHERE id=$1", tenant).Scan(&stillBlocked); e != nil || !stillBlocked {
		t.Fatal("unrelated payment cleared renewal failure", e)
	}
	transaction.ID = "txn_renewal"
	transaction.Origin = "subscription_recurring"
	transaction.BillingPeriod.StartsAt = transaction.BillingPeriod.EndsAt
	transaction.BillingPeriod.EndsAt = transaction.BillingPeriod.StartsAt.AddDate(0, 1, 0)
	if e = w.ApplyTransaction(ctx, transaction, transaction.BillingPeriod.StartsAt); e != nil {
		t.Fatal(e)
	}
	var blocked bool
	if e = db.Pool.QueryRow(ctx, "SELECT renewal_failed FROM tenants WHERE id=$1", tenant).Scan(&blocked); e != nil || blocked {
		t.Fatal("renewal did not recover", e)
	}
	cancel, e := client.CancelSubscription(ctx, req(&pb.CancelSubscriptionRequest{RequestKey: key(), TenantId: tenant}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	event := provider.Event{ID: "evt_cancel", Type: "subscription.canceled", OccurredAt: transaction.BillingPeriod.EndsAt, Data: json.RawMessage(`{"id":"sub_fixture","status":"canceled"}`)}
	raw, e := json.Marshal(event)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Pool.Exec(ctx, "INSERT INTO webhooks(event_id,payload,received_at,occurred_at) VALUES($1,$2,now(),$3)", event.ID, raw, event.OccurredAt); e != nil {
		t.Fatal(e)
	}
	if e = w.ProcessWebhook(ctx, event.ID); e != nil {
		t.Fatal(e)
	}
	var free bool
	var state string
	if e = db.Pool.QueryRow(ctx, "SELECT p.free,j.state FROM tenants t JOIN plans p ON p.id=t.plan_id JOIN jobs j ON j.id=$2 WHERE t.id=$1", tenant, cancel.Msg.JobId).Scan(&free, &state); e != nil || !free || state != "done" {
		t.Fatal("renewal cancellation", free, state, e)
	}
}

func bootstrap(t *testing.T, client delcorev1connect.DelcoreServiceClient) (string, string) {
	t.Helper()
	ctx := context.Background()
	_, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "bootstrap default", Plan: &pb.Plan{Name: "free", Free: true, DefaultFree: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	me, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	return me.Msg.UserId, me.Msg.Tenants[0].Id
}
func TestMaintenanceAndDeletionHolds(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	now := time.Now().UTC()
	cfg.DeletionGraceSeconds = 10
	cfg.WebhookRetentionSeconds = 3600
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_auto' WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	_, e := client.SetAutoTopUp(ctx, req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: tenant, Enabled: true, Threshold: "5", Amount: "10", MonthlyCap: "20"}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e = w.Maintain(ctx, now); e != nil {
		t.Fatal(e)
	}
	if e = w.Maintain(ctx, now); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind='auto_topup'").Scan(&count); e != nil || count != 1 {
		t.Fatal("multiple automatic top-ups", count, e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE jobs SET state='done' WHERE kind='auto_topup'"); e != nil {
		t.Fatal(e)
	}
	if e = w.Maintain(ctx, now); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE jobs SET state='done' WHERE kind='auto_topup'"); e != nil {
		t.Fatal(e)
	}
	if e = w.Maintain(ctx, now); e != nil {
		t.Fatal(e)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind='auto_topup'").Scan(&count); e != nil || count != 2 {
		t.Fatal("monthly cap not enforced", count, e)
	}
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='' WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	_, e = db.Pool.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'credit',10,'fixture',now())", key(), tenant)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.DeleteTenant(ctx, req(&pb.DeleteTenantRequest{RequestKey: key(), TenantId: tenant}, "owner")); e != nil {
		t.Fatal(e)
	}
	future := now.Add(time.Duration(cfg.ReportingGraceSeconds+cfg.DeletionGraceSeconds+1) * time.Second)
	if e = w.Maintain(ctx, future); e != nil {
		t.Fatal(e)
	}
	var deleted bool
	if e = db.Pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM tenants WHERE id=$1", tenant).Scan(&deleted); e != nil || deleted {
		t.Fatal("purchased balance did not hold deletion", e)
	}
	if _, e = db.Pool.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'refund',-10,'fixture-refund',now())", key(), tenant); e != nil {
		t.Fatal(e)
	}
	if e = w.Maintain(ctx, future); e != nil {
		t.Fatal(e)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM tenants WHERE id=$1", tenant).Scan(&deleted); e != nil || !deleted {
		t.Fatal("settled deletion not completed", e)
	}
}
func TestCumulativeSettlementRounding(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "meter service", Service: &pb.Service{Name: "service", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"usage:write"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	start := now.Add(-48 * time.Hour)
	end := now.Add(-24 * time.Hour)
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 2; i++ {
		_, e = db.Pool.Exec(ctx, "INSERT INTO ledger(id,tenant_id,service_id,meter,kind,amount,period_start,period_end,source_key,created_at) VALUES($1,$2,$3,'calls','postpaid',-0.006,$4,$5,$1::uuid::text,$6)", key(), tenant, svc.Msg.Service.Id, start, end, now)
		if e != nil {
			t.Fatal(e)
		}
		if e = w.Maintain(ctx, now); e != nil {
			t.Fatal(e)
		}
	}
	var units string
	if e = db.Pool.QueryRow(ctx, "SELECT sum(billed_minor)::text FROM settlements WHERE tenant_id=$1", tenant).Scan(&units); e != nil || units != "1" {
		t.Fatal("incremental rounding charged twice", units, e)
	}
}
func TestUnknownPaymentIsNeverRecharged(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	topup, e := client.RequestTopUp(ctx, req(&pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE id=$1", topup.Msg.JobId); e != nil {
		t.Fatal(e)
	}
	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[]}`)
			return
		}
		calls++
		w.WriteHeader(500)
	}))
	defer mock.Close()
	cfg.PaddleURL = mock.URL
	cfg.PaddleToken = "fixture"
	cfg.PaddleCreditProductID = "pro_fixture"
	cfg.RetrySeconds = 1
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e = w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE jobs SET state='running' WHERE id=$1", topup.Msg.JobId); e != nil {
		t.Fatal(e)
	}
	if e = w.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if e = w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("ambiguous charge was repeated", calls)
	}
	var state string
	if e = db.Pool.QueryRow(ctx, "SELECT state FROM jobs WHERE id=$1", topup.Msg.JobId).Scan(&state); e != nil || state != "failed" {
		t.Fatal(state, e)
	}
}

func TestSignedRefundReplayAndDispute(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	topup, e := client.RequestTopUp(ctx, req(&pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.PaddleWebhookSecret = "fixture-webhook-secret"
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Now().UTC()
	transaction := provider.Transaction{ID: "txn_refund", Status: "completed", Currency: "USD"}
	transaction.Custom.JobID = topup.Msg.JobId
	transaction.Custom.TenantID = tenant
	transaction.Details.Totals.Subtotal = "1000"
	if e = w.ApplyTransaction(ctx, transaction, now); e != nil {
		t.Fatal(e)
	}
	send := func(eventID, action, status string, at time.Time, amount string) {
		t.Helper()
		body := fmt.Sprintf(`{"event_id":%q,"event_type":"adjustment.updated","occurred_at":%q,"data":{"id":"adj_fixture","transaction_id":"txn_refund","currency_code":"USD","action":%q,"status":%q,"totals":{"subtotal":%q}}}`, eventID, at.Format(time.RFC3339Nano), action, status, amount)
		timestamp := fmt.Sprint(time.Now().Unix())
		mac := hmac.New(sha256.New, []byte(cfg.PaddleWebhookSecret))
		mac.Write([]byte(timestamp + ":" + body))
		r := httptest.NewRequest("POST", "/webhooks/paddle", strings.NewReader(body))
		r.Header.Set("Paddle-Signature", "ts="+timestamp+";h1="+hex.EncodeToString(mac.Sum(nil)))
		rw := httptest.NewRecorder()
		w.Webhook(rw, r)
		if rw.Code != 202 {
			t.Fatal("webhook not persisted", rw.Code)
		}
		if e = w.ProcessWebhook(ctx, eventID); e != nil {
			t.Fatal(e)
		}
	}
	send("evt_refund", "chargeback", "approved", now, "300")
	send("evt_refund", "chargeback", "approved", now, "300")
	send("evt_old", "chargeback", "pending_approval", now.Add(-time.Second), "300")
	var balance string
	var active bool
	if e = db.Pool.QueryRow(ctx, "SELECT sum(amount)::text FROM ledger WHERE tenant_id=$1", tenant).Scan(&balance); e != nil {
		t.Fatal(e)
	}
	d, _ := accounting.Parse(balance)
	seven, _ := accounting.Parse("7")
	if d.Cmp(seven) != 0 {
		t.Fatal("duplicate refund", balance)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT active FROM disputes WHERE external_id='adj_fixture'").Scan(&active); e != nil || !active {
		t.Fatal("dispute not active", e)
	}
	if _, e = client.SetTenantBlocked(ctx, req(&pb.SetTenantBlockedRequest{RequestKey: key(), TenantId: tenant, Blocked: true, Reason: "independent block"}, "owner")); e != nil {
		t.Fatal(e)
	}
	send("evt_reversed", "chargeback", "reversed", now.Add(time.Second), "300")
	if e = db.Pool.QueryRow(ctx, "SELECT active FROM disputes WHERE external_id='adj_fixture'").Scan(&active); e != nil || active {
		t.Fatal("dispute did not reverse", e)
	}
	var blocked bool
	if e = db.Pool.QueryRow(ctx, "SELECT operator_block FROM tenants WHERE id=$1", tenant).Scan(&blocked); e != nil || !blocked {
		t.Fatal("reversal cleared independent block", e)
	}
	bad := httptest.NewRecorder()
	w.Webhook(bad, httptest.NewRequest("POST", "/webhooks/paddle", strings.NewReader(`{}`)))
	if bad.Code != 401 {
		t.Fatal("unsigned webhook accepted")
	}
}
func TestInstanceLockAndSchemaCompatibility(t *testing.T) {
	db, _, _ := setup(t)
	ctx := context.Background()
	lock, e := db.AcquireInstance(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := db.AcquireInstance(ctx); e == nil {
		second.Release()
		t.Fatal("second instance acquired lock")
	}
	lock.Conn().Close(ctx)
	lock.Release()
	if _, e = db.Pool.Exec(ctx, "INSERT INTO delcore_schema VALUES(999,'future',now())"); e != nil {
		t.Fatal(e)
	}
	if db.CheckSchema(ctx) == nil || db.Migrate(ctx) == nil {
		t.Fatal("newer schema accepted")
	}
}

func TestProratedAllowanceAndCatalog(t *testing.T) {
	db, client, _ := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "register service", Service: &pb.Service{Name: "metered", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"usage:read"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.PutMeter(ctx, req(&pb.PutMeterRequest{RequestKey: key(), Reason: "register meter", Meter: &pb.Meter{ServiceId: svc.Msg.Service.Id, Name: "units"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	addon, e := client.PutAddOn(ctx, req(&pb.PutAddOnRequest{RequestKey: key(), Reason: "register allowance addon", AddOn: &pb.AddOn{ServiceId: svc.Msg.Service.Id, Name: "extra"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	price, e := client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "register allowance", Price: &pb.Price{AddOnId: addon.Msg.AddOn.Id, ServiceId: svc.Msg.Service.Id, Meter: "units", UnitPrice: "0.0000001", Included: "102", Currency: "USD"}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now().UTC().Truncate(time.Second)
	end := start.Add(3 * time.Hour)
	at := start.Add(2 * time.Hour)
	if _, e = db.Pool.Exec(ctx, "INSERT INTO entitlements(id,tenant_id,price_id,starts_at) VALUES($1,$2,$3,$4)", key(), tenant, price.Msg.Price.Id, at); e != nil {
		t.Fatal(e)
	}
	previous, _ := accounting.Parse("2")
	for i := 0; i < 2; i++ {
		if e = db.Transaction(ctx, func(tx pgx.Tx) error {
			return postgres.GrantAllowances(ctx, tx, tenant, start, end, at, "upgrade-fixture", postgres.Allowances{svc.Msg.Service.Id + "\x00units": previous})
		}); e != nil {
			t.Fatal(e)
		}
	}
	var amount string
	var count int
	if e = db.Pool.QueryRow(ctx, "SELECT sum(amount)::text,count(*) FROM allowance_grants WHERE source_key LIKE 'upgrade-fixture:%'").Scan(&amount, &count); e != nil {
		t.Fatal(e)
	}
	got, e := accounting.Parse(amount)
	if e != nil || got.String() != "33.333333" || count != 1 {
		t.Fatal("proration precision or replay", amount, count, e)
	}
	catalog, e := client.GetCatalog(ctx, req(&pb.GetCatalogRequest{}, "owner"))
	if e != nil || len(catalog.Msg.AddOns) != 1 || len(catalog.Msg.Meters) != 1 {
		t.Fatal("catalog omitted addon or meter", e)
	}
	if _, e = client.ListUsage(ctx, req(&pb.ListUsageRequest{TenantId: tenant}, "service")); e != nil {
		t.Fatal("registered read scope", e)
	}
}

func TestOneTimeChargeReconciliation(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	cfg.PaddleCreditProductID = "pro_fixture"
	cfg.PaddleToken = "fixture"
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_charge',external_customer_id='ctm_charge',period_end=$2 WHERE id=$1", tenant, time.Now().Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	jobID := key()
	if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'auto_topup',$1::uuid::text,'{"amount":"10","minor_units":"1000","currency":"USD"}',now()-interval '1 second',now(),now())`, jobID, tenant); e != nil {
		t.Fatal(e)
	}
	charges := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			charges++
			if r.URL.Path != "/subscriptions/sub_charge/charge" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			var body map[string]json.RawMessage
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if _, exists := body["custom_data"]; exists {
				t.Error("unsupported charge custom_data")
			}
			if !strings.Contains(string(body["items"]), jobID) {
				t.Error("missing price-level attribution")
			}
			io.WriteString(w, `{"data":{"id":"sub_charge","status":"active"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"id":"txn_charge","status":"completed","currency_code":"USD","subscription_id":"sub_charge","customer_id":"ctm_charge","custom_data":{"delcore_job_id":"old-subscription-job"},"details":{"totals":{"subtotal":"1000"}},"items":[{"quantity":1,"price":{"id":"pri_charge","product_id":"pro_fixture","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q}}}]}]}`, jobID, tenant)
	}))
	defer mock.Close()
	cfg.PaddleURL = mock.URL
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 2; i++ {
		if e := w.Step(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE id=$1", jobID); e != nil {
			t.Fatal(e)
		}
	}
	var state, balance string
	if e := db.Pool.QueryRow(ctx, "SELECT state,(SELECT sum(amount)::text FROM ledger WHERE tenant_id=$2 AND kind='credit') FROM jobs WHERE id=$1", jobID, tenant).Scan(&state, &balance); e != nil {
		t.Fatal(e)
	}
	amount, e := accounting.Parse(balance)
	if e != nil || amount.String() != "10" || state != "done" || charges != 1 {
		t.Fatal("charge reconciliation", state, balance, charges, e)
	}
}

func TestWithdrawalReleasesIdentityAfterFinalization(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	old, tenant := bootstrap(t, client)
	if _, e := client.Withdraw(ctx, req(&pb.WithdrawRequest{RequestKey: key()}, "owner")); e != nil {
		t.Fatal(e)
	}
	if _, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner")); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("pending withdrawal re-registered", e)
	}
	cfg.DeletionGraceSeconds = 1
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e := w.Maintain(ctx, time.Now().UTC().Add(48*time.Hour)); e != nil {
		t.Fatal(e)
	}
	var email, subject string
	var deleted bool
	if e := db.Pool.QueryRow(ctx, "SELECT u.email,u.subject_hash,t.deleted_at IS NOT NULL FROM users u,tenants t WHERE u.id=$1 AND t.id=$2", old, tenant).Scan(&email, &subject, &deleted); e != nil {
		t.Fatal(e)
	}
	if email != "" || subject == "owner" || !deleted {
		t.Fatal("identity not deidentified")
	}
	me, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil || me.Msg.UserId == old {
		t.Fatal("completed withdrawal identity not released", e)
	}
}

func TestEmailRetriesPersistAndRecover(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	jobID := key()
	if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'email',$1::uuid::text,'{"to":"fixture@example.test","subject":"Fixture","text":"Fixture"}',now()-interval '1 second',now(),now())`, jobID, tenant); e != nil {
		t.Fatal(e)
	}
	success := false
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !success {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"result":{"queued":[{"email":"fixture@example.test"}]}}`)
	}))
	defer mock.Close()
	cfg.EmailURL = mock.URL
	cfg.EmailAccount = "fixture"
	cfg.EmailSender = "sender@example.test"
	cfg.EmailToken = "fixture"
	cfg.RetryLimit = 2
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 2; i++ {
		if e := w.Step(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE id=$1", jobID); e != nil {
			t.Fatal(e)
		}
	}
	var state string
	if e := db.Pool.QueryRow(ctx, "SELECT state FROM jobs WHERE id=$1", jobID).Scan(&state); e != nil || state != "failed" {
		t.Fatal("retry limit", state, e)
	}
	if _, e := client.RetryJob(ctx, req(&pb.RetryJobRequest{RequestKey: key(), JobId: jobID, Reason: "provider recovered"}, "owner")); e != nil {
		t.Fatal(e)
	}
	success = true
	if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE id=$1", jobID); e != nil {
		t.Fatal(e)
	}
	if e := w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if e := db.Pool.QueryRow(ctx, "SELECT state FROM jobs WHERE id=$1", jobID).Scan(&state); e != nil || state != "done" {
		t.Fatal("email recovery", state, e)
	}
}

func TestMonthlyRoundingIncludesPurchasedCredits(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	bootstrap(t, client)
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "register fractional meter", Service: &pb.Service{Name: "fractional", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"usage:write"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.PutMeter(ctx, req(&pb.PutMeterRequest{RequestKey: key(), Reason: "register meter", Meter: &pb.Meter{ServiceId: svc.Msg.Service.Id, Name: "units"}}, "owner")); e != nil {
		t.Fatal(e)
	}
	var plan string
	if e = db.Pool.QueryRow(ctx, "SELECT id::text FROM plans WHERE is_default").Scan(&plan); e != nil {
		t.Fatal(e)
	}
	if _, e = client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "fractional unit price", Price: &pb.Price{PlanId: plan, ServiceId: svc.Msg.Service.Id, Meter: "units", UnitPrice: "0.006", Included: "0", Currency: "USD"}}, "owner")); e != nil {
		t.Fatal(e)
	}
	org, e := client.CreateTenant(ctx, req(&pb.CreateTenantRequest{RequestKey: key(), Name: "Rounding"}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	tenant := org.Msg.Tenant.Id
	if _, e = db.Pool.Exec(ctx, "INSERT INTO ledger(id,tenant_id,kind,amount,source_key,created_at) VALUES($1,$2,'credit',1,$1::uuid::text,now())", key(), tenant); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	closeAt := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(time.Second)
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 2; i++ {
		if _, e = client.ReportUsage(ctx, req(&pb.ReportUsageRequest{RequestKey: key(), Usage: &pb.Usage{TenantId: tenant, ServiceId: svc.Msg.Service.Id, Meter: "units", Quantity: "1", EventKey: key(), OccurredAt: timestamppb.New(now)}}, "service")); e != nil {
			t.Fatal(e)
		}
		if e = w.Maintain(ctx, closeAt); e != nil {
			t.Fatal(e)
		}
		var balance string
		if e = db.Pool.QueryRow(ctx, "SELECT sum(amount)::text FROM ledger WHERE tenant_id=$1", tenant).Scan(&balance); e != nil {
			t.Fatal(e)
		}
		amount, e := accounting.Parse(balance)
		if e != nil || amount.String() != "0.99" {
			t.Fatal("monthly total must round once, including prepaid usage", balance, e)
		}
	}
}

func TestRetentionPreservesReplayTombstones(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	bootstrap(t, client)
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "retention service", Service: &pb.Service{Name: "retention", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"usage:write"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.PutMeter(ctx, req(&pb.PutMeterRequest{RequestKey: key(), Reason: "retention meter", Meter: &pb.Meter{ServiceId: svc.Msg.Service.Id, Name: "units"}}, "owner")); e != nil {
		t.Fatal(e)
	}
	var plan string
	if e = db.Pool.QueryRow(ctx, "SELECT id::text FROM plans WHERE is_default").Scan(&plan); e != nil {
		t.Fatal(e)
	}
	if _, e = client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "retention price", Price: &pb.Price{PlanId: plan, ServiceId: svc.Msg.Service.Id, Meter: "units", UnitPrice: "0", Included: "0", Currency: "USD"}}, "owner")); e != nil {
		t.Fatal(e)
	}
	org, e := client.CreateTenant(ctx, req(&pb.CreateTenantRequest{RequestKey: key(), Name: "Retention"}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	tenant := org.Msg.Tenant.Id
	report, e := client.ReportUsage(ctx, req(&pb.ReportUsageRequest{RequestKey: key(), Usage: &pb.Usage{TenantId: tenant, ServiceId: svc.Msg.Service.Id, Meter: "units", Quantity: "1", EventKey: key(), OccurredAt: timestamppb.Now()}}, "service"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.DeleteTenant(ctx, req(&pb.DeleteTenantRequest{RequestKey: key(), TenantId: tenant}, "owner")); e != nil {
		t.Fatal(e)
	}
	cfg.DeletionGraceSeconds = 1
	cfg.RawUsageRetentionSeconds = 3600
	cfg.AccountingRetentionSeconds = 3600
	cfg.AuditRetentionSeconds = 3600
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	future := time.Now().UTC().Add(48 * time.Hour)
	if e = w.Maintain(ctx, future); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM usage_events WHERE id=$1", report.Msg.UsageId).Scan(&count); e != nil || count != 1 {
		t.Fatal("accounting retention removed evidence early", count, e)
	}
	if e = w.Maintain(ctx, future.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM usage_events WHERE id=$1", report.Msg.UsageId).Scan(&count); e != nil || count != 0 {
		t.Fatal("expired usage not removed", count, e)
	}
	if e = db.Pool.QueryRow(ctx, "SELECT count(*) FROM usage_replay WHERE event_id=$1", report.Msg.UsageId).Scan(&count); e != nil || count != 1 {
		t.Fatal("replay identity lost", count, e)
	}
}

func TestSubscriptionRejectsHistoricalPrice(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	var plan string
	if e := db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&plan); e != nil {
		t.Fatal(e)
	}
	service := key()
	if _, e := db.Pool.Exec(ctx, "INSERT INTO services(id,name,client_id,capabilities,scopes) VALUES($1,'price test','price-test','{}','{}')", service); e != nil {
		t.Fatal(e)
	}
	old, current := key(), key()
	for i, price := range []string{old, current} {
		if _, e := db.Pool.Exec(ctx, "INSERT INTO prices(id,plan_id,service_id,meter,unit_price,included,currency,kind,external_price_id,created_at) VALUES($1,$2,$3,'units',1,0,'USD',1,'',$4)", price, plan, service, time.Now().Add(time.Duration(i)*time.Second)); e != nil {
			t.Fatal(e)
		}
	}
	for _, price := range []string{old, current} {
		_, e := client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: plan, PriceIds: []string{price}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
		if price == old && connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatalf("historical price accepted: %v", e)
		}
		if price == current && e != nil {
			t.Fatal(e)
		}
	}
}

func TestAutoTopupFailureNotification(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	jobID := key()
	if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,available_at,created_at,updated_at) VALUES($1,$2,'auto_topup',$1::uuid::text,'{}','reconcile',now(),now(),now())`, jobID, tenant); e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg}
	for i := 0; i < 2; i++ {
		event := provider.Event{ID: fmt.Sprintf("evt_failed_%d", i), Type: "transaction.payment_failed", OccurredAt: time.Now(), Data: json.RawMessage(fmt.Sprintf(`{"id":"txn_failed","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q}}`, jobID, tenant))}
		raw, _ := json.Marshal(event)
		if _, e := db.Pool.Exec(ctx, "INSERT INTO webhooks(event_id,payload,received_at,occurred_at) VALUES($1,$2,now(),$3)", event.ID, raw, event.OccurredAt); e != nil {
			t.Fatal(e)
		}
		if e := w.ProcessWebhook(ctx, event.ID); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := db.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind='email' AND dedup_key LIKE $1", "auto_failed:"+jobID+":%").Scan(&count); e != nil || count != 1 {
		t.Fatal("failure notification missing or duplicated", count, e)
	}
}

func TestDeletionDistinguishesRejectedAndUnknownPurchases(t *testing.T) {
	for _, category := range []string{"provider_rejected", "payment_failed", "payment_method_required", "unknown_payment_outcome", "retry_exhausted", "payment_mismatch"} {
		t.Run(category, func(t *testing.T) {
			db, client, cfg := setup(t)
			ctx := context.Background()
			_, tenant := bootstrap(t, client)
			if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET deleting_at=now()-interval '3 days' WHERE id=$1", tenant); e != nil {
				t.Fatal(e)
			}
			jobID := key()
			if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,error_category,available_at,created_at,updated_at) VALUES($1,$2,'topup',$1::uuid::text,'{}','failed',$3,now(),now(),now())`, jobID, tenant, category); e != nil {
				t.Fatal(e)
			}
			w := &worker.Worker{DB: db, Config: cfg}
			if e := w.Maintain(ctx, time.Now()); e != nil {
				t.Fatal(e)
			}
			var deleted bool
			if e := db.Pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM tenants WHERE id=$1", tenant).Scan(&deleted); e != nil {
				t.Fatal(e)
			}
			want := category == "provider_rejected" || category == "payment_failed" || category == "payment_method_required"
			if deleted != want {
				t.Fatalf("deleted=%v, want %v", deleted, want)
			}
		})
	}
}

func TestRecoveredSubscriptionMutationUsesKnownSubscription(t *testing.T) {
	for _, kind := range []string{"cancel", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			db, client, cfg := setup(t)
			ctx := context.Background()
			_, tenant := bootstrap(t, client)
			if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_recovered' WHERE id=$1", tenant); e != nil {
				t.Fatal(e)
			}
			jobID := key()
			if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,available_at,created_at,updated_at) VALUES($1,$2,$3,$1::uuid::text,'{"timing":"do_not_bill","external_price_ids":["pri_new"]}','running',now(),now(),now())`, jobID, tenant, kind); e != nil {
				t.Fatal(e)
			}
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/subscriptions/sub_recovered" {
					t.Errorf("unexpected recovery request %s %s", r.Method, r.URL.Path)
				}
				io.WriteString(w, `{"data":{"id":"sub_recovered","status":"active","items":[{"price":{"id":"pri_new"},"quantity":1}],"scheduled_change":{"action":"cancel"}}}`)
			}))
			defer mock.Close()
			cfg.PaddleURL = mock.URL
			w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if e := w.Recover(ctx); e != nil {
				t.Fatal(e)
			}
			if e := w.Step(ctx); e != nil {
				t.Fatal(e)
			}
			var state, external string
			if e := db.Pool.QueryRow(ctx, "SELECT state,external_id FROM jobs WHERE id=$1", jobID).Scan(&state, &external); e != nil || state != "reconcile" || external != "sub_recovered" {
				t.Fatal(state, external, e)
			}
		})
	}
}

func TestLateSettlementUsesNextRenewal(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	cfg.PaddleCreditProductID = "pro_fixture"
	cfg.PaddleToken = "fixture"
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_charge',external_customer_id='ctm_charge',period_end=$2 WHERE id=$1", tenant, time.Now().Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	jobID := key()
	if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,'settlement',$1::uuid::text,'{"amount":"10","minor_units":"1000","currency":"USD"}',now()-interval '1 second',now(),now())`, jobID, tenant); e != nil {
		t.Fatal(e)
	}
	charges := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			charges++
			if r.URL.Path != "/subscriptions/sub_charge/charge" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			var body map[string]json.RawMessage
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if string(body["effective_from"]) != `"next_billing_period"` {
				t.Error("late settlement charged outside renewal")
			}
			if _, exists := body["custom_data"]; exists {
				t.Error("unsupported charge custom_data")
			}
			if !strings.Contains(string(body["items"]), jobID) {
				t.Error("missing price-level attribution")
			}
			io.WriteString(w, `{"data":{"id":"sub_charge","status":"active"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"id":"txn_charge","status":"completed","origin":"subscription_recurring","currency_code":"USD","subscription_id":"sub_charge","customer_id":"ctm_charge","custom_data":{"delcore_job_id":"old-subscription-job"},"details":{"totals":{"subtotal":"3000"},"line_items":[{"price_id":"pri_charge","totals":{"subtotal":"1000"}}]},"items":[{"quantity":1,"price":{"id":"pri_charge","product_id":"pro_fixture","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q}}}]}]}`, jobID, tenant)
	}))
	defer mock.Close()
	cfg.PaddleURL = mock.URL
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 2; i++ {
		if e := w.Step(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE id=$1", jobID); e != nil {
			t.Fatal(e)
		}
	}
	var state, balance string
	if e := db.Pool.QueryRow(ctx, "SELECT state,(SELECT sum(amount)::text FROM ledger WHERE tenant_id=$2 AND kind='settlement') FROM jobs WHERE id=$1", jobID, tenant).Scan(&state, &balance); e != nil {
		t.Fatal(e)
	}
	amount, e := accounting.Parse(balance)
	if e != nil || amount.String() != "10" || state != "done" || charges != 1 {
		t.Fatal("charge reconciliation", state, balance, charges, e)
	}
}

func TestEmptyMutationResponsesReplay(t *testing.T) {
	db, client, _ := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	q := &pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: tenant, Enabled: false}
	for i := 0; i < 2; i++ {
		if _, e := client.SetAutoTopUp(ctx, req(q, "owner")); e != nil {
			t.Fatal("empty response retry", e)
		}
	}
	var size int
	if e := db.Pool.QueryRow(ctx, "SELECT octet_length(response) FROM requests WHERE request_key=$1", q.RequestKey).Scan(&size); e != nil || size != 0 {
		t.Fatal("fixture must store empty protobuf", size, e)
	}
	deletion := &pb.DeleteTenantRequest{RequestKey: key(), TenantId: tenant}
	for i := 0; i < 2; i++ {
		if _, e := client.DeleteTenant(ctx, req(deletion, "owner")); e != nil {
			t.Fatal("deletion retry", e)
		}
	}
}

func TestAutoTopupRequiresActiveSubscription(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	for _, state := range []string{"free", "active", "failed"} {
		if state != "free" {
			if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_auto',renewal_failed=$2 WHERE id=$1", tenant, state == "failed"); e != nil {
				t.Fatal(e)
			}
		}
		_, e := client.SetAutoTopUp(ctx, req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: tenant, Enabled: true, Threshold: "5", Amount: "10", MonthlyCap: "20"}, "owner"))
		if state == "active" && e != nil {
			t.Fatal(e)
		}
		if state != "active" && connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatal("inactive subscription accepted", e)
		}
	}
	if _, e := client.RequestTopUp(ctx, req(&pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}, "owner")); e != nil {
		t.Fatal("manual recovery unavailable", e)
	}
}

func TestFreePlanTransitions(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	service := key()
	if _, e := db.Pool.Exec(ctx, "INSERT INTO services VALUES($1,'free transitions','free-transition','{}','{}')", service); e != nil {
		t.Fatal(e)
	}
	var plans, prices []string
	for _, rank := range []int{10, 5} {
		plan, price := key(), key()
		plans = append(plans, plan)
		prices = append(prices, price)
		if _, e := db.Pool.Exec(ctx, "INSERT INTO plans(id,name,rank,free,postpaid) VALUES($1,'free', $2,true,false)", plan, rank); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Pool.Exec(ctx, "INSERT INTO prices(id,plan_id,service_id,meter,unit_price,included,currency,external_price_id,created_at) VALUES($1,$2,$3,'units',1,10,'USD','',now())", price, plan, service); e != nil {
			t.Fatal(e)
		}
	}
	for i, plan := range plans {
		change, e := client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: plan, PriceIds: []string{prices[i]}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
		if e != nil {
			t.Fatal(e)
		}
		var active, state string
		var end time.Time
		if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text,period_end,(SELECT state FROM jobs WHERE id=$2) FROM tenants WHERE id=$1", tenant, change.Msg.JobId).Scan(&active, &end, &state); e != nil {
			t.Fatal(e)
		}
		if i == 0 && (active != plan || state != "done") {
			t.Fatal("free upgrade not applied", active, state)
		}
		if i == 1 {
			if active != plans[0] || state != "pending" {
				t.Fatal("downgrade applied early")
			}
			w := &worker.Worker{DB: db, Config: cfg}
			if e = w.Maintain(ctx, end.Add(time.Second)); e != nil {
				t.Fatal(e)
			}
			if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&active); e != nil || active != plan {
				t.Fatal("free downgrade not applied", e)
			}
		}
	}
}

func TestDeletionWaitsToCancelUntilSettlement(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_delete',period_end=now()+interval '1 hour' WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := client.DeleteTenant(ctx, req(&pb.DeleteTenantRequest{RequestKey: key(), TenantId: tenant}, "owner")); e != nil {
		t.Fatal(e)
	}
	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/subscriptions/sub_delete/cancel" {
			t.Error(r.URL.Path)
		}
		io.WriteString(w, `{"data":{"id":"sub_delete","status":"active"}}`)
	}))
	defer mock.Close()
	cfg.PaddleURL = mock.URL
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e := w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatal("cancellation preceded reporting grace")
	}
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET deleting_at=now()-interval '3 days' WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Pool.Exec(ctx, "INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,available_at,created_at,updated_at) VALUES($1,$2,'settlement','final-debt','{}','failed',now(),now(),now())", key(), tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE kind='cancel'"); e != nil {
		t.Fatal(e)
	}
	if e := w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatal("cancellation preceded debt settlement")
	}
	if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET state='done' WHERE tenant_id=$1 AND kind='settlement'", tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Pool.Exec(ctx, "UPDATE jobs SET available_at=now()-interval '1 second' WHERE kind='cancel'"); e != nil {
		t.Fatal(e)
	}
	if e := w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("settled deletion did not cancel", calls)
	}
}

func TestPaidUsagePriceRefreshWithoutCharge(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	svc, e := client.PutService(ctx, req(&pb.PutServiceRequest{RequestKey: key(), Reason: "create service", Service: &pb.Service{Name: "paid-service", ClientId: "service", Capabilities: []string{"execute"}, Scopes: []string{"authorize", "usage:write"}}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "default free plan", Plan: &pb.Plan{Name: "free", Free: true, DefaultFree: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	paid, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "paid plan", Plan: &pb.Plan{Name: "paid", Rank: 10, Postpaid: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	price, e := client.CreatePrice(ctx, req(&pb.CreatePriceRequest{RequestKey: key(), Reason: "base subscription", Price: &pb.Price{PlanId: paid.Msg.Plan.Id, ServiceId: svc.Msg.Service.Id, Meter: "base", UnitPrice: "10", Included: "0", Currency: "USD", ExternalPriceId: "pri_base", Kind: pb.PriceKind_PRICE_KIND_RECURRING}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	me, e := client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	tenant := me.Msg.Tenants[0].Id
	change, e := client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: paid.Msg.Plan.Id, PriceIds: []string{price.Msg.Price.Id}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	var before string
	if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if before == paid.Msg.Plan.Id {
		t.Fatal("checkout intent activated paid plan")
	}
	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"id":"txn_initial","status":"completed","currency_code":"USD","customer_id":"ctm_fixture","subscription_id":"sub_fixture","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q},"items":[{"price":{"id":"pri_base"},"quantity":1}],"billing_period":{"starts_at":%q,"ends_at":%q}}`, change.Msg.JobId, tenant, now.Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano))
	var transaction provider.Transaction
	if e = json.Unmarshal([]byte(payload), &transaction); e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e = w.ApplyTransaction(ctx, transaction, now); e != nil {
		t.Fatal(e)
	}
	var active string
	if e = db.Pool.QueryRow(ctx, "SELECT plan_id::text FROM tenants WHERE id=$1", tenant).Scan(&active); e != nil || active != paid.Msg.Plan.Id {
		t.Fatal("paid activation", active, e)
	}
	usage := key()
	if _, e = db.Pool.Exec(ctx, "INSERT INTO prices(id,plan_id,service_id,meter,unit_price,included,currency,external_price_id,created_at) VALUES($1,$2,$3,'units',2,10,'USD','',now())", usage, paid.Msg.Plan.Id, svc.Msg.Service.Id); e != nil {
		t.Fatal(e)
	}
	refresh, e := client.ChangeSubscription(ctx, req(&pb.ChangeSubscriptionRequest{RequestKey: key(), TenantId: tenant, PlanId: paid.Msg.Plan.Id, PriceIds: []string{price.Msg.Price.Id, usage}, ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	var state string
	var installed bool
	if e = db.Pool.QueryRow(ctx, "SELECT state,EXISTS(SELECT 1 FROM entitlements WHERE tenant_id=$2 AND price_id=$3 AND ends_at IS NULL) FROM jobs WHERE id=$1", refresh.Msg.JobId, tenant, usage).Scan(&state, &installed); e != nil || state != "done" || !installed {
		t.Fatal("no-charge refresh stuck", state, installed, e)
	}
}

func TestMismatchedAutomaticPaymentSuspendsCharges(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_auto',auto_amount=10,auto_threshold=5,auto_cap=100 WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	jobID := key()
	if _, e := db.Pool.Exec(ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,available_at,created_at,updated_at) VALUES($1,$2,'auto_topup',$1::uuid::text,'{"amount":"10","minor_units":"1000"}','reconcile',now(),now(),now())`, jobID, tenant); e != nil {
		t.Fatal(e)
	}
	var payment provider.Transaction
	if e := json.Unmarshal([]byte(fmt.Sprintf(`{"id":"txn_mismatch","status":"completed","currency_code":"USD","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q},"details":{"totals":{"subtotal":"999"}}}`, jobID, tenant)), &payment); e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg}
	if e := w.ApplyTransaction(ctx, payment, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := w.Maintain(ctx, time.Now()); e != nil {
		t.Fatal(e)
	}
	var suspended bool
	var count int
	if e := db.Pool.QueryRow(ctx, "SELECT auto_suspended,(SELECT count(*) FROM jobs WHERE tenant_id=$1 AND kind='auto_topup') FROM tenants WHERE id=$1", tenant).Scan(&suspended, &count); e != nil || !suspended || count != 1 {
		t.Fatal("mismatch triggered another charge", suspended, count, e)
	}
}

func TestLiveWorkerRecoversFailedFinish(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	topup, e := client.RequestTopUp(ctx, req(&pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Pool.Exec(ctx, `CREATE FUNCTION fail_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state='running' THEN RAISE EXCEPTION 'simulated database write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_finish BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION fail_finish()`); e != nil {
		t.Fatal(e)
	}
	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			calls++
			io.WriteString(w, `{"data":{"id":"txn_live","status":"completed"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"id":"txn_live","status":"completed","currency_code":"USD","custom_data":{"delcore_job_id":%q,"delcore_tenant_id":%q},"details":{"totals":{"subtotal":"1000"}}}]}`, topup.Msg.JobId, tenant)
	}))
	defer mock.Close()
	cfg.PaddleURL = mock.URL
	w := &worker.Worker{DB: db, Config: cfg, Provider: provider.New(cfg), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if e = w.Step(ctx); e == nil {
		t.Fatal("finish fault was not exercised")
	}
	if _, e = db.Pool.Exec(ctx, "DROP TRIGGER fail_finish ON jobs"); e != nil {
		t.Fatal(e)
	}
	if e = w.Step(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	var credits int
	if e = db.Pool.QueryRow(ctx, "SELECT state,(SELECT count(*) FROM ledger WHERE tenant_id=$2 AND kind='credit') FROM jobs WHERE id=$1", topup.Msg.JobId, tenant).Scan(&state, &credits); e != nil || state != "done" || credits != 1 || calls != 1 {
		t.Fatal("live recovery stranded or repeated charge", state, credits, calls, e)
	}
}

func TestDefaultFreePlanCannotBeRemoved(t *testing.T) {
	db, client, _ := setup(t)
	ctx := context.Background()
	bootstrap(t, client)
	var plan pb.Plan
	if e := db.Pool.QueryRow(ctx, "SELECT id::text,name,rank,free,is_default,postpaid FROM plans WHERE is_default").Scan(&plan.Id, &plan.Name, &plan.Rank, &plan.Free, &plan.DefaultFree, &plan.Postpaid); e != nil {
		t.Fatal(e)
	}
	plan.DefaultFree = false
	if _, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "test default protection", Plan: &plan}, "owner")); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("default removal accepted", e)
	}
	replacement, e := client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "replace default", Plan: &pb.Plan{Name: "replacement", Rank: 1, Free: true, DefaultFree: true}}, "owner"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.PutPlan(ctx, req(&pb.PutPlanRequest{RequestKey: key(), Reason: "update former default", Plan: &plan}, "owner")); e != nil {
		t.Fatal(e)
	}
	var current string
	if e = db.Pool.QueryRow(ctx, "SELECT id::text FROM plans WHERE is_default").Scan(&current); e != nil || current != replacement.Msg.Plan.Id {
		t.Fatal("default lost", current, e)
	}
	if _, e = client.GetCurrentUser(ctx, req(&pb.GetCurrentUserRequest{}, "member")); e != nil {
		t.Fatal("new user bootstrap", e)
	}
}

func TestDeletionStopsUnsentSubscriptions(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		for _, state := range []string{"pending", "running", "reconcile", "failed"} {
			t.Run(fmt.Sprintf("withdraw=%t/%s", withdraw, state), func(t *testing.T) {
				db, client, _ := setup(t)
				ctx := context.Background()
				_, tenant := bootstrap(t, client)
				job := key()
				attempts := 1
				if state == "pending" {
					attempts = 0
				}
				if _, e := db.Pool.Exec(ctx, "INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,state,attempts,available_at,created_at,updated_at) VALUES($1,$2,'subscription',$1::uuid::text,'{}',$3,$4,now(),now(),now())", job, tenant, state, attempts); e != nil {
					t.Fatal(e)
				}
				var e error
				if withdraw {
					_, e = client.Withdraw(ctx, req(&pb.WithdrawRequest{RequestKey: key()}, "owner"))
				} else {
					_, e = client.DeleteTenant(ctx, req(&pb.DeleteTenantRequest{RequestKey: key(), TenantId: tenant}, "owner"))
				}
				if state == "pending" && e != nil {
					t.Fatal(e)
				}
				if state != "pending" && connect.CodeOf(e) != connect.CodeFailedPrecondition {
					t.Fatal("uncertain intent did not block deletion", e)
				}
				var deleting, withdrawing bool
				var actual string
				if e = db.Pool.QueryRow(ctx, "SELECT t.deleting_at IS NOT NULL,u.withdrawing_at IS NOT NULL,j.state FROM tenants t JOIN users u ON u.id=t.owner_id JOIN jobs j ON j.tenant_id=t.id WHERE j.id=$1", job).Scan(&deleting, &withdrawing, &actual); e != nil {
					t.Fatal(e)
				}
				if state == "pending" {
					if !deleting || actual != "done" || withdrawing != withdraw {
						t.Fatal("unsent subscription not stopped", deleting, withdrawing, actual)
					}
				} else if deleting || withdrawing || actual != state {
					t.Fatal("deletion changed uncertain intent", deleting, withdrawing, actual)
				}
			})
		}
	}
}

func TestCancellationSuspendsAutomaticTopups(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, tenant := bootstrap(t, client)
	if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_cancel',period_end=now()+interval '10 days' WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	setting := func() error {
		_, e := client.SetAutoTopUp(ctx, req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: tenant, Enabled: true, Threshold: "5", Amount: "10", MonthlyCap: "100"}, "owner"))
		return e
	}
	if e := setting(); e != nil {
		t.Fatal(e)
	}
	w := &worker.Worker{DB: db, Config: cfg}
	if e := w.Maintain(ctx, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := client.CancelSubscription(ctx, req(&pb.CancelSubscriptionRequest{RequestKey: key(), TenantId: tenant}, "owner")); e != nil {
		t.Fatal(e)
	}
	if e := setting(); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("re-enabled while canceling", e)
	}
	if e := w.Maintain(ctx, time.Now()); e != nil {
		t.Fatal(e)
	}
	var suspended bool
	var pending, total int
	if e := db.Pool.QueryRow(ctx, "SELECT auto_suspended,(SELECT count(*) FROM jobs WHERE tenant_id=$1 AND kind='auto_topup' AND state<>'done'),(SELECT count(*) FROM jobs WHERE tenant_id=$1 AND kind='auto_topup') FROM tenants WHERE id=$1", tenant).Scan(&suspended, &pending, &total); e != nil || !suspended || pending != 0 || total != 1 {
		t.Fatal("automatic charge survived cancellation", suspended, pending, total, e)
	}
}
