package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNetworkProfilesReceiptsPinnedGenerationsAndDeletionRecovery(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	c := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
	ctx := context.Background()
	definition := domain.ProxyDefinition{Name: "Corporate", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}
	raw, _ := json.Marshal(definition)
	requestID := domain.NewID()
	save := func(m *pb.Mutation, secret string) (*connect.Response[pb.SaveNetworkProfileResponse], error) {
		return c.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, &pb.SaveNetworkProfileRequest{Mutation: m, SchemaVersion: 1, DocumentJson: raw, CredentialJson: []byte(secret)}))
	}
	m := &pb.Mutation{RequestId: string(requestID)}
	created, err := save(m, outboundtest.Credential)
	if err != nil {
		t.Fatal(err)
	}
	profile := created.Msg.Resource
	if profile.Id != string(requestID) || profile.Revision != 1 || bytes.Contains(profile.DocumentJson, []byte(outboundtest.Password)) || bytes.Contains(profile.DocumentJson, []byte(outboundtest.Username)) {
		t.Fatal("public credential or wrong identity", profile)
	}
	replay, err := save(m, outboundtest.Credential)
	if err != nil || !replay.Msg.Replayed || vault.puts != 1 {
		t.Fatal("native save replay", err, vault.puts)
	}
	if _, err := save(m, `{"username":"other","password":"other"}`); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("altered receipt", err)
	}
	machine := domain.NewID()
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.machine", machine, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Worker", OS: "linux", Architecture: "arm64"})
	})
	if err != nil {
		t.Fatal(err)
	}
	selectRoute := func(machine string, old *pb.Resource, profile *pb.Resource) *pb.Resource {
		t.Helper()
		mutation := &pb.Mutation{RequestId: string(domain.NewID())}
		if old != nil {
			mutation.Id, mutation.ExpectedRevision = old.Id, old.Revision
		}
		req := &pb.SelectNetworkProfileRequest{Mutation: mutation, MachineId: machine}
		if profile != nil {
			req.ProfileId, req.ProfileRevision = profile.Id, profile.Revision
		}
		reply, err := c.SelectNetworkProfile(ctx, ownerRequest(f.service.Identity, req))
		if err != nil {
			t.Fatal(err)
		}
		return reply.Msg.Resource
	}
	worker := selectRoute(string(machine), nil, profile)
	p, key, err := f.service.outboundResolver()(ctx)
	if err != nil || p.Mode != domain.ProxyDirect || len(key) != 0 {
		t.Fatal("Worker affected server", p, err)
	}
	server := selectRoute("", nil, profile)
	p, key, err = f.service.outboundResolver()(ctx)
	if err != nil || string(key) != outboundtest.Credential || p.CredentialGeneration != requestID {
		t.Fatal("server routing", p, err)
	}
	clear(key)
	changedSecret := `{"username":"new-fixture-user","password":"new-fixture-password"}`
	edited, err := save(&pb.Mutation{RequestId: string(domain.NewID()), Id: profile.Id, ExpectedRevision: profile.Revision}, changedSecret)
	if err != nil {
		t.Fatal(err)
	}
	p, key, err = f.service.outboundResolver()(ctx)
	if err != nil || string(key) != outboundtest.Credential || p.CredentialGeneration != requestID {
		t.Fatal("edit changed pinned route", p, err)
	}
	clear(key)
	puts := vault.puts
	if _, err := save(&pb.Mutation{RequestId: string(domain.NewID()), Id: profile.Id, ExpectedRevision: 1}, changedSecret); connect.CodeOf(err) != connect.CodeAborted || vault.puts != puts {
		t.Fatal("stale mutation wrote vault", err)
	}
	deleteRequest := &pb.DeleteNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: profile.Id, ExpectedRevision: edited.Msg.Resource.Revision}}
	if _, err := c.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, deleteRequest)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("deleted selected profile", err)
	}
	export, err := c.ExportWorkerNetworkMetadata(ctx, ownerRequest(f.service.Identity, &pb.ExportWorkerNetworkMetadataRequest{MachineId: string(machine), DesiredGeneration: worker.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	var metadata workerNetworkMetadata
	if domain.Decode(export.Msg.MetadataJson, &metadata) != nil || metadata.ServerID != f.service.Identity.ServerID || metadata.MachineID != machine || metadata.Route.ProfileRevision != 1 || metadata.ExpiresAt.Sub(metadata.IssuedAt) != 5*time.Minute {
		t.Fatal("export scope")
	}
	mac := hmac.New(sha256.New, []byte(f.service.Identity.Token))
	mac.Write([]byte("delidev/worker-network-export/v1\x00"))
	mac.Write(export.Msg.MetadataJson)
	if export.Msg.Authentication != base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) || bytes.Contains(export.Msg.MetadataJson, []byte(outboundtest.Password)) {
		t.Fatal("unauthenticated/secret export")
	}
	if _, err := c.ExportWorkerNetworkMetadata(ctx, ownerRequest(f.service.Identity, &pb.ExportWorkerNetworkMetadataRequest{MachineId: string(machine), DesiredGeneration: worker.Revision + 1})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("export wrong generation", err)
	}
	selectRoute(string(machine), worker, nil)
	selectRoute("", server, nil)
	vault.deleteError = domain.Fail(domain.Unavailable, "Native cleanup unavailable.", "")
	if _, err := c.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, deleteRequest)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("cleanup failure hidden", err)
	}
	if _, err := f.service.Store.Get(ctx, domain.NetworkProfileKind, domain.ID(profile.Id)); err == nil {
		t.Fatal("cleanup failure resurrected profile")
	}
	vault.deleteError = nil
	deleted, err := c.DeleteNetworkProfile(ctx, ownerRequest(f.service.Identity, deleteRequest))
	if err != nil || !deleted.Msg.Deleted || !deleted.Msg.Replayed || len(vault.values) != 0 {
		t.Fatal("deletion retry", err)
	}
	paths, _ := filepath.Glob(filepath.Join(f.root, "state.sqlite*"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{outboundtest.Password, outboundtest.Username, "new-fixture-password", "new-fixture-user"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("credential in SQLite", path)
			}
		}
	}
}

func TestNetworkMutationUncertainVaultAndWorkerAuthorization(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}, putError: domain.Fail(domain.Unavailable, "Unknown native write outcome.", "")}
	f.service.accountSecrets = vault
	c := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
	ctx := context.Background()
	req := &pb.SaveNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DocumentJson: []byte(`{"name":"Retry","mode":"socks5","host":"127.0.0.1","port":1080}`), CredentialJson: []byte(outboundtest.Credential)}
	if _, err := c.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, req)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	if _, err := f.service.Store.Get(ctx, domain.NetworkProfileKind, domain.ID(req.Mutation.RequestId)); err == nil {
		t.Fatal("published uncertain credential")
	}
	vault.putError = nil
	if _, err := c.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, req)); err != nil || len(vault.values) != 1 {
		t.Fatal("exact recovery", err)
	}
	// Retained/staged generations are bounded, but an exact staged retry remains
	// recoverable at capacity without allocating another protected reference.
	owner := domain.ID(req.Mutation.RequestId)
	vault.mu.Lock()
	for n := 1; n < 256; n++ {
		vault.values[proxyRef(owner, domain.NewID())] = []byte(outboundtest.Credential)
	}
	puts := vault.puts
	vault.mu.Unlock()
	update := &pb.SaveNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(owner), ExpectedRevision: 1}, SchemaVersion: 1, DocumentJson: req.DocumentJson, CredentialJson: []byte(outboundtest.Credential)}
	if _, err := c.SaveNetworkProfile(ctx, ownerRequest(f.service.Identity, update)); connect.CodeOf(err) != connect.CodeResourceExhausted || vault.puts != puts {
		t.Fatal("unbounded credential generations", err)
	}
	worker := domain.WithPrincipal(ctx, domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := f.service.GetNetworkRoute(worker, connect.NewRequest(&pb.GetNetworkRouteRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker product read", err)
	}
	if _, err := f.service.SaveNetworkProfile(worker, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker product write", err)
	}
}
