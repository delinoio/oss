// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (f *grokFixture) jobCount(t *testing.T) int {
	t.Helper()
	count := 0
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		jobs, err := tx.Jobs("", "", "", "", 100)
		count = len(jobs)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// restoreGrokImage backs up the current database, lets settle edit the current
// account so restore preflight can pass, restores the image and reopens the
// store under the same service. It returns the bytes of the backup image.
func (f *grokFixture) restoreGrokImage(t *testing.T, settle func(*domain.Account)) []byte {
	t.Helper()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	s := f.service.Store
	if err := s.BindIdentity(ctx, f.service.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(filepath.Join(s.Root(), "backups", string(backup)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, f.service.Identity.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	// The current database settles after the image was taken, so unsettled
	// ownership exists only in the image.
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.grok.settle", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		settle(&a)
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		// The authority fixture's claimed execution would block restore preflight;
		// restore never terminates original work, so it is removed beforehand.
		sr, err := tx.Get(domain.SessionKind, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		if err := tx.Delete(sr.Kind, sr.ID, sr.Revision); err != nil {
			return nil, err
		}
		jobs, err := tx.Jobs("", "", "", "", 100)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if err := tx.Delete(job.Kind, job.ID, job.Revision); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := store.BackupRestoreInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ServerID: f.service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), input); err != nil {
		t.Fatal(err)
	}
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.service.Store = reopened
	return image
}

// A Grok account holding a protected bundle is backed up without any credential
// material. After restore the account is disconnected and recovery-required, the
// external vault is untouched, and no Worker can take, finish or delete it.
func TestGrokBackupRestoreQuarantinesAccountWithoutCredentialMaterialOrWorkerAuthority(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	_, before := f.record()
	commitment := before.Subscription.IdentityCommitment
	envelope, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil || len(envelope) == 0 {
		t.Fatal("fixture vault holds no protected Grok bundle", err)
	}
	// A pending logout is queued so the restored account also owns a pending
	// operation that no Worker may claim.
	pending := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	image := f.restoreGrokImage(t, func(a *domain.Account) { a.Subscription.Pending = nil })

	for _, material := range [][]byte{[]byte("synthetic-key"), []byte("synthetic-refresh"), []byte(grokServerBundle(t, "synthetic-principal-1").IdentityKey()), envelope} {
		if bytes.Contains(image, material) {
			t.Fatal("backup image contains Grok credential material")
		}
	}
	for _, evidence := range []string{string(generation), commitment} {
		if !bytes.Contains(image, []byte(evidence)) {
			t.Fatal("backup image lost the Grok ownership evidence the byte search must not confuse with a secret")
		}
	}

	r, restored := f.record()
	st := restored.Subscription
	if restored.Validate() != nil || restored.Connection != nil || restored.Health != domain.AccountDisconnected || restored.SubscriptionService != domain.SubscriptionGrok {
		t.Fatal("restored Grok account kept usable authentication")
	}
	if st == nil || !st.RecoveryRequired || st.Generation != generation || st.IdentityCommitment != commitment || st.Pending == nil || string(st.Pending.ID) != pending.OperationId {
		t.Fatal("restored Grok account lost its quarantined evidence")
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID); err != nil || len(refs) != 1 {
		t.Fatal("restore touched the external vault", err)
	}

	// Take is refused for the pending operation on its original Worker, so the
	// restored account never redistributes the bundle or grants a lease.
	if lease, err := f.take(pending, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err == nil || lease != nil {
		t.Fatal("Take admitted a restored recovery-required Grok account")
	}
	// A Finish for any lease, including a forged copy of the original one, is
	// refused and cannot clear the fence or the generation.
	forged := &pb.TakeSubscriptionResponse{LeaseId: string(domain.NewID()), GenerationId: string(generation), LeaseRevision: r.Revision}
	if _, err := f.finishWith(forged, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("Finish accepted a restored Grok account")
	}
	_, after := f.record()
	if !reflect.DeepEqual(restored.Subscription, after.Subscription) || after.Connection != nil || after.Health != domain.AccountDisconnected {
		t.Fatal("a refused Take or Finish changed the restored Grok account")
	}
	// Execution admission requires a ready connection that restore removed.
	if after.Health == domain.AccountReady || after.Connection != nil || after.Subscription.Lease != nil {
		t.Fatal("restored Grok account is admissible for execution")
	}

	// Deletion stays refused while the vault holds the historical generation and
	// never creates a native logout job on a Worker without the original home.
	jobs := f.jobCount(t)
	request := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(f.input.AccountID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}
	if err := f.deleteFailedLogin(request); err == nil || connect.CodeOf(err) == connect.CodeUnknown {
		t.Fatal("deletion ignored the protected Grok references", err)
	} else if got := domain.SafeError(rpc.ClientError(err)); got.Code != domain.Conflict {
		t.Fatal("deletion was not refused by protectedAccountDeletion", got.Code)
	}
	if _, kept := f.record(); kept.Subscription == nil || !kept.Subscription.RecoveryRequired {
		t.Fatal("refused deletion changed the restored Grok account")
	}
	if f.jobCount(t) != jobs {
		t.Fatal("deletion queued a native logout job")
	}
}

// An unsettled Grok cleanup (lease plus pending logout) in the image is retained
// as quarantined evidence with its original owner identity.
func TestGrokBackupRestoreRetainsUnsettledCleanupOwnerFields(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	_, original := f.record()
	lease := *original.Subscription.Lease
	if lease.MachineID != f.input.MachineID || lease.InstanceID != f.instance || lease.Epoch == "" || original.Subscription.Pending == nil {
		t.Fatal("fixture has no unsettled Grok cleanup")
	}
	f.restoreGrokImage(t, func(a *domain.Account) { a.Subscription.Lease, a.Subscription.Pending = nil, nil })

	_, restored := f.record()
	st := restored.Subscription
	if restored.Validate() != nil || restored.Connection != nil || restored.Health != domain.AccountDisconnected || st == nil || !st.RecoveryRequired {
		t.Fatal("restored unsettled Grok cleanup is not quarantined")
	}
	if st.Lease == nil || !reflect.DeepEqual(*st.Lease, lease) || !reflect.DeepEqual(st.Pending, original.Subscription.Pending) {
		t.Fatal("restore rewrote the original Grok cleanup owner machine, device, instance or epoch")
	}
	// No other Worker, and not even the original one, is granted the cleanup.
	if _, err := f.finishWith(&pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), GenerationId: string(lease.Generation), LeaseRevision: lease.Revision}, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("restore granted a Worker authority to finish the quarantined Grok cleanup")
	}
	if _, after := f.record(); !reflect.DeepEqual(after.Subscription, restored.Subscription) {
		t.Fatal("a refused Finish changed the quarantined Grok cleanup")
	}
}

// Once recovery has cleared a restored Grok account's subscription state, the
// vault still gates deletion (protectedAccountDeletion) until every protected
// reference is removed. Deletion never queues a native logout on any Worker.
func TestGrokDisconnectedAccountDeletionIsGatedByVaultAndNeverRequestsNativeLogout(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	envelope, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	f.restoreGrokImage(t, func(a *domain.Account) {})
	// The restored account itself is refused by the recovery fence.
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err == nil {
		t.Fatal("deletion accepted a recovery-required Grok account")
	}
	// A disconnected, unreferenced Grok account owns no subscription evidence,
	// but the external vault still holds a reference.
	_, restored := f.record()
	restored.Subscription, restored.Connection, restored.Health = nil, nil, domain.AccountDisconnected
	owner := domain.NewID()
	if _, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.grok.recovered.account", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, owner, 0, "", "", restored)
	}); err != nil {
		t.Fatal(err)
	}
	ref := credentials.Ref{Owner: owner, ID: generation, Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, envelope); err != nil {
		t.Fatal(err)
	}
	deletion := func() *pb.DeleteConfigurationRequest {
		var r store.Record
		if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) (err error) {
			r, err = tx.Get(domain.AccountKind, owner)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(owner), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}
	}
	jobs := f.jobCount(t)
	// A protected reference that is not an AccountLogin generation keeps the
	// account: subscription deletion never removes another purpose.
	protected := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountAPI}
	if _, err := f.secrets.Put(context.Background(), protected, []byte("synthetic-api-key")); err != nil {
		t.Fatal(err)
	}
	err = f.deleteFailedLogin(deletion())
	if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict || !strings.Contains(err.Error(), "retains protected credential intents") {
		t.Fatal("deletion ignored the unremoved vault reference", err)
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), owner); err != nil || len(refs) != 2 {
		t.Fatal("refused deletion changed the vault", err)
	}
	if err := f.secrets.Delete(context.Background(), protected); err != nil {
		t.Fatal(err)
	}
	// The retained AccountLogin generation is removed by the explicit deletion.
	if err := f.deleteFailedLogin(deletion()); err != nil {
		t.Fatal("deletion failed with only a retained generation", err)
	}
	if refs, err := f.secrets.UnremovedReferences(context.Background(), owner); err != nil || len(refs) != 0 {
		t.Fatal("explicit deletion left a retained generation", err)
	}
	if f.jobCount(t) != jobs {
		t.Fatal("deletion queued a native logout job")
	}
}
