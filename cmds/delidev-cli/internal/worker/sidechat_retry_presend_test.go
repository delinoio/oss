// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type retryPresendRPC struct {
	delidevv1connect.WorkerServiceClient
	request *pb.RegisterExecutionRequest
	result  *connect.Response[pb.RegisterExecutionResponse]
	err     error
	calls   int
	header  string
}

func (f *retryPresendRPC) RegisterExecution(_ context.Context, req *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	f.calls++
	f.header = req.Header().Get("Authorization")
	if !proto.Equal(req.Msg, f.request) {
		return nil, errors.New("pre-send request differs from original registration")
	}
	return f.result, f.err
}

type retryNativeTurn struct{ calls int }

func (f *retryNativeTurn) StartTurn(_ context.Context, _, _ domain.ID, _ domain.SessionInput) (codex.TurnResult, error) {
	f.calls++
	return codex.TurnResult{}, nil
}

func TestManagedSidechatRetryChecksOriginalRegistrationBeforeNativeInput(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  *connect.Response[pb.RegisterExecutionResponse]
		err     error
		allowed bool
	}{
		{"revoked actor", nil, connect.NewError(connect.CodePermissionDenied, nil), false},
		{"lost acknowledgment", nil, connect.NewError(connect.CodeUnavailable, nil), false},
		{"empty acknowledgment", nil, nil, false},
		{"new grant instead of replay", connect.NewResponse(&pb.RegisterExecutionResponse{}), nil, false},
		{"unexpected proxy binding", connect.NewResponse(&pb.RegisterExecutionResponse{Replayed: true, ProxyPath: "/api-proxy/v1"}), nil, false},
		{"original actor and binding", connect.NewResponse(&pb.RegisterExecutionResponse{Replayed: true}), nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := newCheckpointFixture(t).input
			input.SidechatRetry = &domain.SidechatRetryExecution{GenerationID: domain.NewID()}
			request := &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(domain.NewID()), ExpectedRevision: 7}, MachineId: string(input.MachineID), InstanceId: string(domain.NewID()), CredentialDigest: bytes.Repeat([]byte{0x42}, 32)}
			rpc := &retryPresendRPC{request: request, result: test.result, err: test.err}
			connection := &PublicationConfig{Credential: Credential{Token: "private-fixture-token"}, Client: rpc}
			native := &retryNativeTurn{}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			_, err := startCodexTurnWithRetryAuthority(context.Background(), connection, request, input, true, native, logger)
			if (err == nil) != test.allowed || native.calls != map[bool]int{true: 1, false: 0}[test.allowed] || rpc.calls != 1 || rpc.header != "Bearer private-fixture-token" {
				t.Fatalf("invalid pre-send boundary: err=%v native=%d registration=%d", err, native.calls, rpc.calls)
			}
			if strings.Contains(logs.String(), input.Input.Prompt) || strings.Contains(logs.String(), connection.Credential.Token) {
				t.Fatal("pre-send logs exposed native input or credential")
			}
		})
	}
}

func TestOtherCodexTurnsDoNotReplayRegistration(t *testing.T) {
	input := newCheckpointFixture(t).input
	request := &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}}
	for _, managed := range []bool{false, true} {
		rpc := &retryPresendRPC{request: request}
		native := &retryNativeTurn{}
		logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
		if _, err := startCodexTurnWithRetryAuthority(context.Background(), &PublicationConfig{Client: rpc}, request, input, managed, native, logger); err != nil || rpc.calls != 0 || native.calls != 1 {
			t.Fatal("ordinary native turn changed registration behavior", err)
		}
	}
}
