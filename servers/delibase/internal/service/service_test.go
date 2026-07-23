package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
)

func TestFoundationBusinessMethodsAreTypedUnimplemented(t *testing.T) {
	t.Parallel()
	response, err := NewAccount(Dependencies{}).GetAccountState(
		context.Background(),
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if response != nil {
		t.Fatalf("response = %#v, want nil", response)
	}
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) {
		t.Fatalf("error = %T, want *connect.Error", err)
	}
	if connectFailure.Code() != connect.CodeUnimplemented {
		t.Fatalf("code = %s, want %s", connectFailure.Code(), connect.CodeUnimplemented)
	}
}
