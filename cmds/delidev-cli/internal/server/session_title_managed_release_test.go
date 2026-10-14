// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// requireTitleNotDelivered asserts the Worker-visible result of a managed title
// Take whose exact lease was released without delivering a credential: a typed
// no-delivery proof that is never the RecoveryRequired code the Worker treats as
// an uncertain execution.
func requireTitleNotDelivered(t *testing.T, err error, code domain.Code) {
	t.Helper()
	got := rpc.ClientError(err)
	if got.Cause != subscriptionTitleNotDeliveredCause || got.Code != code || got.Code == domain.RecoveryRequired {
		t.Fatalf("a released undelivered title did not return a definite no-delivery result: %+v", got)
	}
}

// vaultReadFault replaces the protected bundle read with a failure or a corrupt
// bundle, after the hook (if any) ran.
type vaultReadFault struct {
	accountSecrets
	before func()
	err    error
	bundle []byte
}

func (v *vaultReadFault) Get(_ context.Context, _ credentials.Ref) ([]byte, error) {
	if v.before != nil {
		v.before()
	}
	if v.err != nil {
		return nil, v.err
	}
	return append([]byte(nil), v.bundle...), nil
}

// Every vault-side failure after the lease commits is a definite no-delivery
// outcome: the exact lease is released, the account stays ready, and the Worker
// receives the typed no-delivery proof instead of a RecoveryRequired denial.
func TestManagedTitleVaultFailureBeforeDeliveryReturnsDefiniteResult(t *testing.T) {
	cases := map[string]*vaultReadFault{
		"read-failed":    {err: errors.New("vault read failed")},
		"invalid-bundle": {bundle: []byte("not a protected bundle")},
	}
	for name, fault := range cases {
		t.Run(name, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			record, err := f.claim()
			if err != nil {
				t.Fatal(err)
			}
			fault.accountSecrets = f.secrets
			f.service.accountSecrets = fault
			defer func() { f.service.accountSecrets = f.secrets }()
			response, err := f.takeTitle(record.Revision)
			if err == nil || response != nil {
				t.Fatal("a title received credentials from a failing vault")
			}
			requireTitleNotDelivered(t, err, domain.Unavailable)
			if _, account := f.record(); account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Health != domain.AccountReady {
				t.Fatalf("an undelivered title lease fenced the account: health=%s %+v", account.Health, account.Subscription)
			}
		})
	}
}

// The Worker reports the definite result as an ordinary problem: the title
// settles without a recovery fence and without waiting for reconciliation.
func TestManagedTitleDefiniteNoDeliveryReportsNormally(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	f.service.accountSecrets = &vaultReadFault{accountSecrets: f.secrets, err: errors.New("vault read failed")}
	defer func() { f.service.accountSecrets = f.secrets }()
	_, err = f.takeTitle(record.Revision)
	problem := rpc.ClientError(err)
	requireTitleNotDelivered(t, err, domain.Unavailable)
	if err := f.report(record, nil, problem); err != nil {
		t.Fatal("the definite no-delivery result could not be reported", err)
	}
	session := f.session(t)
	if session.Name != "New session" || session.TitleState != domain.TitleFailed || session.TitleReason != domain.TitleReasonInferenceFailed {
		t.Fatalf("a definite no-delivery title did not settle as a plain failure: %+v", session)
	}
	if _, job := f.titleRecord(t); job.State != domain.JobFailed {
		t.Fatalf("a definite no-delivery title became %s", job.State)
	}
	if _, account := f.record(); account.Subscription.Lease != nil || account.Subscription.RecoveryRequired {
		t.Fatalf("reporting a definite no-delivery title fenced the account: %+v", account.Subscription)
	}
}

// When the exact lease can no longer be released, the result stays the original
// error and the recovery fence applies: only a confirmed release is definite.
func TestManagedTitleUnconfirmedReleaseKeepsRecoveryFence(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	var hookErr error
	f.service.accountSecrets = &vaultReadFault{accountSecrets: f.secrets, err: errors.New("vault read failed"), before: func() {
		// Advance the lease revision so the exact-revision release cannot match.
		_, hookErr = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.advance-lease", nil, func(tx *store.Tx) (any, error) {
			r, a, err := accountFromTx(tx, f.input.AccountID, 0)
			if err != nil {
				return nil, err
			}
			a.Subscription.Lease.Revision++
			return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		})
	}}
	defer func() { f.service.accountSecrets = f.secrets }()
	response, err := f.takeTitle(record.Revision)
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if err == nil || response != nil {
		t.Fatal("a title received credentials from a failing vault")
	}
	if got := rpc.ClientError(err); got.Cause == subscriptionTitleNotDeliveredCause {
		t.Fatalf("an unconfirmed release was reported as definite: %+v", got)
	}
	if _, account := f.record(); account.Subscription.Lease == nil || !account.Subscription.RecoveryRequired {
		t.Fatalf("an unconfirmed release dropped the recovery fence: %+v", account.Subscription)
	}
}
