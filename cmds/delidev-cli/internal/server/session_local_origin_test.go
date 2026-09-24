package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func localOriginFixture(t *testing.T) (*Service, *accountFixture, domain.CreateSession, security.Identity) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: randomCode()}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	h := httptest.NewServer(s.Handler(nil, true))
	t.Cleanup(h.Close)
	f := &accountFixture{t: t, root: root, endpoint: Endpoint{URL: h.URL, ServerID: s.Identity.ServerID}, identity: s.Identity}
	f.config = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, h.URL)
	f.resources = delidevv1connect.NewResourceServiceClient(http.DefaultClient, h.URL)
	input, credential := sessionSelection(t, f)
	repo := domain.NewID()
	checkout := filepath.Join(t.TempDir(), "checkout")
	_, err = db.Mutate(context.Background(), domain.NewID(), "fixture.repository", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, repo, 0, "", "", domain.Repository{Name: "Local fixture", Checkouts: []domain.Checkout{{MachineID: input.MachineID, Path: checkout}}, AutoFetch: true, Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "missing", Name: "not-selected"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	project := f.save(pb.EntityKind_ENTITY_KIND_PROJECT, domain.Project{Name: "Local project", Repositories: []domain.ID{repo}, PrimaryRepository: repo})
	input.ProjectID, input.Workspace = domain.ID(project.Id), domain.Local
	return s, f, input, credential
}

func localOriginClient(t *testing.T, f *accountFixture) security.Identity {
	t.Helper()
	client := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	code, token := randomCode(), randomCode()
	codeDigest, tokenDigest := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	grant, err := client.CreatePairing(context.Background(), ownerRequest(f.identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "Local client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeDigest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PairDevice(context.Background(), connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenDigest[:]})); err != nil {
		t.Fatal(err)
	}
	return security.Identity{Token: token}
}

func TestLocalSessionOriginIsAuthenticatedRetainedAndReferenceOnly(t *testing.T) {
	s, f, input, credential := localOriginFixture(t)
	ctx := context.Background()
	creator := localOriginClient(t, f)
	raw, _ := json.Marshal(input)
	req := &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw, LocalWorkerToken: credential.Token}
	response, err := sessionClient(f).CreateSession(ctx, ownerRequest(creator, req))
	if err != nil {
		t.Fatal(err)
	}
	change := response.Msg.Change
	session := sessionBody(t, change.Session)
	digest := sha256.Sum256([]byte(credential.Token))
	actor, err := s.Store.Authenticate(ctx, digest[:])
	if err != nil || session.LocalOrigin == nil || session.LocalOrigin.MachineID != input.MachineID || session.LocalOrigin.DeviceID != actor.DeviceID {
		t.Fatal("origin was not derived from Worker authentication", err)
	}
	var job domain.Job
	var preparation workspace.PrepareRequest
	if domain.Decode(change.WorkspaceJob.DocumentJson, &job) != nil || domain.Decode(job.Input, &preparation) != nil || preparation.OriginMachineID != input.MachineID || preparation.Repositories[0].AutoFetch || preparation.Repositories[0].Starting.Type != "" {
		t.Fatal("Local job selected remote refs or lost origin")
	}
	if _, err := sessionClient(f).CreateSession(ctx, ownerRequest(f.identity, req)); err == nil {
		t.Fatal("another client reused original Local creation receipt")
	}
	stopped, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_STOP}))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := sessionClient(f).CreateSession(ctx, ownerRequest(creator, req))
	if err != nil || !retry.Msg.Change.Replayed || retry.Msg.Change.Session.Id != change.Session.Id || sessionBody(t, retry.Msg.Change.Session).Dispatch != domain.DispatchPaused {
		t.Fatal("Local retry recreated or resumed a session", err)
	}
	if *sessionBody(t, stopped.Msg.Change.Session).LocalOrigin != *session.LocalOrigin {
		t.Fatal("later client control changed Local origin")
	}
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(filepath.Join(f.root, "state.sqlite") + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(credential.Token)) {
			t.Fatal("origin credential persisted in server data")
		}
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "fixture.revoke-origin", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.DeviceKind, actor.DeviceID)
		if e != nil {
			return nil, e
		}
		device, e := store.Decode[domain.Device](r)
		if e != nil {
			return nil, e
		}
		device.Revoked = true
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", device); e != nil {
			return nil, e
		}
		return nil, tx.RevokeCredential(actor.DeviceID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessionClient(f).CreateSession(ctx, ownerRequest(creator, req)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked origin credential replayed acceptance", err)
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return validateLocalOrigin(tx, session) }); err == nil {
		t.Fatal("revoked origin authorized future execution")
	}
}

func TestLocalSessionRefusesMachineAssertionsAndForeignAuthority(t *testing.T) {
	s, f, input, credential := localOriginFixture(t)
	foreign, _ := pairedWorker(t, context.Background(), f.endpoint, f.identity)
	client := localOriginClient(t, f)
	for _, token := range []string{"", "malformed", randomCode(), f.identity.Token, foreign.Token, client.Token} {
		raw, _ := json.Marshal(input)
		_, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw, LocalWorkerToken: token}))
		if err == nil {
			t.Fatal("unproven origin created Local work")
		}
	}
	input.Workspace = domain.Worktree
	raw, _ := json.Marshal(input)
	if _, err := sessionClient(f).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw, LocalWorkerToken: credential.Token})); err == nil {
		t.Fatal("Worktree accepted Local authority")
	}
	if err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		records, err := tx.List(store.Filter{Kind: domain.SessionKind, Limit: 10})
		if err != nil {
			return err
		}
		if len(records) != 0 {
			t.Fatal("rejected origin left partial session state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
