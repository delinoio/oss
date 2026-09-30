// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestStoppedAccountSwitchStatusPreservesExistingCapabilities(t *testing.T) {
	endpoint, identity, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	response, err := client.GetStatus(context.Background(), ownerRequest(identity, &pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []pb.SystemCapability{
		pb.SystemCapability_SYSTEM_CAPABILITY_AUTOMATIC_TITLES_V1,
		pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1,
		pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1,
		pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1,
	} {
		if !slices.Contains(response.Msg.Capabilities, capability) {
			t.Fatalf("missing capability %v", capability)
		}
	}
	if int32(pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1) != 5 {
		t.Fatal("account switching reused another allocated capability")
	}
}
