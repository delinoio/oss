package service

import (
	"errors"
	"strings"
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
	for _, projectNodeID := range []string{"PVT fixture", "PVT:fixture"} {
		if err := validateProjectConfiguration(
			realqagithub.ProjectPermissionOrganization,
			[]string{projectNodeID},
		); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid project node ID %q code = %v",
				projectNodeID, connect.CodeOf(err))
		}
	}
}

func TestValidatePresetProviderMetadataUsesGitHubRules(t *testing.T) {
	t.Parallel()
	if err := validatePresetProviderMetadata(
		[]string{"bug"}, []string{"octocat"},
	); err != nil {
		t.Fatalf("valid provider metadata rejected: %v", err)
	}
	for _, test := range []struct {
		name      string
		labels    []string
		assignees []string
	}{
		{name: "label", labels: []string{strings.Repeat("界", 51)}},
		{name: "assignee", assignees: []string{"octo..cat"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validatePresetProviderMetadata(test.labels, test.assignees)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid metadata code = %v", connect.CodeOf(err))
			}
		})
	}
}
