// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"testing"
)

func sshObserved(t *testing.T, f *oauthFixture) store.Record {
	t.Helper()
	var record store.Record
	_, err := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.ssh", nil, func(tx *store.Tx) (any, error) {
		var e error
		record, e = tx.Put(domain.SSHSetupKind, domain.NewID(), 0, "", "", sshOperation{ServerID: f.s.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, Target: sshsetup.Target{Host: "127.0.0.1", Port: 22, User: "fixture"}, Identity: sshsetup.Identity{Algorithm: "ssh-ed25519", Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}, State: installationObserved})
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func TestSSHSetupAdmissionReplayAndExactHost(t *testing.T) {
	f := newOAuthFixture(t)
	r := sshObserved(t, f)
	credential, _ := json.Marshal(sshsetup.Credential{Method: sshsetup.Password, Secret: []byte("write-only-sentinel")})
	var o sshOperation
	_ = domain.Decode(r.Data, &o)
	request := func(id domain.ID, pin string, revision uint64) *pb.StartSSHSetupRequest {
		return &pb.StartSSHSetupRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: revision, RequestId: string(id)}, Credential: append([]byte(nil), credential...), Name: "Fixture", ConfirmedFingerprint: pin}
	}
	if _, err := f.s.StartSSHSetup(f.ctx, connect.NewRequest(request(domain.NewID(), o.Identity.Fingerprint, r.Revision))); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("Unset production root did not reject: %v", err)
	}
	if len(f.vault.values) != 0 {
		t.Fatal("Credential consumed before release authority")
	}
	f.s.releaseFactory = func() (releaseClient, error) { return fixtureRelease{}, nil }
	if _, err := f.s.StartSSHSetup(f.ctx, connect.NewRequest(request(domain.NewID(), "SHA256:changed", r.Revision))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("Changed pin accepted", err)
	}
	if len(f.vault.values) != 0 {
		t.Fatal("Credential staged before pin validation")
	}
	id := domain.NewID()
	accepted, err := f.s.StartSSHSetup(f.ctx, connect.NewRequest(request(id, o.Identity.Fingerprint, r.Revision)))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.StartSSHSetup(f.ctx, connect.NewRequest(request(id, o.Identity.Fingerprint, r.Revision)))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Setup.Id != accepted.Msg.Setup.Id {
		t.Fatal("Missing receipt replay", err)
	}
	foreign := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err = f.s.GetSSHSetup(foreign, connect.NewRequest(&pb.GetSSHSetupRequest{Id: string(r.ID)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker read product setup", err)
	}
	canceled, err := f.s.CancelSSHSetup(f.ctx, connect.NewRequest(&pb.CancelSSHSetupRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: accepted.Msg.Setup.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	var current sshOperation
	_ = domain.Decode(canceled.Msg.Setup.DocumentJson, &current)
	if current.State != installationCanceled {
		t.Fatal("Undispatched setup not canceled")
	}
	if _, err = os.Lstat(f.s.sshClaimPath(r.ID)); !os.IsNotExist(err) {
		t.Fatal("Cancellation granted native claim")
	}
	f.s.removeSSHCredentials(f.ctx, r.ID, current)
	read, err := f.s.GetSSHSetup(f.ctx, connect.NewRequest(&pb.GetSSHSetupRequest{Id: string(r.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	current = sshOperation{}
	_ = domain.Decode(read.Msg.Setup.DocumentJson, &current)
	if !current.CredentialRemoved {
		t.Fatal("Protected cleanup not confirmed")
	}
}
func TestSSHExternalClaimSurvivesDatabaseStateRollback(t *testing.T) {
	f := newOAuthFixture(t)
	r := sshObserved(t, f)
	var o sshOperation
	_ = domain.Decode(r.Data, &o)
	o.StartRequestID = domain.NewID()
	o.Name = "Accepted original"
	o.State = installationRunning
	claim := sshClaim{Version: 1, ID: r.ID, Operation: o, InputSHA256: sshOperationDigest(o)}
	directory := f.s.sshClaimPath(r.ID)
	if err := security.PrivateDir(f.root + "/ssh-setup-claims"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(claim)
	if err := security.WriteAtomic(directory, raw); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recoverSSHClaims(f.ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.GetSSHSetup(f.ctx, connect.NewRequest(&pb.GetSSHSetupRequest{Id: string(r.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	var current sshOperation
	_ = domain.Decode(got.Msg.Setup.DocumentJson, &current)
	if current.State != installationUncertain || current.StartRequestID != o.StartRequestID || current.ReconcileRequested {
		t.Fatal("Rolled-back state granted another send")
	}
}
