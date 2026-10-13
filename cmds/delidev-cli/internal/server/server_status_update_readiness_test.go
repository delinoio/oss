// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// TestGetStatusReportsProductionRootReadiness verifies that GetStatus exposes the
// single updates.ProductionReady validator result with explicit presence while
// the signed-update protocol capability stays advertised.
func TestGetStatusReportsProductionRootReadiness(t *testing.T) {
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
	if response.Msg.ProductionRootReady == nil {
		t.Fatal("GetStatus omitted production root readiness presence")
	}
	if got, want := response.Msg.GetProductionRootReady(), updates.ProductionReady(); got != want {
		t.Fatalf("production root readiness = %v, want validator result %v", got, want)
	}
	if response.Msg.GetProductionRootReady() {
		t.Fatal("repository trust root must not report production readiness")
	}
	if !slices.Contains(response.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1) {
		t.Fatal("signed-update protocol capability must remain advertised independently of root readiness")
	}
}
