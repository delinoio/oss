package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	adminUserID  = "018f7c1e-7b4a-7abc-8def-0123456789ab"
	targetUserID = "018f7c1e-7b4a-7abc-8def-0123456789ad"
	uploadID     = "018f7c1e-7b4a-7abc-8def-0123456789ae"
	eventID      = "018f7c1e-7b4a-7abc-8def-0123456789af"
)

func TestEveryAdminRPCRequiresExactRole(t *testing.T) {
	service := newTestAdminService(t, &adminRepository{}, &adminUploads{})
	ctx := auth.WithPrincipal(WithCorrelationID(context.Background(), testCorrelationID), auth.Principal{
		User: domain.User{ID: adminUserID}, Roles: []string{"devhud-admin-helper"},
	})
	tests := map[string]func() error{
		"ListUsers": func() error {
			_, err := service.ListUsers(ctx, connect.NewRequest(&devhudv1.ListUsersRequest{}))
			return err
		},
		"SetUserBlocked": func() error {
			_, err := service.SetUserBlocked(ctx, connect.NewRequest(&devhudv1.SetUserBlockedRequest{}))
			return err
		},
		"GetUserUsage": func() error {
			_, err := service.GetUserUsage(ctx, connect.NewRequest(&devhudv1.GetUserUsageRequest{}))
			return err
		},
		"ListUploads": func() error {
			_, err := service.ListUploads(ctx, connect.NewRequest(&devhudv1.AdminServiceListUploadsRequest{}))
			return err
		},
		"QuarantineUpload": func() error {
			_, err := service.QuarantineUpload(ctx, connect.NewRequest(&devhudv1.QuarantineUploadRequest{}))
			return err
		},
		"DeleteUpload": func() error {
			_, err := service.DeleteUpload(ctx, connect.NewRequest(&devhudv1.AdminServiceDeleteUploadRequest{}))
			return err
		},
		"ListAuditEvents": func() error {
			_, err := service.ListAuditEvents(ctx, connect.NewRequest(&devhudv1.ListAuditEventsRequest{}))
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if code := connect.CodeOf(call()); code != connect.CodePermissionDenied {
				t.Fatalf("code = %v, want PermissionDenied", code)
			}
		})
	}
}

func TestUserSearchNormalizationAndQueryScopedPagination(t *testing.T) {
	var queries []string
	repository := &adminRepository{listUsers: func(_ context.Context, query string, cursor *domain.UserCursor, _ uint32) (domain.UserList, error) {
		queries = append(queries, query)
		if cursor == nil {
			return domain.UserList{
				Users: []domain.User{{ID: targetUserID, CreatedAt: serviceClock{}.Now()}},
				Next:  &domain.UserCursor{CreatedAt: serviceClock{}.Now(), UserID: targetUserID},
			}, nil
		}
		return domain.UserList{}, nil
	}}
	service := newTestAdminService(t, repository, &adminUploads{})
	ctx := administratorContext()
	first, err := service.ListUsers(ctx, connect.NewRequest(&devhudv1.ListUsersRequest{Query: "  E\u0301XAMPLE  "}))
	if err != nil {
		t.Fatal(err)
	}
	if queries[0] != "éxample" || first.Msg.GetNextPageToken() == "" {
		t.Fatalf("query=%q token=%q", queries[0], first.Msg.GetNextPageToken())
	}
	_, err = service.ListUsers(ctx, connect.NewRequest(&devhudv1.ListUsersRequest{
		Query: "éxample", Page: &devhudv1.PageRequest{PageToken: first.Msg.GetNextPageToken()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ListUsers(ctx, connect.NewRequest(&devhudv1.ListUsersRequest{
		Query: "different", Page: &devhudv1.PageRequest{PageToken: first.Msg.GetNextPageToken()},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("cross-query token code = %v", connect.CodeOf(err))
	}
}

func TestRejectedMutationRequiresReasonAndIsCorrelated(t *testing.T) {
	var audits []domain.AuditEvent
	repository := &adminRepository{recordAudit: func(_ context.Context, event domain.AuditEvent) error {
		audits = append(audits, event)
		return nil
	}}
	service := newTestAdminService(t, repository, &adminUploads{})
	_, err := service.SetUserBlocked(administratorContext(), connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED, Reason: "   ",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	if len(audits) != 1 || audits[0].Outcome != domain.AuditOutcomeRejected ||
		audits[0].RejectionReason != domain.AuditRejectionInvalidArgument ||
		audits[0].CorrelationID != testCorrelationID || audits[0].ID != eventID {
		t.Fatalf("audit = %+v", audits)
	}
}

func TestRejectedMutationDoesNotAuditSensitiveReason(t *testing.T) {
	var audits []domain.AuditEvent
	repository := &adminRepository{recordAudit: func(_ context.Context, event domain.AuditEvent) error {
		audits = append(audits, event)
		return nil
	}}
	service := newTestAdminService(t, repository, &adminUploads{})
	_, err := service.SetUserBlocked(administratorContext(), connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED, Reason: "access_token=must-not-be-persisted",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	if len(audits) != 1 || audits[0].Reason != "" || audits[0].Outcome != domain.AuditOutcomeRejected {
		t.Fatalf("unsafe rejected audit = %+v", audits)
	}
}

func TestFailedBlockMutationRecordsOperationFailedAudit(t *testing.T) {
	var audits []domain.AuditEvent
	repository := &adminRepository{
		setBlocked: func(context.Context, string, string, domain.AdministrativeBlockState, domain.AdministrativeBlockState, domain.AuditEvent, time.Time) (domain.User, error) {
			return domain.User{}, errors.New("database unavailable")
		},
		recordAudit: func(_ context.Context, event domain.AuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}
	service := newTestAdminService(t, repository, &adminUploads{})
	_, err := service.SetUserBlocked(administratorContext(), connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED, Reason: "Reviewed abuse report.",
	}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	if len(audits) != 1 || audits[0].Outcome != domain.AuditOutcomeRejected ||
		audits[0].RejectionReason != domain.AuditRejectionOperationFailed ||
		audits[0].CorrelationID != testCorrelationID || audits[0].TargetUserID == nil || *audits[0].TargetUserID != targetUserID {
		t.Fatalf("audit = %+v", audits)
	}
}

func TestPublicAssetReasonsAreRejectedForUserAndUploadMutations(t *testing.T) {
	var audits []domain.AuditEvent
	userMutationCalls := 0
	repository := &adminRepository{
		setBlocked: func(context.Context, string, string, domain.AdministrativeBlockState, domain.AdministrativeBlockState, domain.AuditEvent, time.Time) (domain.User, error) {
			userMutationCalls++
			return domain.User{}, errors.New("unexpected mutation")
		},
		recordAudit: func(_ context.Context, event domain.AuditEvent) error {
			audits = append(audits, event)
			return nil
		},
	}
	uploads := &adminUploads{}
	service := newTestAdminService(t, repository, uploads)

	_, userErr := service.SetUserBlocked(administratorContext(), connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED,
		Reason:      "Reviewed https://assets.example.com/uploads/018f7c1e.png",
	}))
	_, uploadErr := service.DeleteUpload(administratorContext(), connect.NewRequest(&devhudv1.AdminServiceDeleteUploadRequest{
		UploadId: uuid(uploadID), ExpectedState: devhudv1.UploadState_UPLOAD_STATE_FINALIZED,
		Reason: "Reviewed https://assets.example.com/%75ploads/018f7c1e.png",
	}))

	if connect.CodeOf(userErr) != connect.CodeInvalidArgument || connect.CodeOf(uploadErr) != connect.CodeInvalidArgument {
		t.Fatalf("codes = (%v, %v), want InvalidArgument", connect.CodeOf(userErr), connect.CodeOf(uploadErr))
	}
	if userMutationCalls != 0 || uploads.removeCalls != 0 {
		t.Fatalf("invalid reasons reached mutations: user=%d upload=%d", userMutationCalls, uploads.removeCalls)
	}
	if len(audits) != 2 {
		t.Fatalf("audits = %+v", audits)
	}
	for _, event := range audits {
		if event.Reason != "" || event.Outcome != domain.AuditOutcomeRejected || event.RejectionReason != domain.AuditRejectionInvalidArgument {
			t.Fatalf("unsafe rejected audit = %+v", event)
		}
	}
}

func TestAcceptedUploadMutationReturnsOwnerAuditTarget(t *testing.T) {
	uploads := &adminUploads{remove: func(context.Context, string, string, domain.RemovalReason, domain.UploadState, string, domain.AuditEvent) (domain.Upload, error) {
		return domain.Upload{
			UploadReservation: domain.UploadReservation{UploadID: uploadID, OwnerUserID: targetUserID},
			State:             domain.UploadStateDeleted,
		}, nil
	}}
	service := newTestAdminService(t, &adminRepository{}, uploads)
	response, err := service.DeleteUpload(administratorContext(), connect.NewRequest(&devhudv1.AdminServiceDeleteUploadRequest{
		UploadId: uuid(uploadID), ExpectedState: devhudv1.UploadState_UPLOAD_STATE_FINALIZED, Reason: "Reviewed policy violation.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Msg.GetAuditEvent().GetTargetUserId().GetValue(); got != targetUserID {
		t.Fatalf("target user ID = %q, want %q", got, targetUserID)
	}
}

func TestRejectedMutationAuditPreservesRequestDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	var auditDeadline time.Time
	repository := &adminRepository{recordAudit: func(ctx context.Context, _ domain.AuditEvent) error {
		var ok bool
		auditDeadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("rejection audit context has no deadline")
		}
		return nil
	}}
	service := newTestAdminService(t, repository, &adminUploads{})
	ctx, cancel := context.WithDeadline(administratorContext(), deadline)
	defer cancel()
	_, err := service.SetUserBlocked(ctx, connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	if !auditDeadline.Equal(deadline) {
		t.Fatalf("audit deadline = %v, want %v", auditDeadline, deadline)
	}
}

func TestDetachedAuditContextPreservesDeadlineAndIgnoresCancellation(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	requestContext, cancelRequest := context.WithDeadline(context.Background(), deadline)
	cancelRequest()
	auditContext, cancelAudit := detachedAuditContext(requestContext)
	defer cancelAudit()

	auditDeadline, ok := auditContext.Deadline()
	if !ok || !auditDeadline.Equal(deadline) {
		t.Fatalf("audit deadline = %v, ok=%v, want %v", auditDeadline, ok, deadline)
	}
	if err := auditContext.Err(); err != nil {
		t.Fatalf("detached audit context inherited request cancellation: %v", err)
	}
}

func TestRejectedInterceptorAuditUsesOneClockInstant(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 999_999_999, time.UTC)
	clock := &steppingAdminClock{now: now, step: time.Nanosecond}
	interceptor := &AuthInterceptor{clock: clock, ids: fixedAdminIDs{}}
	event, err := interceptor.rejectedAdminMutationEvent(
		WithCorrelationID(context.Background(), testCorrelationID),
		domain.AuditRejectionUnauthenticated,
	)
	if err != nil {
		t.Fatal(err)
	}
	if clock.calls != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.calls)
	}
	if !event.CreatedAt.Equal(now) || !event.ExpiresAt.Equal(now.Add(domain.AuditRetention)) {
		t.Fatalf("audit window = %v to %v", event.CreatedAt, event.ExpiresAt)
	}
}

func TestConcurrentMutationReturnsCurrentStateWithoutSettings(t *testing.T) {
	current := domain.User{
		ID: targetUserID, Subject: "subject", AdministrativeBlockState: domain.AdministrativeBlockStateBlocked,
		CreatedAt: serviceClock{}.Now(), UpdatedAt: serviceClock{}.Now(),
	}
	repository := &adminRepository{setBlocked: func(context.Context, string, string, domain.AdministrativeBlockState, domain.AdministrativeBlockState, domain.AuditEvent, time.Time) (domain.User, error) {
		return domain.User{}, &domain.AdminConflictError{User: &current}
	}}
	service := newTestAdminService(t, repository, &adminUploads{})
	_, err := service.SetUserBlocked(administratorContext(), connect.NewRequest(&devhudv1.SetUserBlockedRequest{
		UserId: uuid(targetUserID), ExpectedState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED,
		TargetState: devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED, Reason: "Reviewed abuse report.",
	}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "canonical_json") || strings.Contains(err.Error(), "settings") {
		t.Fatalf("conflict exposed settings: %v", err)
	}
}

func TestUsageIncludesBoundedGlobalAndSubmissionCounters(t *testing.T) {
	uploads := &adminUploads{usage: domain.UploadUsage{
		SignedURLsRollingHour: 3, UploadBytesRollingDay: 42, StoredBytes: 99,
		SubmissionImages: map[string]uint64{targetUserID: 2},
	}}
	service := newTestAdminService(t, &adminRepository{}, uploads)
	response, err := service.GetUserUsage(administratorContext(), connect.NewRequest(&devhudv1.GetUserUsageRequest{UserId: uuid(targetUserID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.GetCounters()) != 5 {
		t.Fatalf("counters = %+v", response.Msg.GetCounters())
	}
	for _, counter := range response.Msg.GetCounters() {
		if counter.GetLimit() == 0 {
			t.Fatalf("counter has no limit: %+v", counter)
		}
	}
}

func TestUsageRejectsMissingUser(t *testing.T) {
	service := newTestAdminService(t, &adminRepository{}, &adminUploads{usageErr: domain.ErrNotFound})
	_, err := service.GetUserUsage(administratorContext(), connect.NewRequest(&devhudv1.GetUserUsageRequest{UserId: uuid(targetUserID)}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestAdminMessagesCannotExposeSynchronizedSettingsOrObjectLocations(t *testing.T) {
	userFields := (&devhudv1.AdminUser{}).ProtoReflect().Descriptor().Fields()
	uploadFields := (&devhudv1.AdminUpload{}).ProtoReflect().Descriptor().Fields()
	for _, fields := range []struct {
		name string
		set  protoreflect.FieldDescriptors
	}{{"user", userFields}, {"upload", uploadFields}} {
		for index := 0; index < fields.set.Len(); index++ {
			name := string(fields.set.Get(index).Name())
			for _, forbidden := range []string{"canonical_json", "settings", "token", "secret", "screenshot", "dom", "issue_body", "local_path", "public_url", "signed_url", "staging_key"} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s field %q exposes forbidden data", fields.name, name)
				}
			}
		}
	}
}

func administratorContext() context.Context {
	return auth.WithPrincipal(WithCorrelationID(context.Background(), testCorrelationID), auth.Principal{
		User: domain.User{ID: adminUserID}, Roles: []string{domain.AdminRole},
	})
}

func newTestAdminService(t *testing.T, repository domain.AdminRepository, uploads domain.UploadAdministration) *AdminService {
	t.Helper()
	service, err := NewAdminService(repository, uploads, serviceClock{}, fixedAdminIDs{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), []byte("01234567890123456789012345678901"), "https://assets.example.com/uploads/")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type fixedAdminIDs struct{}

func (fixedAdminIDs) New() (string, error) { return eventID, nil }

type steppingAdminClock struct {
	now   time.Time
	step  time.Duration
	calls int
}

func (clock *steppingAdminClock) Now() time.Time {
	value := clock.now.Add(time.Duration(clock.calls) * clock.step)
	clock.calls++
	return value
}

type adminRepository struct {
	listUsers   func(context.Context, string, *domain.UserCursor, uint32) (domain.UserList, error)
	setBlocked  func(context.Context, string, string, domain.AdministrativeBlockState, domain.AdministrativeBlockState, domain.AuditEvent, time.Time) (domain.User, error)
	recordAudit func(context.Context, domain.AuditEvent) error
}

func (r *adminRepository) ListUsers(ctx context.Context, query string, cursor *domain.UserCursor, size uint32) (domain.UserList, error) {
	if r.listUsers != nil {
		return r.listUsers(ctx, query, cursor, size)
	}
	return domain.UserList{}, nil
}
func (r *adminRepository) SetUserBlocked(ctx context.Context, actor, target string, expected, state domain.AdministrativeBlockState, event domain.AuditEvent, now time.Time) (domain.User, error) {
	if r.setBlocked != nil {
		return r.setBlocked(ctx, actor, target, expected, state, event, now)
	}
	return domain.User{}, errors.New("unexpected mutation")
}
func (r *adminRepository) RecordAdministratorAudit(ctx context.Context, event domain.AuditEvent) error {
	if r.recordAudit != nil {
		return r.recordAudit(ctx, event)
	}
	return nil
}
func (*adminRepository) ListAuditEvents(context.Context, domain.AuditFilters, *domain.AuditCursor, uint32) (domain.AuditList, error) {
	return domain.AuditList{}, nil
}

type adminUploads struct {
	usage       domain.UploadUsage
	usageErr    error
	removeCalls int
	remove      func(context.Context, string, string, domain.RemovalReason, domain.UploadState, string, domain.AuditEvent) (domain.Upload, error)
}

func (*adminUploads) ListUploads(context.Context, string, domain.AdminUploadFilters, string, uint32) (domain.UploadList, string, error) {
	return domain.UploadList{}, "", nil
}
func (u *adminUploads) GetUsage(context.Context, string) (domain.UploadUsage, error) {
	return u.usage, u.usageErr
}
func (u *adminUploads) RemoveUpload(ctx context.Context, actorID, uploadID string, reason domain.RemovalReason, state domain.UploadState, rationale string, event domain.AuditEvent) (domain.Upload, error) {
	u.removeCalls++
	if u.remove != nil {
		return u.remove(ctx, actorID, uploadID, reason, state, rationale, event)
	}
	return domain.Upload{}, errors.New("unexpected mutation")
}
