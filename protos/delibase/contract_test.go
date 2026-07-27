package delibase_test

import (
	"slices"
	"testing"

	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCanonicalServices(t *testing.T) {
	t.Parallel()

	files := []protoreflect.FileDescriptor{
		delibasev1.File_delibase_v1_account_proto,
		delibasev1.File_delibase_v1_billing_proto,
		delibasev1.File_delibase_v1_catalog_proto,
		delibasev1.File_delibase_v1_common_proto,
		delibasev1.File_delibase_v1_organization_proto,
		delibasev1.File_delibase_v1_team_proto,
		delibasev1.File_delibase_v1_usage_proto,
	}
	var got []string
	for _, file := range files {
		services := file.Services()
		for index := range services.Len() {
			got = append(got, string(services.Get(index).FullName()))
		}
	}
	slices.Sort(got)
	want := []string{
		"delibase.v1.AccountService",
		"delibase.v1.BillingService",
		"delibase.v1.CatalogService",
		"delibase.v1.OrganizationService",
		"delibase.v1.TeamService",
		"delibase.v1.UsageService",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("canonical services = %v, want %v", got, want)
	}
}

func TestCanonicalScalarWrappers(t *testing.T) {
	t.Parallel()

	messages := delibasev1.File_delibase_v1_common_proto.Messages()
	tests := []struct {
		message protoreflect.Name
		kind    protoreflect.Kind
	}{
		{message: "UuidV7", kind: protoreflect.StringKind},
		{message: "UsdMicros", kind: protoreflect.Int64Kind},
		{message: "UsageUnits", kind: protoreflect.Int64Kind},
	}
	for _, test := range tests {
		descriptor := messages.ByName(test.message)
		if descriptor == nil {
			t.Fatalf("missing %s wrapper", test.message)
		}
		if got := descriptor.Fields().ByName("value").Kind(); got != test.kind {
			t.Errorf("%s.value kind = %s, want %s", test.message, got, test.kind)
		}
	}
}

func TestInvitationIdempotencyOperations(t *testing.T) {
	t.Parallel()

	operations := []delibasev1.IdempotentOperation{
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_ACCEPT_INVITATION,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_INVITATION,
	}
	seen := make(map[delibasev1.IdempotentOperation]struct{}, len(operations))
	for _, operation := range operations {
		if operation == delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UNSPECIFIED {
			t.Fatal("an invitation idempotency operation resolved to unspecified")
		}
		if _, duplicate := seen[operation]; duplicate {
			t.Fatalf("duplicate invitation idempotency operation %d", operation)
		}
		seen[operation] = struct{}{}
	}
}

func TestBackgroundUsageContract(t *testing.T) {
	t.Parallel()

	billingMethods := methodNames(
		delibasev1.File_delibase_v1_billing_proto.
			Services().
			ByName("BillingService").
			Methods(),
	)
	if len(billingMethods) != 10 {
		t.Errorf("BillingService methods = %v", billingMethods)
	}
	for _, method := range []string{
		"CreateBackgroundUsageAuthorization",
		"GetBackgroundUsageAuthorization",
		"ListBackgroundUsageAuthorizations",
		"RevokeBackgroundUsageAuthorization",
	} {
		if !slices.Contains(billingMethods, method) {
			t.Errorf("BillingService missing %s", method)
		}
	}

	usageMethods := methodNames(
		delibasev1.File_delibase_v1_usage_proto.
			Services().
			ByName("UsageService").
			Methods(),
	)
	if len(usageMethods) != 7 {
		t.Errorf("UsageService methods = %v", usageMethods)
	}
	for _, method := range []string{
		"ReserveAuthorizedUsage",
		"CommitAuthorizedUsage",
		"ReleaseAuthorizedUsage",
		"MarkBackgroundUsageResourceDeleted",
	} {
		if !slices.Contains(usageMethods, method) {
			t.Errorf("UsageService missing %s", method)
		}
	}

	if got := delibasev1.BackgroundUsagePurpose_name; len(got) != 2 ||
		got[0] != "BACKGROUND_USAGE_PURPOSE_UNSPECIFIED" ||
		got[1] != "BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE" {
		t.Errorf("background usage purposes = %v", got)
	}

	requiredStatuses := []delibasev1.BackgroundUsageAuthorizationStatus{
		delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE,
		delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED,
		delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST,
		delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED,
		delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_OWNER_DELETED,
	}
	if len(delibasev1.BackgroundUsageAuthorizationStatus_name) != len(requiredStatuses)+1 {
		t.Errorf(
			"background usage authorization statuses = %v",
			delibasev1.BackgroundUsageAuthorizationStatus_name,
		)
	}
	for _, status := range requiredStatuses {
		if status == delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_UNSPECIFIED {
			t.Fatal("a required authorization status resolved to unspecified")
		}
	}
	if got := delibasev1.BackgroundUsagePeriod_name; len(got) != 2 ||
		got[1] != "BACKGROUND_USAGE_PERIOD_UTC_DAY" {
		t.Errorf("background usage periods = %v", got)
	}

	for _, operation := range []delibasev1.IdempotentOperation{
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BACKGROUND_USAGE_AUTHORIZATION,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_BACKGROUND_USAGE_AUTHORIZATION,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_AUTHORIZED_USAGE,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_AUTHORIZED_USAGE,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_AUTHORIZED_USAGE,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MARK_BACKGROUND_USAGE_RESOURCE_DELETED,
	} {
		if operation == delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UNSPECIFIED {
			t.Fatal("a background usage idempotency operation resolved to unspecified")
		}
	}
}

func TestBackgroundUsageAuthorizationShape(t *testing.T) {
	t.Parallel()

	authorization := delibasev1.File_delibase_v1_billing_proto.
		Messages().
		ByName("BackgroundUsageAuthorization")
	if authorization == nil {
		t.Fatal("missing BackgroundUsageAuthorization")
	}
	for _, field := range []protoreflect.Name{
		"authorization_id",
		"authorizer_account_id",
		"owner",
		"organization_id",
		"team_id",
		"service_identity_id",
		"meter_id",
		"purpose",
		"feature_resource_id",
		"period",
		"maximum_units",
		"status",
		"revision",
		"created_at",
		"updated_at",
		"revoked_at",
	} {
		if authorization.Fields().ByName(field) == nil {
			t.Errorf("BackgroundUsageAuthorization missing %s", field)
		}
	}

	owner := delibasev1.File_delibase_v1_common_proto.
		Messages().
		ByName("BackgroundUsageOwner")
	if owner == nil || owner.Oneofs().Len() != 1 {
		t.Fatalf("BackgroundUsageOwner descriptor = %v", owner)
	}
	ownerFields := owner.Oneofs().Get(0).Fields()
	if ownerFields.Len() != 2 ||
		ownerFields.ByName("personal_account_id") == nil ||
		ownerFields.ByName("organization_id") == nil {
		t.Errorf("BackgroundUsageOwner fields = %v", ownerFields)
	}

	listRequest := delibasev1.File_delibase_v1_billing_proto.
		Messages().
		ByName("ListBackgroundUsageAuthorizationsRequest")
	listResponse := delibasev1.File_delibase_v1_billing_proto.
		Messages().
		ByName("ListBackgroundUsageAuthorizationsResponse")
	if listRequest.Fields().ByName("page") == nil ||
		listResponse.Fields().ByName("page") == nil {
		t.Error("background usage authorization list is missing opaque pagination")
	}

	catalogMeter := delibasev1.File_delibase_v1_catalog_proto.
		Messages().
		ByName("CatalogMeter")
	if catalogMeter.Fields().ByName("authorization_targets") == nil {
		t.Error("CatalogMeter is missing public background-authorization targets")
	}

	for _, messageName := range []protoreflect.Name{
		"CommitAuthorizedUsageRequest",
		"ReleaseAuthorizedUsageRequest",
	} {
		request := delibasev1.File_delibase_v1_usage_proto.Messages().ByName(messageName)
		if request.Fields().ByName("reservation_id") == nil {
			t.Errorf("%s is missing reservation_id", messageName)
		}
	}
}

func methodNames(methods protoreflect.MethodDescriptors) []string {
	names := make([]string, 0, methods.Len())
	for index := range methods.Len() {
		names = append(names, string(methods.Get(index).Name()))
	}
	return names
}

func TestStableErrorCategories(t *testing.T) {
	t.Parallel()

	required := []delibasev1.ErrorReason{
		delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
		delibasev1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
		delibasev1.ErrorReason_ERROR_REASON_SLUG_CONFLICT,
		delibasev1.ErrorReason_ERROR_REASON_MEMBER_HAS_ACTIVE_RESERVATIONS,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_EXPIRED,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_DEPTH_EXCEEDED,
		delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
		delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED,
		delibasev1.ErrorReason_ERROR_REASON_ACCOUNT_DELETION_BLOCKED,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	}
	seen := make(map[delibasev1.ErrorReason]struct{}, len(required))
	for _, reason := range required {
		if reason == delibasev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			t.Fatal("a required error category resolved to unspecified")
		}
		if _, duplicate := seen[reason]; duplicate {
			t.Fatalf("duplicate stable error value %d", reason)
		}
		seen[reason] = struct{}{}
	}
}
