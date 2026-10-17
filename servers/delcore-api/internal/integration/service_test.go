//go:build integration

// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"context"
	"errors"
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
	"github.com/delinoio/oss/servers/delcore-api/internal/auth"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/provider"
	"github.com/delinoio/oss/servers/delcore-api/internal/rpc"
	"github.com/delinoio/oss/servers/delcore-api/internal/worker"
	"github.com/google/uuid"
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
	topup, e := client.RequestTopUp(ctx, req(&pb.RequestTopUpRequest{RequestKey: key(), TenantId: tenant, Amount: "10", ReturnUrl: cfg.ReturnURLs[0]}, "owner"))
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
	if _, e = client.RemoveMember(ctx, req(&pb.RemoveMemberRequest{RequestKey: key(), TenantId: tenant, UserId: member.Msg.UserId}, "member")); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("last owner left", e)
	}
}
