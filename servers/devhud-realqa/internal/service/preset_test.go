package service

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
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
