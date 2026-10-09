package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestExecutionGrantAndNativeReferenceIsolationSurviveRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	digest := sha256.Sum256([]byte("private-fixture-execution-token"))
	grant := ExecutionGrant{JobID: domain.NewID(), Digest: digest[:], ExecutionID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID(), ServerEpoch: domain.NewID()}
	ref := ExecutionReference{SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ModelID: domain.ModelIdentity{ProviderID: domain.NewID(), NativeID: "fixture"}.Key(), Kind: domain.NativeResponseReference, NativeID: "resp_fixture"}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.authority", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, ref.SessionID, 0, ref.SessionID, "", struct{}{}); err != nil {
			return nil, err
		}
		if _, err := tx.PutJob(grant.JobID, 0, ref.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: grant.MachineID, InstanceID: grant.InstanceID, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.PutExecutionGrant(grant); err != nil {
			return nil, err
		}
		return struct{}{}, tx.ObserveExecutionReference(ref)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.repeat-authority", nil, func(tx *Tx) (any, error) {
		if err := tx.PutExecutionGrant(grant); err != nil {
			return nil, err
		}
		return struct{}{}, tx.ObserveExecutionReference(ref)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		stored, err := tx.ExecutionGrant(digest[:])
		if err != nil {
			return err
		}
		if stored.JobID != grant.JobID || stored.ExecutionID != grant.ExecutionID || stored.ServerEpoch != grant.ServerEpoch {
			t.Fatal("execution binding changed across restart")
		}
		byJob, err := tx.ExecutionGrantForJob(grant.JobID)
		if err != nil || byJob.JobID != stored.JobID || byJob.ExecutionID != stored.ExecutionID || byJob.MachineID != stored.MachineID || byJob.InstanceID != stored.InstanceID || byJob.DeviceID != stored.DeviceID || byJob.ServerEpoch != stored.ServerEpoch || byJob.Digest != nil {
			t.Fatal("owner control lookup lost grant scope or disclosed its digest")
		}
		if _, err := tx.ExecutionGrantForJob(domain.NewID()); domain.SafeError(err).Code != domain.PermissionDenied {
			t.Fatal("missing native authority was accepted")
		}
		for _, field := range []string{"same", "session", "account", "connection", "model", "kind", "native"} {
			candidate := ref
			switch field {
			case "session":
				candidate.SessionID = domain.NewID()
			case "account":
				candidate.AccountID = domain.NewID()
			case "connection":
				candidate.ConnectionID = domain.NewID()
			case "model":
				candidate.ModelID = domain.ModelIdentity{ProviderID: domain.NewID(), NativeID: "fixture"}.Key()
			case "kind":
				candidate.Kind = domain.NativeConversationReference
			case "native":
				candidate.NativeID = "resp_foreign"
			}
			exists, err := tx.HasExecutionReference(candidate)
			if err != nil || exists != (field == "same") {
				t.Fatalf("native reference escaped %s isolation: %v", field, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := grant
	changed.ServerEpoch = domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.replace-grant", nil, func(tx *Tx) (any, error) { return nil, tx.PutExecutionGrant(changed) })
	assertCode(t, err, domain.Conflict)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-authority", nil, func(tx *Tx) (any, error) {
		if err := tx.Delete(domain.JobKind, grant.JobID, 1); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.SessionKind, ref.SessionID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"execution_grants", "execution_references"} {
		var retained int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&retained); err != nil || retained != 0 {
			t.Fatal("deletion retained execution authority or native references")
		}
	}
}
