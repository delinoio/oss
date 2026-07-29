package service

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
)

func TestProviderDefinitionUnavailableIsRetryable(t *testing.T) {
	t.Parallel()
	err := providerDefinitionUnavailable()
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("provider definition failure code = %v", connect.CodeOf(err))
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("provider definition failure type = %T", err)
	}
	for _, item := range connectErr.Details() {
		value, detailErr := item.Value()
		if detailErr != nil {
			t.Fatal(detailErr)
		}
		detail, ok := value.(*realqav1.ErrorDetail)
		if ok &&
			detail.Reason == realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID &&
			detail.FailureClass == realqav1.FailureClass_FAILURE_CLASS_RETRYABLE {
			return
		}
	}
	t.Fatal("provider definition failure did not include retryable schema detail")
}

func TestValidateProjectConfigurationRequiresConfiguredPermission(t *testing.T) {
	t.Parallel()
	if err := validateProjectConfiguration(
		realqagithub.ProjectPermissionNone,
		[]string{"PVT_fixture"},
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unconfigured project permission code = %v", connect.CodeOf(err))
	}
	for _, permission := range []realqagithub.ProjectPermission{
		realqagithub.ProjectPermissionRepository,
		realqagithub.ProjectPermissionOrganization,
	} {
		if err := validateProjectConfiguration(
			permission,
			[]string{"PVT_fixture"},
		); err != nil {
			t.Fatalf("configured project permission %q rejected: %v", permission, err)
		}
	}
	if err := validateProjectConfiguration(
		realqagithub.ProjectPermissionNone,
		nil,
	); err != nil {
		t.Fatalf("empty project selection rejected: %v", err)
	}
}
