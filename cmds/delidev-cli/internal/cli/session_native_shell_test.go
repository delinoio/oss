// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type nativeShellCLIClient struct {
	delidevv1connect.SessionServiceClient
	runs    []*pb.RunNativeShellRequest
	cancels []*pb.CancelNativeShellRequest
	reads   []*pb.GetNativeShellRequest
	runErr  error
}

func (c *nativeShellCLIClient) RunNativeShell(_ context.Context, r *connect.Request[pb.RunNativeShellRequest]) (*connect.Response[pb.RunNativeShellResponse], error) {
	c.runs = append(c.runs, r.Msg)
	if c.runErr != nil {
		return nil, c.runErr
	}
	return connect.NewResponse(&pb.RunNativeShellResponse{RequestId: r.Msg.Mutation.RequestId, Replayed: true}), nil
}
func (c *nativeShellCLIClient) CancelNativeShell(_ context.Context, r *connect.Request[pb.CancelNativeShellRequest]) (*connect.Response[pb.CancelNativeShellResponse], error) {
	c.cancels = append(c.cancels, r.Msg)
	return connect.NewResponse(&pb.CancelNativeShellResponse{RequestId: r.Msg.Mutation.RequestId}), nil
}
func (c *nativeShellCLIClient) GetNativeShell(_ context.Context, r *connect.Request[pb.GetNativeShellRequest]) (*connect.Response[pb.GetNativeShellResponse], error) {
	c.reads = append(c.reads, r.Msg)
	return connect.NewResponse(&pb.GetNativeShellResponse{}), nil
}

func TestNativeShellCLIRequiresExplicitOriginalAuthorization(t *testing.T) {
	ctx := context.Background()
	session, requestID := domain.NewID(), domain.NewID()
	command := "printf '%s\\n' 'original command'"
	for _, timeout := range []string{"", "0", "3600000"} {
		remote := &nativeShellCLIClient{}
		args := []string{"shell", "--id", string(session), "--revision", "17", "--command", command, "--confirm-full-access"}
		if timeout != "" {
			args = append(args, "--timeout-ms", timeout)
		}
		result, err := sessionCommand(ctx, client{sessions: remote}, options{requestID: requestID}, args, IO{})
		if err != nil || len(remote.runs) != 1 {
			t.Fatal("explicit native shell action was not sent once", err)
		}
		r := remote.runs[0]
		if r.Mutation.Id != string(session) || r.Mutation.ExpectedRevision != 17 || r.Mutation.RequestId != string(requestID) || r.Command != command || !r.FullAccessConfirmed {
			t.Fatal("native shell changed the original command or authorization")
		}
		if timeout == "" && r.TimeoutMs != nil || timeout != "" && r.TimeoutMs == nil || timeout == "0" && *r.TimeoutMs != 0 || timeout == "3600000" && *r.TimeoutMs != 3600000 {
			t.Fatal("native shell changed omitted or explicit native timeout")
		}
		if result.(map[string]any)["request_id"] != string(requestID) || result.(map[string]any)["replayed"] != true {
			t.Fatal("native shell discarded original receipt identity")
		}
	}
	base := []string{"--id", string(session), "--revision", "17", "--command", command}
	for _, args := range [][]string{
		base,
		append(append([]string{}, base...), "--confirm-full-access=false"),
		{"--id", "foreign", "--revision", "17", "--command", command, "--confirm-full-access"},
		{"--id", string(session), "--command", command, "--confirm-full-access"},
		{"--id", string(session), "--revision", "17", "--confirm-full-access"},
		{"--id", string(session), "--revision", "17", "--command", "bad\x00command", "--confirm-full-access"},
		{"--id", string(session), "--revision", "17", "--command", strings.Repeat("x", (64<<10)+1), "--confirm-full-access"},
		append(append([]string{}, base...), "--confirm-full-access", "--timeout-ms", "3600001"),
		append(append([]string{}, base...), "--confirm-full-access", "--retry"),
	} {
		remote := &nativeShellCLIClient{}
		if _, err := sessionNativeShellCommand(ctx, client{sessions: remote}, options{requestID: requestID}, "shell", args); err == nil || len(remote.runs) != 0 {
			t.Fatal("invalid or unconfirmed shell action reached native execution")
		}
	}
}

func TestNativeShellCLIObservesAndCancelsOnlyOriginalJob(t *testing.T) {
	remote := &nativeShellCLIClient{}
	c := client{sessions: remote}
	job, requestID := domain.NewID(), domain.NewID()
	o := options{requestID: requestID}
	if _, err := sessionCommand(context.Background(), c, o, []string{"shell-status", "--job-id", string(job)}, IO{}); err != nil {
		t.Fatal(err)
	}
	if len(remote.reads) != 1 || remote.reads[0].JobId != string(job) || len(remote.runs) != 0 {
		t.Fatal("shell status acquired execution authority")
	}
	if _, err := sessionCommand(context.Background(), c, o, []string{"shell-cancel", "--job-id", string(job), "--revision", "9"}, IO{}); err != nil {
		t.Fatal(err)
	}
	if len(remote.cancels) != 1 || remote.cancels[0].Mutation.Id != string(job) || remote.cancels[0].Mutation.ExpectedRevision != 9 || remote.cancels[0].Mutation.RequestId != string(requestID) || len(remote.runs) != 0 {
		t.Fatal("shell cancellation changed original operation ownership")
	}
	for _, args := range [][]string{{"shell-cancel", "--job-id", string(job)}, {"shell-status", "--job-id", string(job), "--command", "replacement"}} {
		if _, err := sessionCommand(context.Background(), c, o, args, IO{}); err == nil {
			t.Fatal("invalid original-job operation accepted")
		}
	}
	if len(remote.reads) != 1 || len(remote.cancels) != 1 || len(remote.runs) != 0 {
		t.Fatal("invalid original-job request issued another RPC")
	}
}

func TestNativeShellCLIUncertainDeliveryDoesNotResend(t *testing.T) {
	remote := &nativeShellCLIClient{runErr: connect.NewError(connect.CodeUnavailable, errors.New("acknowledgment unavailable"))}
	_, err := sessionNativeShellCommand(context.Background(), client{sessions: remote}, options{requestID: domain.NewID()}, "shell", []string{"--id", string(domain.NewID()), "--revision", "1", "--command", "printf done", "--confirm-full-access"})
	if err == nil || len(remote.runs) != 1 || len(remote.reads) != 0 || len(remote.cancels) != 0 {
		t.Fatal("uncertain delivery was retried or adopted another operation")
	}
}

func TestNativeShellCLIFailureRetainsRequestIdentity(t *testing.T) {
	remote := &nativeShellCLIClient{runErr: connect.NewError(connect.CodeUnavailable, errors.New("acknowledgment unavailable"))}
	requestID := domain.NewID()
	var output bytes.Buffer
	code, handled := dispatchSession(context.Background(), client{sessions: remote}, options{requestID: requestID}, []string{"shell", "--id", string(domain.NewID()), "--revision", "1", "--command", "printf done", "--confirm-full-access"}, IO{Out: &output})
	var result envelope
	if !handled || code == 0 || json.Unmarshal(output.Bytes(), &result) != nil || result.RequestID != requestID || result.Error == nil || len(remote.runs) != 1 {
		t.Fatal("uncertain native shell output discarded its original request identity")
	}
}
