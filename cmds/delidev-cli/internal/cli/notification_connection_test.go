// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type notificationStatusFixture struct {
	delidevv1connect.UnimplementedSystemServiceHandler
	code   connect.Code
	status *pb.GetStatusResponse
}

func (f notificationStatusFixture) GetStatus(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	if f.code != 0 {
		return nil, connect.NewError(f.code, errors.New("private native diagnostic"))
	}
	return connect.NewResponse(f.status), nil
}

type notificationDoer func(*http.Request) (*http.Response, error)

func (f notificationDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestNotificationConnectionCountsOnlyClientGeneratedTransportFailures(t *testing.T) {
	for _, test := range []struct {
		code  connect.Code
		state string
	}{{connect.CodeUnavailable, "network-unavailable"}, {connect.CodeDeadlineExceeded, "network-deadline"}, {connect.CodeUnauthenticated, "unusable"}, {connect.CodePermissionDenied, "unusable"}} {
		native := notificationDoer(func(*http.Request) (*http.Response, error) {
			return nil, connect.NewError(test.code, errors.New("private native diagnostic"))
		})
		c := client{system: delidevv1connect.NewSystemServiceClient(native, "https://fixture.invalid")}
		value, err := observeNotificationConnection(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if state := value.(map[string]any); state["state"] != test.state || len(state) != 1 {
			t.Fatal("raw transport classification", state)
		}
	}
	for _, code := range []connect.Code{connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeFailedPrecondition} {
		_, handler := delidevv1connect.NewSystemServiceHandler(notificationStatusFixture{code: code})
		server := httptest.NewServer(handler)
		c := client{system: delidevv1connect.NewSystemServiceClient(server.Client(), server.URL)}
		value, err := observeNotificationConnection(context.Background(), c)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if state := value.(map[string]any); state["state"] != "unusable" || len(state) != 1 {
			t.Fatal("wire error became transport edge", state)
		}
	}
}
func TestNotificationConnectionOriginalSuccessSurvivesRawPreferenceRefreshLoss(t *testing.T) {
	id := domain.NewID()
	_, handler := delidevv1connect.NewSystemServiceHandler(notificationStatusFixture{status: &pb.GetStatusResponse{ServerId: string(id), ProtocolVersion: rpc.ProtocolVersion, Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SITUATION_NOTIFICATIONS_V1}}})
	server := httptest.NewServer(handler)
	defer server.Close()
	native := notificationDoer(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/GetNotificationPreferences") {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("private native diagnostic"))
		}
		return server.Client().Do(r)
	})
	c := client{system: delidevv1connect.NewSystemServiceClient(native, server.URL), inbox: delidevv1connect.NewInboxServiceClient(native, server.URL)}
	value, err := observeNotificationConnection(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(map[string]any)
	if state["state"] != "authenticated-success" || state["server_id"] != string(id) || len(state) != 2 {
		t.Fatal("successful original status lost or fabricated cache", state)
	}
}
