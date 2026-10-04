// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestTitleDiagnosticSendPublicationRollsBackOriginalHTTPClaim(t *testing.T) {
	ctx := context.Background()
	f := recoveredAutomaticTitleFixture(t)
	_, recovery := acceptRecovery(t, f)
	completeRecovery(t, f, recovery.ExecutionRecoveryJob)
	record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](record)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claimTitleJob(ctx, f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(token))
	_, err = f.client.RegisterExecution(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(claimed.ID), ExpectedRevision: claimed.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CredentialDigest: digest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.BeforeSubmit != nil || lease.PublishDiagnostic == nil {
		t.Fatal("title ownership was not composed with diagnostic publication")
	}
	attempted := false
	id := domain.NewID()
	scope := lease.Scope
	value := domain.RequestDiagnostic{ID: id, CorrelationID: id, SessionID: scope.SessionID, ExecutionID: scope.ExecutionID, AccountID: scope.AccountID, ConnectionID: scope.ConnectionID, ProviderID: scope.ProviderID, ModelID: scope.ModelID, Harness: scope.Harness, Purpose: scope.Purpose, Source: domain.DiagnosticProxyHTTP, Operation: domain.DiagnosticResponse, State: domain.DiagnosticInProgress, ObservedAt: time.Now().UTC(), HTTPAttempted: &attempted}
	for i := 0; i < 2; i++ {
		if err := lease.PublishDiagnostic(ctx, value); err != nil {
			t.Fatal(err)
		}
		value.Revision++
	}
	// Inject a real SQLite write failure after the HTTP claim insert. The
	// diagnostic, claim and event must all roll back at this one boundary.
	db, err := sql.Open("sqlite", filepath.Join(f.service.Store.Root(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fixture_title_diagnostic_failure BEFORE INSERT ON request_diagnostics WHEN json_extract(CAST(NEW.body AS TEXT),'$.http_attempted')=1 BEGIN SELECT RAISE(ABORT,'fixture publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	attempted = true
	if err := lease.PublishDiagnostic(ctx, value); err == nil {
		t.Fatal("injected diagnostic failure did not reject send publication")
	}
	var retained domain.RequestDiagnostic
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		consumed, err := tx.TitleHTTPRequestClaimed(claimed.ID)
		if err != nil {
			return err
		}
		if consumed {
			t.Fatal("failed diagnostic publication consumed original title HTTP authority")
		}
		retained, err = tx.RequestDiagnostic(id)
		return err
	})
	if err != nil || retained.Revision != 2 || retained.HTTPAttempted == nil || *retained.HTTPAttempted {
		t.Fatal("failed send publication changed retained metadata", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fixture_title_diagnostic_failure`); err != nil {
		t.Fatal(err)
	}
	if err := lease.PublishDiagnostic(ctx, value); err != nil {
		t.Fatal("original unconsumed HTTP authority was stranded", err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		consumed, err := tx.TitleHTTPRequestClaimed(claimed.ID)
		if err != nil {
			return err
		}
		if !consumed {
			t.Fatal("send metadata committed without original title HTTP claim")
		}
		retained, err = tx.RequestDiagnostic(id)
		return err
	})
	if err != nil || retained.Revision != 3 || retained.HTTPAttempted == nil || !*retained.HTTPAttempted {
		t.Fatal("composed send publication lost original metadata", err)
	}
	if err := lease.PublishDiagnostic(ctx, value); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("a later publication consumed title authority again", err)
	}
}
