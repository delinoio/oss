package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestCLIRepositoryAccessIsScopedReadOnlyObservation(t *testing.T) {
	selected := domain.NewID()
	for _, malformed := range []string{"", "foreign", "version", "incomplete"} {
		t.Run(malformed, func(t *testing.T) {
			value := domain.RepositoryIntegrationAccess{RepositoryID: selected, RepositoryRevision: "9007199254740993", ProfileID: domain.NewID(), GenerationID: domain.NewID(), ObservedAt: time.Now().UTC()}
			for _, feature := range domain.IntegrationFeatures() {
				value.Features = append(value.Features, domain.IntegrationFeatureAccess{Feature: feature, State: domain.IntegrationAccessNotEvaluated, Problem: domain.Fail(domain.Unavailable, "Not evaluated.", "Configure access.")})
			}
			version := uint32(1)
			switch malformed {
			case "foreign":
				value.RepositoryID = domain.NewID()
			case "version":
				version = 2
			case "incomplete":
				value.Features = value.Features[:1]
			}
			calls := 0
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.IntegrationServiceInspectRepositoryIntegrationProcedure, func(_ context.Context, r *connect.Request[pb.InspectRepositoryIntegrationRequest]) (*connect.Response[pb.InspectRepositoryIntegrationResponse], error) {
				calls++
				if r.Msg.RepositoryId != string(selected) {
					t.Error("lost selected repository")
				}
				raw, _ := json.Marshal(value)
				return connect.NewResponse(&pb.InspectRepositoryIntegrationResponse{SchemaVersion: version, DocumentJson: raw}), nil
			}))
			defer peer.Close()
			var output, diagnostic strings.Builder
			code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "integration", "inspect-repository", "--repository-id", string(selected)}, IO{In: strings.NewReader("private-fixture-auth"), Out: &output, Err: &diagnostic})
			if calls != 1 || (code == 0) != (malformed == "") {
				t.Fatalf("read result: %d %s calls=%d", code, output.String(), calls)
			}
			if malformed == "" && !strings.Contains(output.String(), `"repository_revision":"9007199254740993"`) {
				t.Fatal("rounded repository revision")
			}
			if strings.Contains(output.String(), "private-fixture-auth") {
				t.Fatal("secret leaked")
			}
		})
	}
}
