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
