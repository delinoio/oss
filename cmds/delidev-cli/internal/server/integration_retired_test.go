package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func assertRetiredInspection(t *testing.T, reply *connect.Response[pb.InspectRepositoryIntegrationResponse], err error) {
	t.Helper()
	problem := rpc.ClientError(err)
	if reply != nil || connect.CodeOf(err) != connect.CodeUnimplemented || problem.Code != domain.Unsupported || problem.Message != "Standalone GitHub repository access inspection has been retired." || problem.Guidance != "Use GitHub item operations; each operation validates its own access." || problem.CorrelationID == "" {
		t.Fatalf("unexpected retirement response: %v %v", reply, err)
	}
}

func TestRetiredRepositoryInspectionNeedsNoProductState(t *testing.T) {
	// Nil product dependencies make any repository transaction, credential access,
	// admission or outbound operation fail instead of silently satisfying this test.
	service := &Service{}
	for _, actor := range []domain.DeviceType{domain.OwnerDevice, domain.ClientDevice} {
		ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: actor})
		for _, id := range []string{"", "invalid", string(domain.NewID())} {
			request := connect.NewRequest(&pb.InspectRepositoryIntegrationRequest{RepositoryId: id})
			request.Header().Set(rpc.CorrelationHeader, "retirement-fixture")
			response, err := service.InspectRepositoryIntegration(ctx, request)
			assertRetiredInspection(t, response, err)
			if service.integrationChecks != nil || service.integrationPreviews != nil || service.integrationSecrets != nil {
				t.Fatal("retired endpoint admitted work")
			}
		}
	}
}

func TestRetiredRepositoryInspectionAuthenticationAndDeviceAuthority(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.url)
	pair := func(kind pb.DeviceType) (security.Identity, *pb.Resource) {
		code, token := randomCode(), randomCode()
		codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
		grant, err := devices.CreatePairing(ctx, ownerRequest(f.service.Identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "Retirement fixture", Type: kind, CodeDigest: codeHash[:]}))
		if err != nil {
			t.Fatal(err)
		}
		request := &pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]}
		if kind == pb.DeviceType_DEVICE_TYPE_WORKER {
			request.MachineId = string(domain.NewID())
			request.MachineJson, _ = json.Marshal(domain.Machine{Name: "Fixture Worker", OS: "linux", Architecture: "amd64", Version: rpc.Version})
		}
		paired, err := devices.PairDevice(ctx, connect.NewRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		return security.Identity{Token: token}, paired.Msg.Device
	}
	client, device := pair(pb.DeviceType_DEVICE_TYPE_CLIENT)
	worker, _ := pair(pb.DeviceType_DEVICE_TYPE_WORKER)
	request := &pb.InspectRepositoryIntegrationRequest{RepositoryId: string(domain.NewID())}
	for _, identity := range []security.Identity{f.service.Identity, client} {
		response, err := f.client.InspectRepositoryIntegration(ctx, ownerRequest(identity, request))
		assertRetiredInspection(t, response, err)
	}
	for _, identity := range []security.Identity{{}, {Token: "invalid-fixture-token"}} {
		_, err := f.client.InspectRepositoryIntegration(ctx, ownerRequest(identity, request))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("authentication changed", err)
		}
	}
	_, err := f.client.InspectRepositoryIntegration(ctx, ownerRequest(worker, request))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker admitted", err)
	}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: profileMutation(device)})); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.InspectRepositoryIntegration(ctx, ownerRequest(client, request))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client admitted", err)
	}
	puts, deletes, values := f.native.counts()
	if puts != 0 || deletes != 0 || values != 0 || f.calls.Load() != 0 || len(f.service.integrationChecks) != 0 || len(f.service.integrationPreviews) != 0 {
		t.Fatal("retired endpoint performed credential or outbound work")
	}
}
