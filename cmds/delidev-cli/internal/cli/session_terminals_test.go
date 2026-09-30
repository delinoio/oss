// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalCLIFixture struct {
	delidevv1connect.UnimplementedTerminalServiceHandler
	sync.Mutex
	input   *pb.ControlTerminalRequest
	cursor  *pb.WatchTerminalOutputRequest
	timeout string
}

func (f *terminalCLIFixture) ControlTerminal(_ context.Context, req *connect.Request[pb.ControlTerminalRequest]) (*connect.Response[pb.ControlTerminalResponse], error) {
	f.Lock()
	defer f.Unlock()
	f.input = req.Msg
	return connect.NewResponse(&pb.ControlTerminalResponse{Replayed: true}), nil
}
func (f *terminalCLIFixture) WatchTerminalOutput(_ context.Context, req *connect.Request[pb.WatchTerminalOutputRequest], stream *connect.ServerStream[pb.WatchTerminalOutputResponse]) error {
	f.Lock()
	defer f.Unlock()
	f.cursor = req.Msg
	f.timeout = req.Header().Get("Connect-Timeout-Ms")
	return stream.Send(&pb.WatchTerminalOutputResponse{Epoch: req.Msg.Epoch, Sequence: req.Msg.AfterSequence, Data: []byte{0xe2, 0x82}, Gap: true})
}

func TestTerminalCLIFollowPreservesCallerLifetime(t *testing.T) {
	for _, action := range []string{"output", "reattach"} {
		for _, test := range []struct {
			name      string
			flags     []string
			parent    bool
			unbounded bool
		}{
			{name: "observation"},
			{name: "follow", flags: []string{"--follow"}, unbounded: true},
			{name: "explicit-true", flags: []string{"--follow=true"}, unbounded: true},
			{name: "explicit-false", flags: []string{"--follow=false"}},
			{name: "last-value", flags: []string{"--follow", "--follow=false"}},
			{name: "parent-deadline", flags: []string{"--follow"}, parent: true},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				fixture := &terminalCLIFixture{}
				_, handler := delidevv1connect.NewTerminalServiceHandler(fixture)
				endpoint := httptest.NewServer(handler)
				defer endpoint.Close()
				ctx := context.Background()
				if test.parent {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
				}
				args := append([]string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", endpoint.URL, "--token-stdin", "session", "terminal", action, "--id", string(domain.NewID())}, test.flags...)
				var out, diagnostic bytes.Buffer
				if code := Run(ctx, args, IO{In: bytes.NewBufferString("fixture-only"), Out: &out, Err: &diagnostic}); code != 0 {
					t.Fatal("terminal observation failed", code, out.String(), diagnostic.String())
				}
				fixture.Lock()
				defer fixture.Unlock()
				if fixture.cursor == nil {
					t.Fatal("no actual Connect output request")
				}
				if test.unbounded {
					if fixture.timeout != "" {
						t.Fatal("follow acquired an ordinary command deadline", fixture.timeout)
					}
				} else {
					milliseconds, err := strconv.Atoi(fixture.timeout)
					limit := 30000
					if test.parent {
						limit = 5000
					}
					if err != nil || milliseconds <= 0 || milliseconds > limit {
						t.Fatal("observation lost its command or caller deadline", fixture.timeout, err)
					}
				}
			})
		}
	}
}

func TestTerminalCLIBytesReceiptsAndExactCursor(t *testing.T) {
	fixture := &terminalCLIFixture{}
	_, handler := delidevv1connect.NewTerminalServiceHandler(fixture)
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()
	c := client{terminals: delidevv1connect.NewTerminalServiceClient(http.DefaultClient, endpoint.URL), token: "fixture-only"}
	id, requestID, epoch := domain.NewID(), domain.NewID(), domain.NewID()
	raw := []byte{0, 3, 0xff, 0xe2, 0x82}
	result, err := sessionTerminalCommand(context.Background(), c, options{requestID: requestID}, []string{"input", "--id", string(id), "--revision", "7"}, IO{In: bytes.NewReader(raw), Out: io.Discard})
	fixture.Lock()
	if err != nil || !bytes.Equal(fixture.input.Input, raw) || fixture.input.Mutation.RequestId != string(requestID) || fixture.input.Mutation.ExpectedRevision != 7 || result.(map[string]any)["replayed"] != true {
		t.Fatal("CLI changed native input or request identity", err)
	}
	fixture.Unlock()
	result, err = sessionTerminalCommand(context.Background(), c, options{}, []string{"reattach", "--id", string(id), "--epoch", string(epoch), "--after", "18446744073709551614"}, IO{Out: io.Discard})
	fixture.Lock()
	defer fixture.Unlock()
	if err != nil || fixture.cursor.TerminalId != string(id) || fixture.cursor.AfterSequence != ^uint64(0)-1 || result.(map[string]any)["sequence"] != "18446744073709551614" || !bytes.Equal(result.(map[string]any)["data"].([]byte), []byte{0xe2, 0x82}) {
		t.Fatal("CLI rounded the cursor or decoded partial bytes", err)
	}
}
