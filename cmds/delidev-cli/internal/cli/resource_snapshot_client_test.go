// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type largeSnapshotStub struct {
	delidevv1connect.UnimplementedResourceServiceHandler
	response *pb.GetSnapshotResponse
}

func (s largeSnapshotStub) GetSnapshot(context.Context, *connect.Request[pb.GetSnapshotRequest]) (*connect.Response[pb.GetSnapshotResponse], error) {
	return connect.NewResponse(s.response), nil
}

// The CLI reads snapshots through the production client constructor, so its
// resource client must accept the 8 MiB typed-Job snapshot ceiling.
func TestCLIResourceClientReadsTypedLargeJobSnapshots(t *testing.T) {
	// Two Jobs make the binary response exceed the 5 MiB ceiling of other RPCs.
	first := bytes.Repeat([]byte("x"), domain.MaxLargeJobDocumentBytes)
	second := bytes.Repeat([]byte("y"), 3<<20)
	job := func(document []byte) *pb.Resource {
		return &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, DocumentJson: document}
	}
	stub := largeSnapshotStub{response: &pb.GetSnapshotResponse{Resources: []*pb.Resource{job(first), job(second)}, Cursor: "cursor"}}
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.NewResourceServiceHandler(stub, connect.WithSendMaxBytes(domain.MaxLargeJobSnapshotBytes)))
	host := httptest.NewServer(mux)
	defer host.Close()
	c, err := connectClient(options{server: host.URL, dataDir: filepath.Join(t.TempDir(), "client"), tokenStdin: true}, strings.NewReader("isolated-fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := c.resources.GetSnapshot(context.Background(), request(c, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_JOB}}))
	if err != nil || len(response.Msg.Resources) != 2 || !bytes.Equal(response.Msg.Resources[0].DocumentJson, first) || response.Msg.Cursor != "cursor" {
		t.Fatal("CLI resource client rejected a typed large Job snapshot", err)
	}
}
