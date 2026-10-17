// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/protos/gen/go/delcore/v1/delcorev1connect"
	"github.com/delinoio/oss/servers/delcore-api/internal/auth"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
)

type Server struct {
	delcorev1connect.UnimplementedDelcoreServiceHandler
	DB     *postgres.Store
	Auth   auth.Verifier
	Config config.Config
	Log    *slog.Logger
	Now    func() time.Time
}
type call struct {
	ctx                               context.Context
	tx                                pgx.Tx
	identity                          auth.Identity
	user, service, actor, correlation string
	now                               time.Time
	s                                 *Server
}

func ID() string             { return uuid.Must(uuid.NewV7()).String() }
func invalid(s string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(s)) }
func forbidden() error {
	return connect.NewError(connect.CodePermissionDenied, errors.New("operation is not permitted"))
}
func precondition(s string) error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(s))
}
func conflict() error {
	return connect.NewError(connect.CodeAlreadyExists, errors.New("request identity conflicts with previous input"))
}
func canonical(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u.Version() == 7 && u.String() == s
}
func (s *Server) Handler() (string, http.Handler) {
	return delcorev1connect.NewDelcoreServiceHandler(s, connect.WithReadMaxBytes(int(s.Config.MaxPayloadBytes)))
}
func run[Q any, R any](s *Server, ctx context.Context, req *connect.Request[Q], out *R, fn func(*call) error) (*connect.Response[R], error) {
	correlation := ID()
	id, e := s.Auth.Verify(ctx, req.Header().Get("Authorization"))
	if e != nil {
		return nil, typed(connect.NewError(connect.CodeUnauthenticated, errors.New("authentication failed")), correlation)
	}
	ctx, cancel := context.WithTimeout(ctx, s.Config.Timeout())
	defer cancel()
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	var key string
	value := reflect.ValueOf(req.Msg).Elem()
	f := value.FieldByName("RequestKey")
	if f.IsValid() {
		key = f.String()
		if !canonical(key) {
			return nil, typed(invalid("request_key must be a UUID v7"), correlation)
		}
	}
	message := any(req.Msg).(proto.Message)
	raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if e != nil {
		return nil, typed(invalid("invalid request"), correlation)
	}
	sum := sha256.Sum256(append([]byte(req.Spec().Procedure+"\x00"), raw...))
	fingerprint := hex.EncodeToString(sum[:])
	e = s.DB.Transaction(ctx, func(tx pgx.Tx) error {
		c := &call{ctx: ctx, tx: tx, identity: id, now: now, s: s, correlation: correlation}
		if id.ClientID != "" {
			var scopes []string
			if e := tx.QueryRow(ctx, "SELECT id::text,scopes FROM services WHERE client_id=$1", id.ClientID).Scan(&c.service, &scopes); e != nil {
				return forbidden()
			}
			c.actor = "service:" + c.service
		} else {
			c.user = ID()
			var withdrawing *time.Time
			e := tx.QueryRow(ctx, `INSERT INTO users(id,issuer,subject_hash,email) VALUES($1,$2,$3,$4)
    ON CONFLICT(issuer,subject_hash) DO UPDATE SET email=EXCLUDED.email
    RETURNING id::text,withdrawing_at`, c.user, s.Config.Issuer, id.SubjectHash, id.Email).Scan(&c.user, &withdrawing)
			if e != nil {
				return e
			}
			if withdrawing != nil {
				return precondition("withdrawal is pending")
			}
			c.actor = "user:" + c.user
			var personal, configured bool
			if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tenants WHERE personal AND owner_id=$1 AND deleted_at IS NULL),EXISTS(SELECT 1 FROM plans WHERE is_default AND free)", c.user).Scan(&personal, &configured); e != nil {
				return e
			}
			if !personal && configured {
				if _, e = c.createTenant("Personal", true); e != nil {
					return e
				}
			}
			if !configured && !id.Operator {
				return precondition("default free plan is not configured")
			}

		}
		if key != "" {
			var old string
			var cached []byte
			e := tx.QueryRow(ctx, "SELECT fingerprint,response FROM requests WHERE actor_id=$1 AND request_key=$2", c.actor, key).Scan(&old, &cached)
			if e == nil {
				if old != fingerprint {
					return conflict()
				}
				if len(cached) == 0 {
					return precondition("cached response was removed under retention policy")
				}
				return proto.Unmarshal(cached, any(out).(proto.Message))
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}
		if e := fn(c); e != nil {
			return e
		}
		if key != "" {
			b, e := proto.Marshal(any(out).(proto.Message))
			if e != nil {
				return e
			}
			var tenant any
			field := value.FieldByName("TenantId")
			if field.IsValid() && canonical(field.String()) {
				tenant = field.String()
			}
			if tenant == nil {
				if created, ok := any(out).(*pb.CreateTenantResponse); ok && created.Tenant != nil {
					tenant = created.Tenant.Id
				}
			}
			_, e = tx.Exec(ctx, "INSERT INTO requests(actor_id,request_key,fingerprint,response,created_at,tenant_id) VALUES($1,$2,$3,$4,$5,$6)", c.actor, key, fingerprint, b, now, tenant)
			return e
		}
		return nil
	})
	if e != nil {
		var databaseError *pgconn.PgError
		if errors.As(e, &databaseError) {
			s.Log.ErrorContext(ctx, "database operation failed", "correlation_id", correlation, "sqlstate", databaseError.Code, "position", databaseError.Position)
		}
		s.Log.WarnContext(ctx, "rpc rejected", "correlation_id", correlation, "procedure", req.Spec().Procedure, "category", connect.CodeOf(e).String())
		return nil, typed(e, correlation)
	}
	reflect.ValueOf(out).Elem().FieldByName("Metadata").Set(reflect.ValueOf(&pb.Metadata{CorrelationId: correlation}))
	resp := connect.NewResponse(out)
	resp.Header().Set("X-Delcore-Correlation-Id", correlation)
	return resp, nil
}
func typed(err error, id string) error {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		ce = connect.NewError(connect.CodeInternal, errors.New("operation could not be completed"))
	}
	reason := pb.ErrorReason_ERROR_REASON_DEPENDENCY
	switch ce.Code() {
	case connect.CodeInvalidArgument:
		reason = pb.ErrorReason_ERROR_REASON_INVALID_INPUT
	case connect.CodePermissionDenied:
		reason = pb.ErrorReason_ERROR_REASON_FORBIDDEN
	case connect.CodeUnauthenticated:
		reason = pb.ErrorReason_ERROR_REASON_UNAUTHENTICATED
	case connect.CodeAlreadyExists:
		reason = pb.ErrorReason_ERROR_REASON_CONFLICT
	case connect.CodeFailedPrecondition:
		reason = pb.ErrorReason_ERROR_REASON_PRECONDITION
	}
	d, e := connect.NewErrorDetail(&pb.ErrorDetail{Reason: reason, CorrelationId: id})
	if e == nil {
		ce.AddDetail(d)
	}
	ce.Meta().Set("X-Delcore-Correlation-Id", id)
	return ce
}
func (c *call) operator(reason string) error {
	if !c.identity.Operator || c.user == "" {
		return forbidden()
	}
	if len(reason) < 3 || len(reason) > 1000 {
		return invalid("operator reason must contain 3 to 1000 characters")
	}
	return nil
}
func (c *call) audit(tenant, action, reason string) error {
	var t any
	if tenant != "" {
		t = tenant
	}
	_, e := c.tx.Exec(c.ctx, "INSERT INTO audit VALUES($1,$2,$3,$4,$5,$6,$7)", ID(), c.actor, t, action, reason, c.correlation, c.now)
	return e
}
func (c *call) roles(tenant string) ([]int32, error) {
	if c.user == "" || !canonical(tenant) {
		return nil, forbidden()
	}
	var roles []int32
	e := c.tx.QueryRow(c.ctx, `SELECT m.roles FROM members m JOIN tenants t ON t.id=m.tenant_id WHERE m.tenant_id=$1 AND m.user_id=$2 AND t.deleted_at IS NULL`, tenant, c.user).Scan(&roles)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, forbidden()
	}
	return roles, e
}
func has(roles []int32, want ...pb.Role) bool {
	for _, r := range roles {
		for _, w := range want {
			if r == int32(w) {
				return true
			}
		}
	}
	return false
}
func (c *call) require(tenant string, want ...pb.Role) error {
	r, e := c.roles(tenant)
	if e != nil {
		return e
	}
	if !has(r, append(want, pb.Role_ROLE_OWNER)...) {
		return forbidden()
	}
	return nil
}
func (c *call) member(tenant string) error { _, e := c.roles(tenant); return e }
func page(p *pb.Page) (int, string, error) {
	if p == nil {
		return 50, "00000000-0000-0000-0000-000000000000", nil
	}
	n := int(p.Size)
	if n == 0 {
		n = 50
	}
	if n < 1 || n > 100 {
		return 0, "", invalid("page size must be 1 to 100")
	}
	after := p.AfterId
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	} else if !canonical(after) {
		return 0, "", invalid("invalid cursor")
	}
	return n, after, nil
}
func (c *call) job(tenant, kind, key string, payload any) (string, error) {
	id := ID()
	var t any
	if tenant != "" {
		t = tenant
	}
	_, e := c.tx.Exec(c.ctx, `INSERT INTO jobs(id,tenant_id,kind,dedup_key,payload,available_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6,$6)`, id, t, kind, key, payload, c.now)
	return id, e
}
func (c *call) serviceScope(scope string) error {
	if c.service == "" {
		return forbidden()
	}
	var ok bool
	e := c.tx.QueryRow(c.ctx, "SELECT $2=ANY(scopes) FROM services WHERE id=$1", c.service, scope).Scan(&ok)
	if e != nil {
		return e
	}
	for _, s := range c.identity.Scopes {
		if ok && s == scope {
			return nil
		}
	}
	return forbidden()
}
func nonempty(s string, max int) error {
	if len(s) == 0 || len(s) > max {
		return invalid(fmt.Sprintf("value must contain 1 to %d bytes", max))
	}
	return nil
}
